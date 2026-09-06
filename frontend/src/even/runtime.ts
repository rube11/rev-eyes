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
import { AudioCaptureController } from "./audio"
import { AssistantResponseLifecycle } from "./assistant-response-lifecycle"
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

type ListeningState = "idle" | "starting" | "listening" | "stopping"
type DisplaySurface = "compact" | "message" | "offline" | "sleep" | "transcript"
type NotificationPresentation = {
  id: string
  message: GlassesMessage
}
type AssistantPresentation = {
  message: GlassesMessage
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
  onStatus: (status: string) => void,
  onWorkspaceChanged: (resources: readonly WorkspaceResource[]) => void =
    () => undefined,
  onConnected: () => void = () => undefined,
): Promise<() => Promise<void>> {
  resumeGlassesPage()
  let active = true
  let transitionTail: Promise<void> = Promise.resolve()
  let socket: WebSocket | undefined
  let socketBinding: SocketBinding | undefined
  let listeningState: ListeningState = "idle"
  let surface: DisplaySurface = "compact"
  let transcriptLayoutHasBody = false
  let locationStarted = false
  let awaitingResponse = false
  let sleeping = false
  let visibleAssistant: AssistantPresentation | undefined
  let deferredAssistant: AssistantPresentation | undefined
  let lastAssistant: AssistantPresentation | undefined
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
  let responseWindowGeneration = 0

  onStatus("Connecting")
  await renderGlassesPage(buildCompactPage("CONNECTING"))
  const bridge = await getEvenBridge()
  const audioCapture = new AudioCaptureController({
    start: () => bridge.audioControl(true, AudioInputSource.Glasses),
    stop: () => bridge.audioControl(false),
  })
  const responseLifecycle = new AssistantResponseLifecycle({
    onConversationExpired: handleResponseConversationExpired,
    onDisplayExpired: handleResponseDisplayExpired,
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
    responseWindowGeneration += 1
    responseLifecycle.cancel()
  }

  function resetAssistantInteraction(): void {
    cancelAssistantResponseWindow()
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
        listeningState !== "listening" ||
        thinking ||
        awaitingResponse
      ) {
        return
      }
      visibleAssistant = undefined
      await showListening()
    })
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
        listeningState = "stopping"
        sendControl("listening_stop")
        await stopAudioCapture()
      }
      reportStatus("Connected")
      await showReady()
    })
  }

  async function startAudioCapture(followUp = false): Promise<boolean> {
    return audioCapture.start(
      () => active && !sleeping && socketIsOpen() &&
        (!followUp || responseLifecycle.active),
    )
  }

  async function stopAudioCapture() {
    await audioCapture.stop()
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
    await setPage(buildCompactPage("TAP TO TALK"), "compact")
  }

  async function showListening() {
    clearThinkingAnimation()
    visibleAssistant = undefined
    if (latestTranscript) {
      await renderTranscript()
      return
    }
    await setPage(
      buildCompactPage("LISTENING  ·  PAUSE TO SEND"),
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
    try {
      await setPage(
        buildMessagePage(presentation.message, "OPENING FOLLOW-UP"),
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
    // The server ignores starts until the previous transcription has finished.
    // If it is still stopping, listening_stopped will open the follow-up stream.
    if (listeningState === "idle") {
      await startListening(true)
    }
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
      sendControl("listening_stop")
      listeningState = "idle"
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
    if (!socketIsOpen()) {
      reportStatus("Reconnecting")
      forceReconnect()
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

    const wasFollowUp = responseLifecycle.active
    cancelAssistantResponseWindow()
    if (wasFollowUp && (listeningState === "listening" || listeningState === "starting")) {
      listeningState = "stopping"
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
    if (wasListening || audioCapture.state !== "idle") {
      await stopAudioCapture()
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
        listeningState = "stopping"
        sendControl("listening_stop")
      }
      await stopAudioCapture()
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
    visibleAssistant = presentation
    reportStatus("Connected")
    await showAssistantPresentation(presentation)
  }

  async function completeAssistantTurn() {
    awaitingResponse = false
    clearThinkingAnimation()
    if (sleeping) {
      reportStatus("Sleeping")
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
        // Speech within the window owns the turn; do not cut it off at 30s.
        if (listeningState === "listening") {
          cancelAssistantResponseWindow()
          visibleAssistant = undefined
        }
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
        if (
          listeningState === "starting" ||
          listeningState === "listening"
        ) {
          listeningState = "stopping"
          sendControl("listening_stop")
          await stopAudioCapture()
        }
        if (sleeping) {
          reportStatus("Sleeping")
          return
        }
        reportStatus("Thinking")
        await startThinkingAnimation()
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
        const presentation: AssistantPresentation = {
          message: presentGlassesMessage(responseText),
          sourceText: responseText,
        }
        lastAssistant = presentation
        await displayAssistantPresentation(presentation)
        return
      }

      case "assistant_repeat": {
        if (!lastAssistant) {
          awaitingResponse = false
          clearThinkingAnimation()
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
          cancelAssistantResponseWindow()
          awaitingResponse = false
          latestTranscript = ""
          reportStatus(message.error)
          idlePrompt = "MIC UNAVAILABLE  ·  TAP TO RETRY"
          if (!currentNotification) {
            await showIdlePrompt(idlePrompt)
          }
        } else {
          awaitingResponse = false
          if (responseLifecycle.active && !currentNotification) {
            await startListening(true)
            return
          }
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
      cancelAssistantResponseWindow()
      listeningState = "stopping"
      awaitingResponse = true
      const sent = sendControl("listening_stop")
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

    await startListening()
  }

  async function startListening(followUp = false) {
    if (!active || sleeping || currentNotification || !socketIsOpen() ||
        listeningState !== "idle" || (followUp && !responseLifecycle.active)) {
      return
    }
    latestTranscript = ""
    idlePrompt = undefined
    if (!followUp) {
      resetAssistantInteraction()
      visibleAssistant = undefined
    }
    listeningState = "starting"
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
      listeningState = "idle"
      if (followUp && !responseLifecycle.active) {
        reportStatus("Connected")
        await showReady()
        return
      }
      reportStatus("Microphone unavailable")
      await showIdlePrompt("MIC UNAVAILABLE  ·  TAP TO RETRY")
      return
    }
    if (!sendControl("listening_start")) {
      listeningState = "idle"
      await stopAudioCapture()
      reportStatus("Reconnecting")
      closeSocketQuietly(socket)
      forceReconnect()
      await showConnectionLost()
      return
    }
    listeningState = "listening"
    reportStatus("Listening")
    if (followUp && visibleAssistant) {
      await setPage(buildMessagePage(visibleAssistant.message, "LISTENING  ·  FOLLOW UP"), "message")
    } else {
      await showListening()
    }
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
          ownedPcm = Uint8Array.from(pcm)
          safeSend(socket, ownedPcm.buffer)
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

  let teardownPromise: Promise<void> | undefined
  function teardown(): Promise<void> {
    if (!active) {
      return teardownPromise ?? Promise.resolve()
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
    stopEvents()
    stopLocationEvents()
    unbindSocket()
    const closingSocket = socket
    socket = undefined
    closeSocketQuietly(closingSocket)
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
