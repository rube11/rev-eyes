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
  buildMessageStatus,
  glassesMessagePages,
  fitGlassesText,
  buildSleepPage,
  buildTranscriptContent,
  buildTranscriptPage,
  buildThreadPage,
  presentGlassesMessage,
} from "./glasses-ui"
import type { GlassesMessage } from "./glasses-ui"
import { env } from "../shared/config/env"
import { AudioCaptureController } from "./audio"
import { AssistantResponseLifecycle } from "./assistant-response-lifecycle"
import {
  getEvenBridge,
  renderGlassesPage,
  resumeGlassesPage,
  upgradeCompactText,
  upgradeTranscriptStatus,
  upgradeTranscriptText,
  upgradeMessageStatus,
} from "./glasses-page-host"
import {
  parseRealtimeServerMessage,
  type RealtimeServerMessage,
} from "./realtime-protocol"
import {
  closeSocketQuietly,
  safeSend,
  safeSendJson,
  socketIsOpen as isRealtimeSocketOpen,
} from "./realtime-socket"
import type { WorkspaceResource } from "../features/workspace/workspaceTypes"
import { RealtimeConnection } from "./realtime-connection"
import { loadAgentConnections, openAgentThread, type AgentTarget } from "../features/workspace/agentClient"
import { agentStatusLabel } from "../features/workspace/agentStatus"

type ListeningState = "idle" | "starting" | "listening" | "stopping"
type DisplaySurface = "thread" | "compact" | "message" | "offline" | "sleep" | "transcript" | "agents"
type AgentRow = { target?: AgentTarget; label: string; status: string }
type NotificationPresentation = {
  id: string
  message: GlassesMessage
}
type PendingTranscript = {
  message?: RealtimeServerMessage
  queued: boolean
  responseGeneration: number
}
const TRANSCRIPT_UPDATE_MS = 250
const LISTENING_ANIMATION_MS = 800
const LISTENING_FRAMES = [".", ". .", ". . .", ". ."] as const
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
  onStatus: (status: string) => void,
  onWorkspaceChanged: (resources: readonly WorkspaceResource[]) => void =
    () => undefined,
  onConnected: () => void = () => undefined,
  agents: { target?: AgentTarget; onTargetChange?: (target?: AgentTarget) => void } = {},
): Promise<() => Promise<void>> {
  resumeGlassesPage()
  const agentTarget = agents.target
  const serverListeningEnabled = env.serverListeningEnabled && !agentTarget
  let agentRequests = new AbortController()
  let agentRows: AgentRow[] = []
  let shownAgentRow: AgentRow | undefined
  let pickerOpen = false
  let agentRequestPending = false
  let agentIndex = 0
  let agentMore: { connectionId: string; cursor: number }[] = []
  let agentUncertain = false
  let active = true
  let transitionTail: Promise<void> = Promise.resolve()
  let listeningState: ListeningState = "idle"
  let surface: DisplaySurface = "compact"
  let transcriptLayoutThinking = false
  let lastTranscriptContent = ""
  let messagePageIndex = 0
  let pendingTranscript: PendingTranscript | undefined
  let transcriptTimer: ReturnType<typeof setTimeout> | undefined
  let responseGeneration = 0
  let pendingResponses = 0
  let listeningAnimationTimer: ReturnType<typeof setTimeout> | undefined
  let listeningAnimationFrame = 0
  let locationStarted = false
  let awaitingResponse = false
  let sleeping = false
  let visibleAssistant: GlassesMessage | undefined
  let deferredAssistant: GlassesMessage | undefined
  let lastAssistant: GlassesMessage | undefined
  let currentNotification: NotificationPresentation | undefined
  const notificationQueue: NotificationPresentation[] = []
  const seenNotificationIds = new Set<string>()
  const dismissedNotificationIds = new Set<string>()
  let idlePrompt: string | undefined
  let latestTranscript = ""
  let thinking = false
  let suppressClicksUntil = 0
  let responseWindowGeneration = 0

  const connection = new RealtimeConnection(accessToken, {
    agentTarget,
    enqueue: enqueueTransition,
    connected: handleConnected,
    unavailable: showReconnectState,
  })

  onStatus("Connecting")
  await renderGlassesPage(buildCompactPage("CONNECTING"))
  const bridge = await getEvenBridge()
  const audioCapture = new AudioCaptureController({
    start: () => bridge.audioControl(true, AudioInputSource.Glasses),
    stop: () => bridge.audioControl(false),
  })
  const responseLifecycle = new AssistantResponseLifecycle({
    serverManaged: serverListeningEnabled,
    onConversationExpired: handleResponseConversationExpired,
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
    transitionTail = result.catch((error: unknown) => {
      reportStatus(error instanceof Error ? error.message : "Glasses command failed")
    })
    return transitionTail
  }

  function listeningLabel(): string {
    return `Listening ${LISTENING_FRAMES[listeningAnimationFrame]}`
  }

  function listeningPrompt(): string {
    return agentTarget ? `${listeningLabel()}\nPause or tap to send` : `${listeningLabel().toUpperCase()}  ·  PAUSE TO SEND`
  }

  function stopListeningAnimation(): void {
    if (listeningAnimationTimer !== undefined) {
      clearTimeout(listeningAnimationTimer)
      listeningAnimationTimer = undefined
    }
    listeningAnimationFrame = 0
  }

  function scheduleListeningAnimation(): void {
    if (listeningAnimationTimer !== undefined || listeningState !== "listening") return
    listeningAnimationTimer = setTimeout(() => {
      listeningAnimationTimer = undefined
      if (!active || sleeping || listeningState !== "listening") return
      listeningAnimationFrame = (listeningAnimationFrame + 1) % LISTENING_FRAMES.length
      void enqueueTransition(refreshListeningAnimation).finally(scheduleListeningAnimation)
    }, LISTENING_ANIMATION_MS)
  }

  function setListeningState(next: ListeningState): ListeningState {
    listeningState = next
    if (next === "listening") scheduleListeningAnimation()
    else stopListeningAnimation()
    return next
  }

  function socketIsOpen() {
    return isRealtimeSocketOpen(connection.current)
  }

  function sendControl(type: string) {
    return safeSendJson(connection.current, {
      type,
    })
  }

  function cancelAssistantResponseWindow(): void {
    if (serverListeningEnabled) sendControl("ambient_reply_disarm")
    responseWindowGeneration += 1
    responseLifecycle.cancel()
  }

  function clearAssistantResponseState(): void {
    cancelAssistantResponseWindow()
    thinking = false
  }

  function handleResponseConversationExpired(): void {
    const generation = responseWindowGeneration
    void enqueueTransition(async () => {
      if (
        generation !== responseWindowGeneration ||
        responseLifecycle.active ||
        sleeping ||
        currentNotification ||
        thinking ||
        awaitingResponse
      ) {
        return
      }
      if (listeningState === "listening" || listeningState === "starting") {
        setListeningState("stopping")
        sendControl("listening_stop")
        await stopAudioCapture()
      }
      reportStatus("Connected")
      if (visibleAssistant && surface === "message") {
        await refreshAnswerStatus()
      } else {
        await showReady()
      }
    })
  }

  async function startAudioCapture(followUp = false): Promise<boolean> {
    if (serverListeningEnabled && !sendControl("ambient_start")) return false
    return audioCapture.start(
      () => active && !sleeping && socketIsOpen() &&
        (!followUp || responseLifecycle.active),
    )
  }

  async function stopAudioCapture(force = false) {
    if (serverListeningEnabled) {
      sendControl("ambient_reply_disarm")
      if (!force && active && !sleeping && socketIsOpen()) {
        setListeningState("idle")
        return
      }
      sendControl("ambient_stop")
    }
    await audioCapture.stop()
  }

  async function startAmbientCapture(): Promise<boolean> {
    if (!serverListeningEnabled || sleeping) return true
    if (!active || !socketIsOpen()) return false
    if (!await startAudioCapture()) {
      sendControl("ambient_stop")
      reportStatus("Microphone unavailable")
      await showIdlePrompt("MIC UNAVAILABLE  ·  TAP TO RETRY")
      return false
    }
    return true
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
    const content = buildTranscriptContent(latestTranscript)
    const canUpgrade =
      surface === "transcript" && transcriptLayoutThinking === thinking

    if (canUpgrade) {
      if (lastTranscriptContent === content) return
      const upgraded = await upgradeTranscriptText(content)
      if (upgraded || !active) {
        lastTranscriptContent = content
        return
      }
    }

    await setPage(
      buildTranscriptPage(latestTranscript, thinking && !agentTarget,
        agentTarget ? (thinking ? "Sending" : "Listening · Tap to send") : listeningLabel()),
      "transcript",
    )
    if (active) {
      transcriptLayoutThinking = thinking
      lastTranscriptContent = content
    }
  }

  async function showThinking() {
    if (currentNotification || thinking) {
      return
    }
    thinking = true
    if (visibleAssistant) await refreshAnswerStatus()
    else await renderTranscript()
  }

  async function showReady() {
    clearAssistantResponseState()
    awaitingResponse = false
    latestTranscript = ""
    idlePrompt = undefined
    visibleAssistant = undefined
    if (agentTarget) {
      await setPage(buildThreadPage(agentTarget.title,
        agentUncertain ? "Send not confirmed.\nScroll to check the thread." : "Tap to speak\nScroll to read reply",
        "Hold: Eyes"), "thread")
    } else {
      await setPage(buildCompactPage(serverListeningEnabled ? "LISTENING · TAP TO TALK · SCROLL: THREADS" : "TAP TO TALK · SCROLL: THREADS"), "compact")
    }
  }

  async function showListening() {
    thinking = false
    if (visibleAssistant) {
      await refreshAnswerStatus()
      return
    }
    if (latestTranscript) {
      await renderTranscript()
      return
    }
    await setPage(
      buildCompactPage(listeningPrompt()),
      "compact",
    )
  }

  async function showConnectionLost() {
    clearAssistantResponseState()
    await setPage(
      buildCompactPage(agentTarget ? "Reconnecting\nHold: Eyes" : "OFFLINE  ·  RECONNECTING"),
      "offline",
    )
  }

  async function showPresentation(presentation: GlassesMessage) {
    thinking = false
    messagePageIndex = 0
    await setPage(buildMessagePage(presentation), "message")
  }

  function answerAction(): string {
    if (thinking || awaitingResponse) return agentTarget ? "Sending" : "Thinking"
    if (agentTarget) return "Scroll · Hold: Eyes"
    return listeningState === "listening"
      ? listeningLabel() : "Tap to talk"
  }

  async function refreshListeningAnimation(): Promise<void> {
    if (!active || sleeping || currentNotification || pendingResponses > 0 || listeningState !== "listening") return
    if (surface === "compact") {
      await upgradeCompactText(listeningPrompt())
    } else if (surface === "transcript" && !thinking) {
      await upgradeTranscriptStatus(agentTarget ? "Listening · Tap to send" : listeningLabel())
    } else if (surface === "message" && visibleAssistant) {
      await refreshAnswerStatus()
    }
  }

  async function refreshAnswerStatus() {
    if (!visibleAssistant || currentNotification) return
    const message = visibleAssistant
    const action = answerAction()
    const upgraded = surface === "message" &&
      await upgradeMessageStatus(buildMessageStatus(message, action, messagePageIndex))
    if (!upgraded) await setPage(buildMessagePage(message, action, messagePageIndex), "message")
  }

  async function turnMessagePage(direction: number) {
    const message = currentNotification?.message ?? visibleAssistant
    if (sleeping || surface !== "message" || !message) return
    const index = Math.max(0, Math.min(glassesMessagePages(message).length - 1, messagePageIndex + direction))
    if (index === messagePageIndex) return
    messagePageIndex = index
    await setPage(buildMessagePage(message, currentNotification ? "Tap to dismiss" : answerAction(), index), "message")
  }

  async function showAssistantPresentation(
    presentation: GlassesMessage,
  ) {
    clearAssistantResponseState()
    if (agentTarget) {
      await setPage(buildMessagePage(presentation, answerAction(), messagePageIndex), "message")
      return
    }
    try {
      await setPage(
        buildMessagePage(presentation, "Opening mic", messagePageIndex),
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
    responseLifecycle.begin()
    // The server ignores starts until the previous transcription has finished.
    // If it is still stopping, listening_stopped will open the follow-up stream.
    if (listeningState === "idle") {
      await startListening(true)
    }
  }

  async function showIdlePrompt(prompt: string) {
    clearAssistantResponseState()
    idlePrompt = prompt
    await setPage(buildCompactPage(prompt), "compact")
  }

  async function enterSleep() {
    if (sleeping) {
      return
    }

    cancelAssistantResponseWindow()
    takePendingTranscript()
    thinking = false
    latestTranscript = ""
    sleeping = true
    visibleAssistant = undefined
    if (listeningState !== "idle") {
      awaitingResponse =
        listeningState === "listening" || listeningState === "stopping"
      sendControl("listening_stop")
      setListeningState("idle")
    }
    if (audioCapture.state !== "idle") {
      await stopAudioCapture()
    }
    reportStatus("Sleeping")
    await setPage(buildSleepPage(), "sleep")
  }

  async function wakeInterface() {
    if (!sleeping) {
      return
    }

    sleeping = false
    if (!await startAmbientCapture() && socketIsOpen()) return
    if (!socketIsOpen()) {
      reportStatus("Reconnecting")
      connection.reconnect()
      await showConnectionLost()
      return
    }

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
      await showThinking()
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
      safeSendJson(connection.current, { type: "notification_ack", id })
      return
    }
    if (seenNotificationIds.has(id)) {
      return
    }

    const wasFollowUp = responseLifecycle.active
    cancelAssistantResponseWindow()
    if (wasFollowUp && (listeningState === "listening" || listeningState === "starting")) {
      setListeningState("stopping")
      sendControl("listening_stop")
      await stopAudioCapture()
    }
    seenNotificationIds.add(id)
    const notification = {
      id,
      message: presentGlassesMessage(text),
    }
    if (currentNotification) {
      notificationQueue.push(notification)
      if (sleeping) {
        sleeping = false
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
    reportStatus("Connected")
    await showPresentation(notification.message)
  }

  async function dismissNotification() {
    if (!currentNotification || surface !== "message") {
      return
    }

    const dismissed = currentNotification
    dismissedNotificationIds.add(dismissed.id)
    safeSendJson(connection.current, {
      type: "notification_ack",
      id: dismissed.id,
    })
    currentNotification = notificationQueue.shift()
    await restoreAfterNotifications()
  }

  async function startLocationUpdates(expectedSocket: WebSocket) {
    if (locationStarted || connection.current !== expectedSocket || !socketIsOpen()) {
      return
    }
    const started = await bridge.startAppLocationUpdates({
      accuracy: AppLocationAccuracy.Medium,
      intervalMs: 5000,
      distanceFilter: 10,
    }).catch(() => false)
    if (
      !active ||
      connection.current !== expectedSocket ||
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
          connection.current === expectedSocket &&
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

  async function handleSocketClosed(closedSocket: WebSocket) {
    if (connection.current !== closedSocket) {
      return
    }
    connection.release(closedSocket)
    if (agentTarget && (listeningState !== "idle" || thinking || awaitingResponse)) {
      agentUncertain = true
      // A previous reply must not hide the unconfirmed send after reconnect.
      visibleAssistant = undefined
    }
    clearAssistantResponseState()
    awaitingResponse = false
    latestTranscript = ""
    idlePrompt = undefined
    const wasListening = listeningState !== "idle"
    setListeningState("idle")
    if (wasListening || audioCapture.state !== "idle") {
      await stopAudioCapture()
    }
    await stopLocationUpdates()
    connection.schedule()

    if (sleeping) {
      reportStatus("Sleeping")
    } else {
      reportStatus("Reconnecting")
      if (!pickerOpen) await showConnectionLost()
    }
  }

  function bindSocket(nextSocket: WebSocket) {
    const handleMessage = (event: MessageEvent<unknown>) => {
      const message = parseRealtimeServerMessage(event.data)
      if (!message) {
        return
      }
      if (!active || connection.current !== nextSocket) return
      // Phone data must not wait for the glasses to acknowledge a page update.
      if (message.type === "workspace_changed") {
        if (message.resources) onWorkspaceChanged(message.resources)
        return
      }
      if (message.type === "user_transcript" && message.text) {
        // Receipt, not delayed rendering, owns the follow-up deadline.
        if (listeningState === "listening") cancelAssistantResponseWindow()
        if (pendingTranscript) pendingTranscript.message = message
        else pendingTranscript = { message, queued: false, responseGeneration }
        if (transcriptTimer === undefined && !pendingTranscript.queued) {
          transcriptTimer = setTimeout(() => {
            queuePendingTranscript(nextSocket)
          }, TRANSCRIPT_UPDATE_MS)
        }
        return
      }
      const replacesTranscript =
        (message.type === "assistant_response" && Boolean(message.text?.trim())) ||
        message.type === "assistant_repeat"
      if (replacesTranscript) {
        responseGeneration += 1
        pendingResponses += 1
        takePendingTranscript()
      } else {
        // Seal the batch at a control boundary so later speech cannot jump
        // ahead of conversation_started/idle or another server state change.
        queuePendingTranscript(nextSocket, true)
      }
      const generation = responseGeneration
      void enqueueTransition(async () => {
        try {
          if (connection.current !== nextSocket) return
          if (generation !== responseGeneration &&
            (message.type === "assistant_thinking" || message.type === "ambient_candidate")) return
          await handleServerMessage(message)
        } finally {
          if (replacesTranscript) pendingResponses -= 1
        }
      })
    }
    const handleClose = () => {
      void enqueueTransition(async () => {
        await handleSocketClosed(nextSocket)
      })
    }
    connection.bind(nextSocket, handleMessage, handleClose)
  }

  function queuePendingTranscript(socket: WebSocket, seal = false) {
    if (transcriptTimer !== undefined) clearTimeout(transcriptTimer)
    transcriptTimer = undefined
    const pending = pendingTranscript
    if (seal) pendingTranscript = undefined
    if (!pending || pending.queued) return
    pending.queued = true
    void enqueueTransition(async () => {
      if (pendingTranscript === pending) pendingTranscript = undefined
      const message = pending.message
      pending.message = undefined
      // Select the newest interim text when rendering starts, not when its
      // throttle expires. Slow native writes must not build a stale backlog.
      if (connection.current === socket && pending.responseGeneration === responseGeneration && message) {
        await handleServerMessage(message)
      }
    })
  }

  function takePendingTranscript() {
    if (transcriptTimer !== undefined) clearTimeout(transcriptTimer)
    transcriptTimer = undefined
    const pending = pendingTranscript?.message
    if (pendingTranscript) pendingTranscript.message = undefined
    pendingTranscript = undefined
    return pending
  }

  async function handleConnected(nextSocket: WebSocket) {
    if (nextSocket.readyState !== WebSocket.OPEN) {
      closeSocketQuietly(nextSocket)
      connection.schedule()
      return
    }

    connection.adopt(nextSocket)
    bindSocket(nextSocket)
    onConnected()
    if (pickerOpen) {
      reportStatus("Connected")
      await startLocationUpdates(nextSocket)
      return
    }
    if (!await startAmbientCapture()) {
      await startLocationUpdates(nextSocket)
      return
    }

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

  async function showReconnectState() {
    if (pickerOpen) { reportStatus("Reconnecting"); return }
    if (sleeping) {
      reportStatus("Sleeping")
    } else {
      reportStatus("Reconnecting")
      if (surface !== "offline") await showConnectionLost()
    }
  }

  async function displayAssistantPresentation(
    presentation: GlassesMessage,
  ): Promise<void> {
    clearAssistantResponseState()
    messagePageIndex = 0
    awaitingResponse = false
    visibleAssistant = undefined
    if (listeningState !== "idle") {
      if (
        listeningState === "starting" ||
        listeningState === "listening"
      ) {
        setListeningState("stopping")
        sendControl("listening_stop")
      }
      await stopAudioCapture()
    }

    const wakesSleepingInterface =
      presentation.kind === "reminder" ||
      presentation.kind === "update"
    if (currentNotification) {
      deferredAssistant = presentation
      return
    }
    if (sleeping && !wakesSleepingInterface) {
      deferredAssistant = presentation
      return
    }
    sleeping = false
    visibleAssistant = presentation
    reportStatus("Connected")
    await showAssistantPresentation(presentation)
  }

  async function completeAssistantTurn() {
    if (agentTarget && !visibleAssistant && (thinking || awaitingResponse)) {
      agentUncertain = Boolean(latestTranscript) || agentUncertain
      await displayAssistantPresentation(presentGlassesMessage(latestTranscript
        ? "Send not confirmed.\nScroll to check the thread."
        : "No speech heard.\nTap to try again."))
      return
    }
    if (serverListeningEnabled) setListeningState("idle")
    awaitingResponse = false
    thinking = false
    if (sleeping) {
      reportStatus("Sleeping")
      return
    }
    reportStatus("Connected")
    if (currentNotification) {
      return
    }
    if (visibleAssistant) {
      await refreshAnswerStatus()
    } else if (latestTranscript) {
      await renderTranscript()
    } else {
      await showReady()
    }
  }

  async function handleServerMessage(message: RealtimeServerMessage) {
    // Browsing never resumes speech or replaces the row being selected.
    if (pickerOpen) return
    switch (message.type) {
      case "conversation_started": {
        if (!serverListeningEnabled || sleeping) return
        cancelAssistantResponseWindow()
        latestTranscript = ""
        setListeningState("listening")
        awaitingResponse = false
        reportStatus("Listening")
        if (!currentNotification) await showListening()
        return
      }
      case "conversation_idle": {
        if (!serverListeningEnabled) return
        clearAssistantResponseState()
        setListeningState("idle")
        awaitingResponse = false
        reportStatus(sleeping ? "Sleeping" : "Connected")
        if (!sleeping && !currentNotification) {
          if (visibleAssistant) await refreshAnswerStatus()
          else await showReady()
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
        // Speech within the window owns the turn; do not cut it off at 30s.
        if (listeningState === "listening") {
          cancelAssistantResponseWindow()
        }
        if (sleeping) {
          reportStatus("Sleeping")
          return
        }
        reportStatus(thinking || awaitingResponse ? (agentTarget ? "Sending" : "Thinking") : "Listening")
        // A transcript is evidence of a possible follow-up, not a display
        // transition. Keep the previous answer readable until its replacement
        // is ready, including turns that ultimately produce no response.
        if (!currentNotification && !(visibleAssistant && surface === "message")) {
          await renderTranscript()
        }
        return
      }

      case "ambient_candidate": {
        if (!serverListeningEnabled) return
        cancelAssistantResponseWindow()
        setListeningState("stopping")
        awaitingResponse = true
        if (!sleeping) { reportStatus("Thinking"); await showThinking() }
        return
      }

      case "assistant_thinking": {
        cancelAssistantResponseWindow()
        awaitingResponse = true
        if (
          listeningState === "starting" ||
          listeningState === "listening"
        ) {
          setListeningState("stopping")
          sendControl("listening_stop")
          await stopAudioCapture()
        }
        if (sleeping) {
          reportStatus("Sleeping")
          return
        }
        reportStatus(agentTarget ? "Sending" : "Thinking")
        await showThinking()
        return
      }

      case "assistant_done": {
        await completeAssistantTurn()
        return
      }

      case "assistant_response": {
        const responseText = message.text?.trim()
        if (!responseText) {
          await completeAssistantTurn()
          return
        }
        let text = responseText
        if (agentTarget) {
          if (responseText.startsWith("Message queued in your T3 thread.")) {
            agentUncertain = false
            text = "Sent to thread.\nScroll to read reply."
          } else if (responseText.startsWith("Delivery is uncertain.")) {
            agentUncertain = true
            text = "Send not confirmed.\nScroll to check before retrying."
          } else if (responseText.startsWith("Message was not sent.")) {
            text = "Not sent.\nCheck T3 on your phone."
          } else if (responseText.startsWith("Speech was not sent.")) {
            text = "Not sent.\nTap and speak your message."
          }
        }
        const presentation = presentGlassesMessage(text)
        lastAssistant = presentation
        await displayAssistantPresentation(presentation)
        return
      }

      case "assistant_repeat": {
        if (!lastAssistant) {
          awaitingResponse = false
          thinking = false
          if (!sleeping && !currentNotification) {
            await showReady()
          }
          return
        }
        await displayAssistantPresentation(lastAssistant)
        return
      }

      case "listening_stopped": {
        if (serverListeningEnabled && message.error) await stopAudioCapture(true)
        const stoppedUnexpectedly =
          listeningState === "starting" || listeningState === "listening"
        const wasThinking = thinking
        const agentSendPending = agentTarget && awaitingResponse
        thinking = false
        setListeningState("idle")
        if (stoppedUnexpectedly) {
          await stopAudioCapture()
        }
        if (sleeping) {
          awaitingResponse = false
          reportStatus("Sleeping")
          return
        }
        if (message.error) {
          cancelAssistantResponseWindow()
          awaitingResponse = false
          latestTranscript = ""
          reportStatus(message.error)
          if (agentTarget) {
            agentUncertain = true
            visibleAssistant = undefined
            if (!currentNotification) await setPage(buildThreadPage(agentTarget.title,
              "Speech interrupted.\nScroll to check the thread.", "Hold: Eyes"), "thread")
          } else {
            idlePrompt = "MIC UNAVAILABLE  ·  TAP TO RETRY"
            if (!currentNotification) await showIdlePrompt(idlePrompt)
          }
        } else {
          if (agentSendPending) {
            thinking = true
            reportStatus("Sending")
            if (!currentNotification) await renderTranscript()
            return
          }
          awaitingResponse = false
          if (responseLifecycle.active && !currentNotification) {
            await startListening(true)
            return
          }
          reportStatus("Connected")
          if (currentNotification) {
            return
          }
          if (visibleAssistant && surface === "message") {
            await refreshAnswerStatus()
          } else if (wasThinking && latestTranscript) {
            await renderTranscript()
          } else if (wasThinking || stoppedUnexpectedly) {
            await showReady()
          }
        }
        return
      }
    }
  }

  async function selectAgentTarget(target?: AgentTarget) {
    if (!agents.onTargetChange) return
    // Stop this runtime before React opens the new bound connection. Queued taps
    // and messages from the previous target cannot start another microphone.
    void teardown()
    agents.onTargetChange(target)
  }

  async function closeAgentPicker() {
    agentRequests.abort()
    agentRequests = new AbortController()
    agentRequestPending = false
    pickerOpen = false
    shownAgentRow = undefined
    if (!socketIsOpen()) { await showConnectionLost(); return }
    await showReady()
    await startAmbientCapture()
  }

  async function handleClick(clickedRow?: AgentRow) {
    if (pickerOpen) {
      if (!clickedRow || clickedRow !== shownAgentRow) return
      const target = clickedRow.target
      if (target?.connectionId === agentTarget?.connectionId && target?.threadId === agentTarget?.threadId) {
        await closeAgentPicker()
      } else await selectAgentTarget(target)
      return
    }
    if (currentNotification && surface === "message") {
      await dismissNotification()
      return
    }

    if (sleeping) {
      await wakeInterface()
      return
    }

    if (listeningState === "listening") {
      if (serverListeningEnabled && visibleAssistant && surface === "message" && !latestTranscript) {
        sendControl("conversation_stop")
        cancelAssistantResponseWindow()
        setListeningState("idle")
        awaitingResponse = false
        visibleAssistant = undefined
        await showReady()
        return
      }
      // Finish a server reply before disarming its capture window. Recognition
      // may still be catching up with the final microphone frames.
      const serverFinished = serverListeningEnabled ? sendControl("conversation_finalize") : undefined
      cancelAssistantResponseWindow()
      setListeningState("stopping")
      awaitingResponse = true
      const sent = serverFinished ?? sendControl("listening_stop")
      await stopAudioCapture()
      if (!sent) {
        if (agentTarget) agentUncertain = true
        setListeningState("idle")
        awaitingResponse = false
        reportStatus("Reconnecting")
        closeSocketQuietly(connection.current)
        connection.reconnect()
        await showConnectionLost()
        return
      }
      reportStatus(agentTarget ? "Sending" : "Thinking")
      // A tap on a still-visible reply dismisses it and closes follow-up.
      if (visibleAssistant && surface === "message" && !latestTranscript) {
        await showReady()
        return
      }
      await showThinking()
      return
    }

    if (thinking || (agentTarget && awaitingResponse)) {
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
      connection.reconnect()
      await showConnectionLost()
      return
    }

    await startListening()
  }

  async function startListening(followUp = false) {
    if (!active || sleeping || currentNotification || !socketIsOpen() ||
        listeningState !== "idle" || (followUp && !responseLifecycle.active)) {
      return
    }
    latestTranscript = ""
    idlePrompt = undefined
    if (agentTarget) {
      agentRequests.abort()
      agentRequests = new AbortController()
      agentRequestPending = false
    }
    if (!followUp) {
      cancelAssistantResponseWindow()
      visibleAssistant = undefined
    }
    listeningState = setListeningState("starting")
    reportStatus("Starting microphone")
    if (!followUp) {
      await setPage(buildCompactPage("STARTING MICROPHONE"), "compact")
    }
    if (!active || listeningState !== "starting") {
      return
    }
    const started = await startAudioCapture(followUp)
    if (!active || listeningState !== "starting") {
      if (started) {
        await stopAudioCapture()
      }
      return
    }
    if (!started) {
      if (serverListeningEnabled) sendControl("ambient_stop")
      setListeningState("idle")
      if (followUp && !responseLifecycle.active) {
        reportStatus("Connected")
        if (visibleAssistant) await refreshAnswerStatus()
        else await showReady()
        return
      }
      reportStatus("Microphone unavailable")
      await showIdlePrompt("MIC UNAVAILABLE  ·  TAP TO RETRY")
      return
    }
    const startCommand = serverListeningEnabled && followUp ? "ambient_reply_arm" : "listening_start"
    if (!sendControl(startCommand)) {
      setListeningState("idle")
      await stopAudioCapture()
      reportStatus("Reconnecting")
      closeSocketQuietly(connection.current)
      connection.reconnect()
      await showConnectionLost()
      return
    }
    setListeningState("listening")
    reportStatus("Listening")
    if (followUp && visibleAssistant) {
      await refreshAnswerStatus()
    } else {
      await showListening()
    }
  }

  function requestAgentData<T>(
    load: (signal: AbortSignal) => Promise<T>,
    success: (value: T) => Promise<void>,
    failure: () => Promise<void>,
  ) {
    agentRequests.abort()
    const controller = new AbortController()
    agentRequests = controller
    agentRequestPending = true
    // Keep network waits outside the gesture queue so Hold can always leave.
    void (async () => {
      let timeout: ReturnType<typeof setTimeout> | undefined
      let cancel: (() => void) | undefined
      try {
        const result = await Promise.race([
          load(controller.signal),
          new Promise<never>((_, reject) => {
            cancel = () => reject(new Error("Thread request cancelled"))
            controller.signal.addEventListener("abort", cancel, { once: true })
          }),
          new Promise<never>((_, reject) => {
            timeout = setTimeout(() => {
              controller.abort()
              reject(new Error("Thread request timed out"))
            }, 8000)
          }),
        ])
        if (timeout !== undefined) clearTimeout(timeout)
        await enqueueTransition(async () => {
          if (agentRequests !== controller || controller.signal.aborted) return
          agentRequestPending = false
          try { await success(result) }
          catch { await failure() }
        })
      } catch {
        await enqueueTransition(async () => {
          if (agentRequests === controller) {
            agentRequestPending = false
            await failure()
          }
        })
      } finally {
        if (timeout !== undefined) clearTimeout(timeout)
        if (cancel) controller.signal.removeEventListener("abort", cancel)
      }
    })()
  }

  async function readAgentReply() {
    if (!agentTarget || thinking || awaitingResponse || listeningState !== "idle") return
    reportStatus("Reading reply")
    await setPage(buildThreadPage(agentTarget.title, "Reading reply…", "Hold: Eyes"), "thread")
    requestAgentData(
      // Reread the recent window: an in-progress reply can change in place.
      signal => openAgentThread(accessToken, agentTarget, signal),
      async detail => {
        const body = detail.messages.map(message => `${message.role === "user" ? "You" : "T3"}: ${message.text}`).join("\n\n")
        const presentation = presentGlassesMessage(body
          ? `${body}${detail.truncated ? "\n\nFull text is in T3." : ""}`
          : "No recent messages.\nOpen T3 for earlier replies.")
        visibleAssistant = presentation
        messagePageIndex = 0
        await showAssistantPresentation(presentation)
        reportStatus("Connected")
      },
      async () => {
        await setPage(buildThreadPage(agentTarget.title, "Couldn’t read reply.\nScroll to retry.", "Hold: Eyes"), "thread")
        reportStatus("Couldn’t read reply")
      },
    )
  }

  async function showAgentRow() {
    const row = agentRows[agentIndex]
    shownAgentRow = undefined
    await setPage(buildThreadPage(row.label, `${fitGlassesText(row.status, 1)}\nScroll: threads · Hold: back`,
      `${agentIndex + 1}/${agentRows.length} · Tap: select`), "agents")
    shownAgentRow = row
  }

  async function browseAgents(direction: number) {
    if (surface === "message" && (currentNotification || visibleAssistant)) {
      if (agentTarget && !currentNotification && direction > 0 && visibleAssistant && messagePageIndex >= glassesMessagePages(visibleAssistant).length - 1) await readAgentReply()
      else await turnMessagePage(direction)
      return
    }
    if (sleeping || currentNotification || thinking || awaitingResponse || listeningState !== "idle") return
    if (agentTarget && !pickerOpen) { await readAgentReply(); return }
    if (pickerOpen && agentRequestPending) return
    if (!pickerOpen) {
      cancelAssistantResponseWindow()
      takePendingTranscript()
      await stopAudioCapture(true)
      pickerOpen = true
      shownAgentRow = undefined
      agentRows = []
      await setPage(buildThreadPage("Threads", "Loading…", "Hold: back"), "agents")
      requestAgentData(
        signal => loadAgentConnections(accessToken, signal),
        async connections => {
          agentRows = [{ label: "Eyes", status: "Your assistant" }, ...connections.filter(entry => !entry.unavailable && entry.canSend).flatMap(entry => entry.threads.map(thread => ({
            target: { connectionId: entry.id, threadId: thread.id, title: thread.title },
            label: thread.title,
            status: `T3 · ${agentStatusLabel(thread.status)}`,
          })))]
          agentMore = connections.filter(entry => !entry.unavailable && entry.hasMore && entry.canSend && entry.cursor !== undefined).map(entry => ({ connectionId: entry.id, cursor: entry.cursor! }))
          agentIndex = Math.min(1, agentRows.length - 1)
          await showAgentRow()
        },
        async () => {
          shownAgentRow = undefined
          await setPage(buildThreadPage("Threads unavailable", "Scroll to retry", "Hold: back"), "agents")
          reportStatus("Couldn’t load threads")
        },
      )
      return
    }
    if (!agentRows.length) {
      // A failed load can be retried without turning a tap into speech.
      agentRequests.abort()
      pickerOpen = false
      await browseAgents(direction)
      return
    }
    if (!shownAgentRow) return
    if (direction > 0 && agentIndex === agentRows.length - 1 && agentMore.length) {
      const page = agentMore[0]
      shownAgentRow = undefined
      await setPage(buildThreadPage("Threads", "Loading more…", "Hold: back"), "agents")
      requestAgentData(
        signal => loadAgentConnections(accessToken, signal, page),
        async connections => {
          const entry = connections.find(item => item.id === page.connectionId)
          if (!entry || entry.unavailable || entry.cursor === page.cursor) throw new Error("Invalid page")
          const existing = new Set(agentRows.map(row => row.target && JSON.stringify([row.target.connectionId, row.target.threadId])))
          const nextRows = entry.threads.filter(thread => !existing.has(JSON.stringify([entry.id, thread.id]))).map(thread => ({
            target: { connectionId: entry.id, threadId: thread.id, title: thread.title }, label: thread.title, status: `T3 · ${agentStatusLabel(thread.status)}`,
          }))
          agentRows.push(...nextRows)
          if (entry.hasMore && entry.cursor !== undefined) agentMore[0] = { connectionId: entry.id, cursor: entry.cursor }
          else agentMore.shift()
          agentIndex = Math.min(agentRows.length - 1, agentIndex + 1)
          await showAgentRow()
        },
        async () => {
          const row = agentRows[agentIndex]
          await setPage(buildThreadPage(row.label, "Couldn’t load more.\nScroll to retry.", "Tap: select · Hold: back"), "agents")
          shownAgentRow = row
        },
      )
      return
    }
    agentIndex = Math.max(0, Math.min(agentRows.length - 1, agentIndex + direction))
    await showAgentRow()
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
      if (audioCapture.running) {
        let ownedPcm: Uint8Array<ArrayBuffer> | undefined
        try {
          // Speaker identification is disabled. Keep an owned copy of raw
          // PCM; never mutate SDK memory or forward its fallible role labels.
          ownedPcm = new Uint8Array(pcm)
          safeSend(connection.current, ownedPcm.buffer)
        } catch {
          // Ignore malformed or late audio frames.
        } finally {
          ownedPcm?.fill(0)
        }
      }
      return
    }
    if (eventType === OsEventTypeList.SCROLL_TOP_EVENT) {
      void enqueueTransition(() => browseAgents(-1))
    } else if (eventType === OsEventTypeList.SCROLL_BOTTOM_EVENT) {
      void enqueueTransition(() => browseAgents(1))
    } else if (isLongPressEvent(event)) {
      suppressClicksUntil = Date.now() + RELEASE_CLICK_SUPPRESSION_MS
      void enqueueTransition(async () => {
        if (pickerOpen) { await closeAgentPicker(); return }
        if (agentTarget && agents.onTargetChange) { await selectAgentTarget(); return }
        await enterSleep()
      })
    } else if (isClickEvent(event) && Date.now() >= suppressClicksUntil) {
      const clickedInPicker = pickerOpen
      const clickedRow = shownAgentRow
      const pending = takePendingTranscript()
      void enqueueTransition(async () => {
        if (clickedInPicker !== pickerOpen) return
        if (pending) await handleServerMessage(pending)
        await handleClick(clickedRow)
      })
    }
  })

  const stopLocationEvents = bridge.onAppLocationChanged((location) => {
    sendLocation(connection.current, location)
  })

  connection.start()

  let teardownPromise: Promise<void> | undefined
  function teardown(): Promise<void> {
    if (!active) {
      return teardownPromise ?? Promise.resolve()
    }
    active = false
    agentRequests.abort()
    takePendingTranscript()
    clearAssistantResponseState()
    setListeningState("idle")
    stopEvents()
    stopLocationEvents()
    connection.dispose()
    const stoppingAudio = audioCapture.dispose()
    let stoppingLocation: Promise<unknown> | undefined
    if (locationStarted) {
      locationStarted = false
      stoppingLocation = bridge.stopAppLocationUpdates()
    }
    teardownPromise = Promise.allSettled([transitionTail, stoppingAudio, stoppingLocation])
      .then(() => undefined)
    return teardownPromise
  }

  return teardown
}
