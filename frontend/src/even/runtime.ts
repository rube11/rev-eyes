import {
  AppLocationAccuracy,
  AudioInputSource,
  OsEventTypeList,
} from "@evenrealities/even_hub_sdk"
import type {
  AppLocation,
  EvenHubEvent,
  RebuildPageContainer,
} from "@evenrealities/even_hub_sdk"

import {
  buildCompactPage,
  buildMessagePage,
  buildSleepPage,
  buildTranscriptContent,
  buildTranscriptPage,
  presentGlassesMessage,
} from "./glasses-ui"
import type { GlassesMessage } from "./glasses-ui"
import {
  CandidateAudioClient,
  type CandidateFinalizedEvent,
  type MoonshineRunResult,
} from "./candidate-audio-client"
import { AssistantConversationState } from "./assistant-conversation-state"
import { AssistantResponseLifecycle } from "./assistant-response-lifecycle"
import { FocusedCandidateTracker } from "./focused-candidate-tracker"
import {
  getEvenBridge,
  renderGlassesPage,
  resumeGlassesPage,
  upgradeTranscriptText,
} from "./glasses-page-host"
import {
  parseRealtimeServerMessage,
  type RealtimeServerMessage,
} from "./realtime-protocol"
import {
  closeSocketQuietly,
  closeUnadoptedSocket,
  reconnectDelay,
  safeSend,
  safeSendJson,
  socketIsOpen as isRealtimeSocketOpen,
} from "./realtime-socket"
import type { WorkspaceResource } from "../features/workspace/workspaceTypes"
import { connectRealtimeSocket } from "../shared/api/client"
import { env } from "../shared/config/env"

export { showEvenMessage } from "./glasses-page-host"

type ListeningState = "idle" | "starting" | "listening" | "stopping"
type DisplaySurface = "compact" | "message" | "offline" | "sleep" | "transcript"
type NotificationPresentation = {
  id: string
  message: GlassesMessage
}
type AssistantPresentation = {
  message: GlassesMessage
  awaitingConfirmation: boolean
  sourceText: string
}
type SocketBinding = {
  socket: WebSocket
  handleClose: () => void
  handleMessage: (event: MessageEvent<unknown>) => void
}

const THINKING_FRAME_DELAY_MS = 480
const CONNECTION_TIMEOUT_MS = 10_000
const RELEASE_CLICK_SUPPRESSION_MS = 750

function sendLocation(socket: WebSocket | undefined, location: AppLocation) {
  return safeSendJson(socket, {
    type: "location",
    latitude: location.latitude,
    longitude: location.longitude,
    accuracy_meters: location.accuracy,
  })
}


function isClickEvent(event: EvenHubEvent) {
  const listEvent = event.listEvent
  const listClick =
    listEvent !== undefined &&
    (listEvent.eventType ?? OsEventTypeList.CLICK_EVENT) ===
      OsEventTypeList.CLICK_EVENT
  const textEvent = event.textEvent
  const textClick =
    textEvent !== undefined &&
    (textEvent.eventType ?? OsEventTypeList.CLICK_EVENT) ===
      OsEventTypeList.CLICK_EVENT
  const systemClick =
    event.sysEvent !== undefined &&
    (event.sysEvent.eventType ?? OsEventTypeList.CLICK_EVENT) ===
      OsEventTypeList.CLICK_EVENT
  return listClick || textClick || systemClick
}

function isLongPressEvent(event: EvenHubEvent) {
  const raw = event.jsonData
  if (!raw) {
    return false
  }

  const gestureValues = [
    raw.eventType,
    raw.Event_Type,
    raw.gesture,
    raw.gestureType,
    raw.Gesture_Type,
    raw.action,
  ]
  const namedLongPress = gestureValues.some((value) => {
    if (typeof value !== "string") {
      return false
    }
    const normalized = value.replace(/[\s-]+/gu, "_").toUpperCase()
    return normalized.includes("LONG_PRESS") || normalized.includes("HOLD")
  })
  if (namedLongPress) {
    return true
  }

  const durationValues = [
    raw.durationMs,
    raw.pressDurationMs,
    raw.holdDurationMs,
  ]
  return durationValues.some((value) => {
    const duration = typeof value === "string" ? Number(value) : value
    return typeof duration === "number" && Number.isFinite(duration) && duration >= 800
  })
}

export async function initializeEvenExperience(
  accessToken: string,
  onResponse: (text: string) => void,
  onStatus: (status: string) => void,
  onWorkspaceChanged: (resources: readonly WorkspaceResource[]) => void =
    () => undefined,
  onConnected: () => void = () => undefined,
): Promise<() => void> {
  resumeGlassesPage()
  let active = true
  let transitionTail: Promise<void> = Promise.resolve()
  let socket: WebSocket | undefined
  let socketBinding: SocketBinding | undefined
  let listeningState: ListeningState = "idle"
  const focusedCandidate = new FocusedCandidateTracker()
  let surface: DisplaySurface = "compact"
  let transcriptLayoutHasBody = false
  let locationStarted = false
  let awaitingResponse = false
  let sleeping = false
  let visibleAssistant: AssistantPresentation | undefined
  let deferredAssistant: AssistantPresentation | undefined
  let lastAssistant: AssistantPresentation | undefined
  const assistantConversation = new AssistantConversationState()
  let currentNotification: NotificationPresentation | undefined
  const notificationQueue: NotificationPresentation[] = []
  const seenNotificationIds = new Set<string>()
  const dismissedNotificationIds = new Set<string>()
  let idlePrompt: string | undefined
  let latestTranscript = ""
  let thinking = false
  let thinkingFrame = 0
  let thinkingTimer: ReturnType<typeof setTimeout> | undefined
  let reconnectTimer: ReturnType<typeof setTimeout> | undefined
  let reconnectAttempt = 0
  let connecting = false
  let connectGeneration = 0
  let connectionAbort: AbortController | undefined
  let suppressClicksUntil = 0

  onStatus("Connecting")
  await renderGlassesPage(buildCompactPage("CONNECTING"))
  const bridge = await getEvenBridge()
  const candidateAudioEnabled = env.candidateAudioEnabled
  const continuousListeningEnabled =
    candidateAudioEnabled && env.continuousListeningEnabled
  const candidateAudio = new CandidateAudioClient({
    candidateAudioEnabled,
    debugTranscripts: env.moonshineDebugTranscripts,
    forwardDiagnostics:
      import.meta.env.DEV && env.moonshineDebugTranscripts,
    device: {
      start: () => bridge.audioControl(true, AudioInputSource.Glasses),
      stop: () => bridge.audioControl(false),
    },
    getSocket: () => socket,
    moonshineEnabled: env.moonshineShadowEnabled || candidateAudioEnabled,
    onCandidateFinalized: handleCandidateFinalized,
    onCandidateSent: handleCandidateSent,
    onRunComplete: handleMoonshineRunComplete,
    onVoiceReplyStarted: handleVoiceReplyStarted,
  })
  const responseLifecycle = new AssistantResponseLifecycle({
    onConversationExpired: handleResponseConversationExpired,
    onDisplayExpired: handleResponseDisplayExpired,
  })
  void candidateAudio.prepare().then((ready) => {
    if (!continuousListeningEnabled) {
      return
    }
    void enqueueTransition(async () => {
      if (!ready) {
        if (socketIsOpen() && !sleeping) {
          reportStatus("Local speech model unavailable")
          await showIdlePrompt("LOCAL MODEL UNAVAILABLE")
        }
        return
      }
      const started = await ensureContinuousCapture()
      if (
        started &&
        listeningState === "idle" &&
        !thinking &&
        !currentNotification &&
        !visibleAssistant
      ) {
        await showReady()
      }
    })
  })

  function reportStatus(status: string) {
    if (active) {
      onStatus(status)
    }
  }

  function enqueueTransition(transition: () => Promise<void>): Promise<void> {
    const result = transitionTail.then(async () => {
      if (active) {
        await transition()
      }
    })
    transitionTail = result.catch(() => {
      reportStatus("Glasses command failed")
    })
    return transitionTail
  }

  function socketIsOpen() {
    return isRealtimeSocketOpen(socket)
  }

  function sendControl(type: string) {
    return safeSendJson(socket, { type })
  }

  function cancelAssistantResponseWindow(): void {
    responseLifecycle.cancel()
    candidateAudio.setVoiceReplyArmed(false)
    assistantConversation.closeResponseWindow()
  }

  function resetAssistantInteraction(): void {
    responseLifecycle.cancel()
    candidateAudio.setVoiceReplyArmed(false)
    assistantConversation.reset()
  }

  function isCompetingCandidateMessage(messageID: string | undefined): boolean {
    return (
      continuousListeningEnabled &&
      focusedCandidate.competes(messageID)
    )
  }

  function handleCandidateSent(candidateID: string): void {
    const ownsInteraction = focusedCandidate.focus(
      candidateID,
      listeningState === "listening" || listeningState === "stopping",
    )
    if (!assistantConversation.replyActive || !ownsInteraction) {
      return
    }

    listeningState = "stopping"
    awaitingResponse = true
    reportStatus("Thinking")
    void enqueueTransition(async () => {
      if (
        !assistantConversation.replyActive ||
        !awaitingResponse ||
        sleeping ||
        currentNotification
      ) {
        return
      }
      await startThinkingAnimation()
    })
  }

  function handleCandidateFinalized(event: CandidateFinalizedEvent): void {
    if (
      event.category !== "manual" ||
      event.submitted ||
      !assistantConversation.replyActive
    ) {
      return
    }

    assistantConversation.finishReply()
    listeningState = "idle"
    focusedCandidate.clear()
    awaitingResponse = false
    reportStatus(socketIsOpen() ? "Connected" : "Reconnecting")
    void enqueueTransition(async () => {
      if (sleeping || currentNotification || listeningState !== "idle") {
        return
      }
      if (!socketIsOpen()) {
        await showConnectionLost()
        return
      }
      await showReady()
    })
  }

  function handleVoiceReplyStarted(): void {
    const replyAllowed =
      active &&
      !sleeping &&
      !currentNotification &&
      !thinking &&
      !awaitingResponse &&
      listeningState === "idle" &&
      socketIsOpen() &&
      assistantConversation.beginReply()
    if (!replyAllowed) {
      candidateAudio.discardPendingCandidate()
      cancelAssistantResponseWindow()
      return
    }

    cancelAssistantResponseWindow()
    focusedCandidate.clear()
    listeningState = "listening"
    latestTranscript = ""
    visibleAssistant = undefined
    reportStatus("Listening")
    void enqueueTransition(async () => {
      if (
        !assistantConversation.replyActive ||
        listeningState !== "listening" ||
        sleeping ||
        currentNotification
      ) {
        return
      }
      await showListening()
    })
  }

  function handleResponseDisplayExpired(): void {
    const presentation = visibleAssistant
    void enqueueTransition(async () => {
      if (
        !presentation ||
        !responseLifecycle.active ||
        visibleAssistant !== presentation ||
        sleeping ||
        currentNotification ||
        listeningState !== "idle" ||
        thinking ||
        awaitingResponse
      ) {
        return
      }
      visibleAssistant = undefined
      if (!assistantConversation.voiceReplyAvailable) {
        await showReady()
        return
      }
      await setPage(buildCompactPage("SPEAK TO FOLLOW UP"), "compact")
    })
  }

  function handleResponseConversationExpired(): void {
    candidateAudio.setVoiceReplyArmed(false)
    assistantConversation.closeResponseWindow()
    void enqueueTransition(async () => {
      if (
        responseLifecycle.active ||
        assistantConversation.replyActive ||
        sleeping ||
        currentNotification ||
        listeningState !== "idle" ||
        thinking ||
        awaitingResponse
      ) {
        return
      }
      await showReady()
    })
  }

  function handleMoonshineRunComplete(result: MoonshineRunResult): void {
    if (
      !candidateAudioEnabled ||
      continuousListeningEnabled ||
      !active
    ) {
      return
    }
    void enqueueTransition(async () => {
      if (listeningState !== "stopping") {
        return
      }
      listeningState = "idle"
      if (result.candidateSubmitted) {
        return
      }
      awaitingResponse = false
      clearThinkingAnimation()
      reportStatus("Connected")
      if (!sleeping && !currentNotification) {
        await showReady()
      }
    })
  }

  async function startAudioCapture(): Promise<boolean> {
    return candidateAudio.startCapture(
      () => active && !sleeping && socketIsOpen(),
    )
  }

  async function ensureContinuousCapture(): Promise<boolean> {
    if (
      !continuousListeningEnabled ||
      !active ||
      sleeping ||
      !socketIsOpen() ||
      !candidateAudio.isReady()
    ) {
      return false
    }
    return startAudioCapture()
  }

  async function stopAudioCapture(finalizeCandidate = true) {
    await candidateAudio.stopCapture(finalizeCandidate)
  }

  function clearThinkingAnimation() {
    thinking = false
    if (thinkingTimer !== undefined) {
      clearTimeout(thinkingTimer)
      thinkingTimer = undefined
    }
  }

  function clearReconnectTimer() {
    if (reconnectTimer !== undefined) {
      clearTimeout(reconnectTimer)
      reconnectTimer = undefined
    }
  }

  async function setPage(
    page: RebuildPageContainer,
    nextSurface: DisplaySurface,
  ) {
    if (!active) {
      return
    }
    await renderGlassesPage(page)
    if (active) {
      surface = nextSurface
    }
  }

  async function renderTranscript() {
    if (!active || currentNotification) {
      return
    }
    const frame = thinking ? thinkingFrame : undefined
    const hasBody = latestTranscript.trim().length > 0
    const canUpgrade =
      surface === "transcript" && transcriptLayoutHasBody === hasBody

    if (canUpgrade) {
      const upgraded = await upgradeTranscriptText(
        buildTranscriptContent(latestTranscript, frame),
      )
      if (upgraded || !active) {
        return
      }
    }

    await setPage(
      buildTranscriptPage(latestTranscript, frame),
      "transcript",
    )
    if (active) {
      transcriptLayoutHasBody = hasBody
    }
  }

  function scheduleThinkingFrame() {
    if (
      !active ||
      !thinking ||
      currentNotification ||
      thinkingTimer !== undefined
    ) {
      return
    }
    thinkingTimer = setTimeout(() => {
      thinkingTimer = undefined
      void enqueueTransition(async () => {
        if (!thinking || currentNotification) {
          return
        }
        thinkingFrame = (thinkingFrame + 1) % 3
        await renderTranscript()
        scheduleThinkingFrame()
      })
    }, THINKING_FRAME_DELAY_MS)
  }

  async function startThinkingAnimation() {
    if (currentNotification || thinking) {
      return
    }
    thinking = true
    thinkingFrame = 0
    await renderTranscript()
    scheduleThinkingFrame()
  }

  async function showReady() {
    resetAssistantInteraction()
    clearThinkingAnimation()
    awaitingResponse = false
    latestTranscript = ""
    idlePrompt = undefined
    visibleAssistant = undefined
    const prompt =
      continuousListeningEnabled && candidateAudio.captureRunning
        ? "LISTENING LOCALLY  ·  TAP TO TALK"
        : "TAP TO TALK"
    await setPage(buildCompactPage(prompt), "compact")
  }

  async function showListening() {
    clearThinkingAnimation()
    visibleAssistant = undefined
    if (latestTranscript) {
      await renderTranscript()
      return
    }
    await setPage(
      buildCompactPage(
        assistantConversation.replyActive
          ? "LISTENING"
          : "LISTENING  ·  TAP TO FINISH",
      ),
      "compact",
    )
  }

  async function showConnectionLost() {
    resetAssistantInteraction()
    clearThinkingAnimation()
    await setPage(
      buildCompactPage("OFFLINE  ·  RECONNECTING"),
      "offline",
    )
  }

  async function showPresentation(presentation: GlassesMessage) {
    clearThinkingAnimation()
    await setPage(buildMessagePage(presentation), "message")
  }

  async function showAssistantPresentation(
    presentation: AssistantPresentation,
  ) {
    cancelAssistantResponseWindow()
    clearThinkingAnimation()
    const handsFreeEligible =
      continuousListeningEnabled &&
      candidateAudio.captureRunning &&
      candidateAudio.isReady() &&
      !candidateAudio.hasInFlightCandidates() &&
      socketIsOpen() &&
      !sleeping &&
      !currentNotification
    let voiceReplyAvailable = false
    if (handsFreeEligible) {
      // Establish a clean turn boundary. Any pre-response ambient window is no
      // longer eligible to become the user's explicit follow-up.
      candidateAudio.discardPendingCandidate()
      voiceReplyAvailable = candidateAudio.setVoiceReplyArmed(true)
    }
    assistantConversation.openResponseWindow(voiceReplyAvailable)
    const action = assistantConversation.voiceReplyAvailable
      ? presentation.awaitingConfirmation
        ? 'SAY "SAVE THAT" OR "NO"'
        : "SPEAK TO FOLLOW UP"
      : "TAP TO RESPOND"
    try {
      await setPage(
        buildMessagePage(presentation.message, action),
        "message",
      )
    } catch (error) {
      cancelAssistantResponseWindow()
      throw error
    }
    if (
      !active ||
      visibleAssistant !== presentation ||
      sleeping ||
      currentNotification
    ) {
      cancelAssistantResponseWindow()
      return
    }
    // Do not spend the user's reading time waiting for the SDK render call.
    responseLifecycle.begin(presentation.sourceText)
  }

  async function showIdlePrompt(prompt: string) {
    resetAssistantInteraction()
    clearThinkingAnimation()
    idlePrompt = prompt
    await setPage(buildCompactPage(prompt), "compact")
  }

  async function enterSleep() {
    if (sleeping) {
      return
    }

    resetAssistantInteraction()
    clearThinkingAnimation()
    latestTranscript = ""
    sleeping = true
    visibleAssistant = undefined
    if (listeningState !== "idle") {
      awaitingResponse =
        listeningState === "listening" || listeningState === "stopping"
      if (!candidateAudioEnabled) {
        sendControl("listening_stop")
      }
      listeningState = "idle"
    }
    focusedCandidate.clear()
    candidateAudio.discardPendingCandidate()
    if (candidateAudio.captureState !== "idle") {
      await stopAudioCapture(false)
    }
    reportStatus("Sleeping")
    await setPage(buildSleepPage(), "sleep")
  }

  async function wakeInterface() {
    if (!sleeping) {
      return
    }

    sleeping = false
    if (!socketIsOpen()) {
      reportStatus("Reconnecting")
      forceReconnect()
      await showConnectionLost()
      return
    }

    await ensureContinuousCapture()
    reportStatus("Connected")
    if (currentNotification) {
      await showPresentation(currentNotification.message)
      return
    }
    if (deferredAssistant) {
      visibleAssistant = deferredAssistant
      deferredAssistant = undefined
      await showAssistantPresentation(visibleAssistant)
      return
    }
    await showReady()
  }

  async function restoreAfterNotifications() {
    if (currentNotification) {
      await showPresentation(currentNotification.message)
      return
    }
    if (sleeping) {
      reportStatus("Sleeping")
      await setPage(buildSleepPage(), "sleep")
      return
    }
    if (!socketIsOpen()) {
      reportStatus("Reconnecting")
      await showConnectionLost()
      return
    }
    if (deferredAssistant) {
      visibleAssistant = deferredAssistant
      deferredAssistant = undefined
      reportStatus("Connected")
      await showAssistantPresentation(visibleAssistant)
      return
    }
    if (listeningState === "listening" || listeningState === "starting") {
      reportStatus(listeningState === "listening" ? "Listening" : "Starting microphone")
      await showListening()
      return
    }
    if (awaitingResponse) {
      reportStatus("Thinking")
      await startThinkingAnimation()
      return
    }
    if (idlePrompt) {
      await showIdlePrompt(idlePrompt)
      return
    }
    if (latestTranscript) {
      reportStatus("Connected")
      await renderTranscript()
      return
    }
    reportStatus("Connected")
    await showReady()
  }

  async function presentNotification(message: RealtimeServerMessage) {
    const id = message.id?.trim()
    const text = message.text?.trim()
    if (!id || !text) {
      return
    }

    if (dismissedNotificationIds.has(id)) {
      safeSendJson(socket, { type: "notification_ack", id })
      return
    }
    if (seenNotificationIds.has(id)) {
      return
    }

    cancelAssistantResponseWindow()
    seenNotificationIds.add(id)
    onResponse(text)
    const notification = {
      id,
      message: presentGlassesMessage(text),
    }
    if (currentNotification) {
      notificationQueue.push(notification)
      if (sleeping) {
        sleeping = false
        await ensureContinuousCapture()
        reportStatus("Connected")
        await showPresentation(currentNotification.message)
      }
      return
    }

    currentNotification = notification
    if (visibleAssistant) {
      deferredAssistant = visibleAssistant
      visibleAssistant = undefined
    }
    sleeping = false
    await ensureContinuousCapture()
    reportStatus("Connected")
    await showPresentation(notification.message)
  }

  async function dismissNotification() {
    if (!currentNotification || surface !== "message") {
      return
    }

    const dismissed = currentNotification
    dismissedNotificationIds.add(dismissed.id)
    safeSendJson(socket, {
      type: "notification_ack",
      id: dismissed.id,
    })
    currentNotification = notificationQueue.shift()
    await restoreAfterNotifications()
  }

  async function startLocationUpdates(expectedSocket: WebSocket) {
    if (locationStarted || socket !== expectedSocket || !socketIsOpen()) {
      return
    }
    const started = await bridge.startAppLocationUpdates({
      accuracy: AppLocationAccuracy.Medium,
      intervalMs: 5000,
      distanceFilter: 10,
    }).catch(() => false)
    if (
      !active ||
      socket !== expectedSocket ||
      expectedSocket.readyState !== WebSocket.OPEN
    ) {
      if (started) {
        await bridge.stopAppLocationUpdates().catch(() => undefined)
      }
      return
    }
    locationStarted = started
    if (started) {
      void bridge.getAppLocation({
        accuracy: AppLocationAccuracy.Medium,
        timeoutMs: 5000,
      }).then((location) => {
        if (
          active &&
          socket === expectedSocket &&
          expectedSocket.readyState === WebSocket.OPEN &&
          location
        ) {
          sendLocation(expectedSocket, location)
        }
      }).catch(() => undefined)
    }
  }

  async function stopLocationUpdates() {
    if (!locationStarted) {
      return
    }
    locationStarted = false
    await bridge.stopAppLocationUpdates().catch(() => undefined)
  }

  function unbindSocket(expectedSocket?: WebSocket) {
    if (!socketBinding || (expectedSocket && socketBinding.socket !== expectedSocket)) {
      return
    }
    socketBinding.socket.removeEventListener("message", socketBinding.handleMessage)
    socketBinding.socket.removeEventListener("close", socketBinding.handleClose)
    socketBinding = undefined
  }

  async function handleSocketClosed(closedSocket: WebSocket) {
    if (socket !== closedSocket) {
      return
    }
    unbindSocket(closedSocket)
    socket = undefined
    resetAssistantInteraction()
    clearThinkingAnimation()
    awaitingResponse = false
    latestTranscript = ""
    idlePrompt = undefined
    const wasListening = listeningState !== "idle"
    listeningState = "idle"
    focusedCandidate.clear()
    candidateAudio.resetTransport()
    if (wasListening || candidateAudio.captureState !== "idle") {
      await stopAudioCapture(false)
    }
    await stopLocationUpdates()
    scheduleReconnect()

    if (sleeping) {
      reportStatus("Sleeping")
    } else {
      reportStatus("Reconnecting")
      await showConnectionLost()
    }
  }

  function bindSocket(nextSocket: WebSocket) {
    const handleMessage = (event: MessageEvent<unknown>) => {
      const message = parseRealtimeServerMessage(event.data)
      if (!message) {
        return
      }
      void enqueueTransition(async () => {
        if (socket === nextSocket) {
          await handleServerMessage(message)
        }
      })
    }
    const handleClose = () => {
      void enqueueTransition(async () => {
        await handleSocketClosed(nextSocket)
      })
    }
    socketBinding = { socket: nextSocket, handleMessage, handleClose }
    nextSocket.addEventListener("message", handleMessage)
    nextSocket.addEventListener("close", handleClose)
  }

  async function handleConnected(nextSocket: WebSocket) {
    if (nextSocket.readyState !== WebSocket.OPEN) {
      closeSocketQuietly(nextSocket)
      scheduleReconnect()
      return
    }

    clearReconnectTimer()
    reconnectAttempt = 0
    if (socket && socket !== nextSocket) {
      unbindSocket(socket)
      closeSocketQuietly(socket)
    }
    socket = nextSocket
    bindSocket(nextSocket)
    onConnected()
    await ensureContinuousCapture()

    if (sleeping) {
      reportStatus("Sleeping")
      if (surface !== "sleep") {
        await setPage(buildSleepPage(), "sleep")
      }
    } else {
      reportStatus("Connected")
      if (currentNotification) {
        await showPresentation(currentNotification.message)
      } else if (deferredAssistant) {
        visibleAssistant = deferredAssistant
        deferredAssistant = undefined
        await showAssistantPresentation(visibleAssistant)
      } else if (visibleAssistant) {
        await showAssistantPresentation(visibleAssistant)
      } else {
        await showReady()
      }
    }
    await startLocationUpdates(nextSocket)
  }

  function startConnectionAttempt() {
    if (!active || connecting || socketIsOpen()) {
      return
    }
    connecting = true
    const generation = ++connectGeneration
    const controller = new AbortController()
    connectionAbort = controller

    void (async () => {
      try {
        const nextSocket = await connectRealtimeSocket(accessToken, {
          signal: controller.signal,
          timeoutMs: CONNECTION_TIMEOUT_MS,
        })
        if (!active || generation !== connectGeneration) {
          closeSocketQuietly(nextSocket)
          return
        }
        connecting = false
        connectionAbort = undefined
        await enqueueTransition(async () => {
          if (generation !== connectGeneration) {
            return
          }
          await handleConnected(nextSocket)
        })
        closeUnadoptedSocket(nextSocket, socket)
      } catch {
        if (!active || generation !== connectGeneration) {
          return
        }
        connecting = false
        connectionAbort = undefined
        await enqueueTransition(async () => {
          if (sleeping) {
            reportStatus("Sleeping")
          } else {
            reportStatus("Reconnecting")
            if (surface !== "offline") {
              await showConnectionLost()
            }
          }
        })
        scheduleReconnect()
      }
    })()
  }

  function scheduleReconnect(immediate = false) {
    if (
      !active ||
      connecting ||
      socketIsOpen() ||
      reconnectTimer !== undefined
    ) {
      return
    }
    const delay = immediate ? 0 : reconnectDelay(reconnectAttempt)
    if (!immediate) {
      reconnectAttempt += 1
    }
    reconnectTimer = setTimeout(() => {
      reconnectTimer = undefined
      startConnectionAttempt()
    }, delay)
  }

  function forceReconnect() {
    if (!active || connecting || socketIsOpen()) {
      return
    }
    clearReconnectTimer()
    scheduleReconnect(true)
  }

  async function displayAssistantPresentation(
    presentation: AssistantPresentation,
  ): Promise<void> {
    resetAssistantInteraction()
    awaitingResponse = false
    clearThinkingAnimation()
    visibleAssistant = undefined
    if (listeningState !== "idle") {
      if (
        listeningState === "starting" ||
        listeningState === "listening"
      ) {
        if (!candidateAudioEnabled) {
          sendControl("listening_stop")
        }
      }
      listeningState = "idle"
      if (continuousListeningEnabled) {
        focusedCandidate.clear()
        candidateAudio.discardPendingCandidate()
      } else {
        await stopAudioCapture(!candidateAudioEnabled)
      }
    }

    const wakesSleepingInterface =
      presentation.message.kind === "reminder" ||
      presentation.message.kind === "update"
    if (currentNotification) {
      deferredAssistant = presentation
      return
    }
    if (sleeping && !wakesSleepingInterface) {
      deferredAssistant = presentation
      return
    }
    sleeping = false
    await ensureContinuousCapture()
    visibleAssistant = presentation
    reportStatus("Connected")
    await showAssistantPresentation(presentation)
  }

  async function completeAssistantTurn(messageID: string | undefined) {
    candidateAudio.complete(messageID)
    const focusedCompletion = focusedCandidate.matches(messageID)
    if (isCompetingCandidateMessage(messageID)) {
      return
    }
    const silentAmbientCompletion =
      continuousListeningEnabled &&
      messageID !== undefined &&
      listeningState === "idle" &&
      !focusedCandidate.active &&
      !thinking &&
      !awaitingResponse
    awaitingResponse = false
    clearThinkingAnimation()
    if (focusedCompletion) {
      assistantConversation.finishReply()
      listeningState = "idle"
      focusedCandidate.clear()
    }
    if (sleeping) {
      reportStatus("Sleeping")
      return
    }
    if (silentAmbientCompletion) {
      return
    }
    reportStatus("Connected")
    if (currentNotification) {
      return
    }
    if (latestTranscript) {
      await renderTranscript()
    } else {
      await showReady()
    }
  }

  async function handleServerMessage(message: RealtimeServerMessage) {
    switch (message.type) {
      case "workspace_changed": {
        if (message.resources) {
          onWorkspaceChanged(message.resources)
        }
        return
      }

      case "notification": {
        await presentNotification(message)
        return
      }

      case "user_transcript": {
        if (!message.text) {
          return
        }
        latestTranscript = message.text
        if (sleeping) {
          reportStatus("Sleeping")
          return
        }
        reportStatus(thinking || awaitingResponse ? "Thinking" : "Listening")
        if (!currentNotification) {
          await renderTranscript()
        }
        return
      }

      case "assistant_thinking": {
        cancelAssistantResponseWindow()
        visibleAssistant = undefined
        awaitingResponse = true
        if (sleeping) {
          reportStatus("Sleeping")
          return
        }
        reportStatus("Thinking")
        await startThinkingAnimation()
        return
      }

      case "assistant_done": {
        await completeAssistantTurn(message.id)
        return
      }

      case "assistant_response": {
        const responseText = message.text?.trim()
        if (!responseText) {
          await completeAssistantTurn(message.id)
          return
        }
        candidateAudio.complete(message.id)
        if (isCompetingCandidateMessage(message.id)) {
          onResponse(responseText)
          return
        }
        onResponse(responseText)
        const presentation: AssistantPresentation = {
          message: presentGlassesMessage(responseText),
          awaitingConfirmation: message.awaitingConfirmation === true,
          sourceText: responseText,
        }
        lastAssistant = presentation
        await displayAssistantPresentation(presentation)
        return
      }

      case "assistant_repeat": {
        candidateAudio.complete(message.id)
        if (isCompetingCandidateMessage(message.id)) {
          return
        }
        if (!lastAssistant) {
          assistantConversation.finishReply()
          awaitingResponse = false
          clearThinkingAnimation()
          if (
            continuousListeningEnabled &&
            focusedCandidate.matches(message.id)
          ) {
            listeningState = "idle"
            focusedCandidate.clear()
          }
          if (!sleeping && !currentNotification) {
            await showReady()
          }
          return
        }
        await displayAssistantPresentation(lastAssistant)
        return
      }

      case "listening_stopped": {
        const stoppedUnexpectedly =
          listeningState === "starting" || listeningState === "listening"
        const wasThinking = thinking
        clearThinkingAnimation()
        listeningState = "idle"
        if (stoppedUnexpectedly) {
          await stopAudioCapture()
        }
        if (sleeping) {
          awaitingResponse = false
          reportStatus("Sleeping")
          return
        }
        if (message.error) {
          awaitingResponse = false
          latestTranscript = ""
          reportStatus(message.error)
          idlePrompt = "MIC UNAVAILABLE  ·  TAP TO RETRY"
          if (!currentNotification) {
            await showIdlePrompt(idlePrompt)
          }
        } else {
          awaitingResponse = false
          reportStatus("Connected")
          if (currentNotification) {
            return
          }
          if (wasThinking && latestTranscript) {
            await renderTranscript()
          } else if (wasThinking || stoppedUnexpectedly) {
            await showReady()
          }
        }
        return
      }
    }
  }

  async function handleClick() {
    if (currentNotification && surface === "message") {
      await dismissNotification()
      return
    }

    if (sleeping) {
      await wakeInterface()
      return
    }

    if (listeningState === "listening") {
      listeningState = "stopping"
      awaitingResponse = true
      if (continuousListeningEnabled) {
        const submitted =
          candidateAudio.finalizePendingCandidate() ||
          focusedCandidate.active
        if (!submitted) {
          listeningState = "idle"
          awaitingResponse = false
          focusedCandidate.clear()
          reportStatus("Connected")
          await showReady()
          return
        }
        reportStatus("Thinking")
        await startThinkingAnimation()
        return
      }

      const sent = candidateAudioEnabled || sendControl("listening_stop")
      await stopAudioCapture()
      if (!sent) {
        listeningState = "idle"
        awaitingResponse = false
        reportStatus("Reconnecting")
        closeSocketQuietly(socket)
        forceReconnect()
        await showConnectionLost()
        return
      }
      reportStatus("Thinking")
      await startThinkingAnimation()
      return
    }

    if (thinking) {
      return
    }

    if (
      continuousListeningEnabled &&
      candidateAudio.hasInFlightCandidates()
    ) {
      return
    }

    if (surface === "message" && visibleAssistant) {
      cancelAssistantResponseWindow()
      visibleAssistant = undefined
    }
    if (surface === "transcript") {
      await showReady()
      return
    }
    if (listeningState !== "idle") {
      return
    }

    if (!socketIsOpen()) {
      reportStatus("Reconnecting")
      forceReconnect()
      await showConnectionLost()
      return
    }

    if (candidateAudioEnabled && !candidateAudio.isReady()) {
      reportStatus("Local speech model loading")
      await showIdlePrompt("LOCAL MODEL LOADING  ·  TAP TO RETRY")
      return
    }
    latestTranscript = ""
    idlePrompt = undefined
    resetAssistantInteraction()
    visibleAssistant = undefined
    focusedCandidate.clear()
    listeningState = "starting"
    reportStatus("Starting microphone")
    await setPage(buildCompactPage("STARTING MICROPHONE"), "compact")
    if (!active || listeningState !== "starting") {
      return
    }
    const started = continuousListeningEnabled
      ? await ensureContinuousCapture()
      : await startAudioCapture()
    if (!active || listeningState !== "starting") {
      if (started) {
        await stopAudioCapture(false)
      }
      return
    }
    if (!started) {
      listeningState = "idle"
      reportStatus("Microphone unavailable")
      await showIdlePrompt("MIC UNAVAILABLE  ·  TAP TO RETRY")
      return
    }
    if (
      candidateAudioEnabled &&
      !candidateAudio.armForcedCandidate()
    ) {
      listeningState = "idle"
      await stopAudioCapture(false)
      reportStatus("Local speech model unavailable")
      await showIdlePrompt("LOCAL MODEL UNAVAILABLE")
      return
    }
    if (!candidateAudioEnabled && !sendControl("listening_start")) {
      listeningState = "idle"
      await stopAudioCapture(false)
      reportStatus("Reconnecting")
      closeSocketQuietly(socket)
      forceReconnect()
      await showConnectionLost()
      return
    }
    listeningState = "listening"
    reportStatus("Listening")
    await showListening()
  }

  const stopEvents = bridge.onEvenHubEvent((event) => {
    const eventType =
      event.listEvent?.eventType ??
      event.textEvent?.eventType ??
      event.sysEvent?.eventType
    if (eventType === OsEventTypeList.DOUBLE_CLICK_EVENT) {
      teardown()
      return
    }

    const pcm = event.audioEvent?.audioPcm
    if (pcm) {
      if (candidateAudio.captureRunning) {
        let ownedPcm: Uint8Array<ArrayBuffer> | undefined
        try {
          ownedPcm = Uint8Array.from(pcm)
          candidateAudio.push(ownedPcm)
          if (!candidateAudioEnabled) {
            safeSend(socket, ownedPcm.buffer)
          }
        } catch {
          // Ignore malformed or late audio frames.
        } finally {
          ownedPcm?.fill(0)
        }
      }
      return
    }
    if (isLongPressEvent(event)) {
      suppressClicksUntil = Date.now() + RELEASE_CLICK_SUPPRESSION_MS
      void enqueueTransition(enterSleep)
    } else if (isClickEvent(event) && Date.now() >= suppressClicksUntil) {
      void enqueueTransition(handleClick)
    }
  })

  const stopLocationEvents = bridge.onAppLocationChanged((location) => {
    sendLocation(socket, location)
  })

  scheduleReconnect(true)

  function teardown() {
    if (!active) {
      return
    }
    active = false
    connectGeneration += 1
    connectionAbort?.abort()
    connectionAbort = undefined
    connecting = false
    clearReconnectTimer()
    clearThinkingAnimation()
    resetAssistantInteraction()
    listeningState = "idle"
    focusedCandidate.clear()
    stopEvents()
    stopLocationEvents()
    unbindSocket()
    const closingSocket = socket
    socket = undefined
      closeSocketQuietly(closingSocket)
    candidateAudio.resetTransport()
    candidateAudio.dispose()
    if (locationStarted) {
      locationStarted = false
      void bridge.stopAppLocationUpdates().catch(() => undefined)
    }
  }

  return teardown
}
