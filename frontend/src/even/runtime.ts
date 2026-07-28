import {
  AppLocationAccuracy,
  AudioInputSource,
  CreateStartUpPageContainer,
  OsEventTypeList,
  StartUpPageCreateResult,
  TextContainerUpgrade,
  waitForEvenAppBridge,
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
import { connectRealtimeSocket } from "../shared/api/client"

type ServerMessage = {
  type: string
  id?: string
  text?: string
  error?: string
  awaitingConfirmation?: boolean
}

type ListeningState = "idle" | "starting" | "listening" | "stopping"
type DisplaySurface = "compact" | "message" | "offline" | "sleep" | "transcript"
type NotificationPresentation = {
  id: string
  message: GlassesMessage
}
type AssistantPresentation = {
  message: GlassesMessage
  awaitingConfirmation: boolean
}
type SocketBinding = {
  socket: WebSocket
  handleClose: () => void
  handleMessage: (event: MessageEvent<unknown>) => void
}

const THINKING_FRAME_DELAY_MS = 480
const CONNECTION_TIMEOUT_MS = 10_000
const RECONNECT_BASE_DELAY_MS = 500
const RECONNECT_MAX_DELAY_MS = 10_000
const RELEASE_CLICK_SUPPRESSION_MS = 750

let bridgePromise: ReturnType<typeof waitForEvenAppBridge> | undefined
let startup: Promise<void> | undefined
let exitEventsRegistered = false
let pageMutationTail: Promise<void> = Promise.resolve()
let pageSuspended = false

function getBridge() {
  bridgePromise ??= waitForEvenAppBridge()
  return bridgePromise
}

function serializePageMutation<T>(mutation: () => Promise<T>): Promise<T> {
  const result = pageMutationTail.then(mutation, mutation)
  pageMutationTail = result.then(
    () => undefined,
    () => undefined,
  )
  return result
}

async function ensurePage() {
  const bridge = await getBridge()

  startup ??= (async () => {
    const initialPage = buildCompactPage("SIGN IN ON PHONE")
    const result = await bridge.createStartUpPageContainer(
      new CreateStartUpPageContainer({
        containerTotalNum: initialPage.containerTotalNum,
        listObject: initialPage.listObject,
        textObject: initialPage.textObject,
        imageObject: initialPage.imageObject,
      }),
    )
    if (result !== StartUpPageCreateResult.success) {
      throw new Error(`Glasses page failed (${result})`)
    }
  })().catch((error: unknown) => {
    startup = undefined
    throw error
  })

  await startup
  if (!exitEventsRegistered) {
    exitEventsRegistered = true
    bridge.onEvenHubEvent((event) => {
      const eventType =
        event.listEvent?.eventType ??
        event.textEvent?.eventType ??
        event.sysEvent?.eventType
      if (eventType === OsEventTypeList.DOUBLE_CLICK_EVENT) {
        pageSuspended = true
        void serializePageMutation(async () => {
          const stopped = await bridge.shutDownPageContainer(0)
          if (stopped) {
            startup = undefined
          }
        }).catch(() => undefined)
      }
    })
  }

  return bridge
}

async function renderGlassesPage(page: RebuildPageContainer): Promise<void> {
  if (pageSuspended) {
    return
  }
  await serializePageMutation(async () => {
    if (pageSuspended) {
      return
    }
    const bridge = await ensurePage()
    const rebuilt = await bridge.rebuildPageContainer(page)
    if (!rebuilt) {
      throw new Error("Glasses display update failed")
    }
  })
}

async function upgradeTranscriptText(content: string): Promise<boolean> {
  if (pageSuspended) {
    return false
  }
  return serializePageMutation(async () => {
    if (pageSuspended) {
      return false
    }
    const bridge = await ensurePage()
    return bridge.textContainerUpgrade(new TextContainerUpgrade({
      containerID: 1,
      containerName: "live-transcript",
      content,
    }))
  })
}

export async function showEvenMessage(text: string): Promise<void> {
  if (pageSuspended) {
    pageSuspended = false
    startup = undefined
  }
  await renderGlassesPage(buildCompactPage(text))
}

function safeSend(socket: WebSocket | undefined, data: string | ArrayBuffer) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return false
  }
  try {
    socket.send(data)
    return true
  } catch {
    return false
  }
}

function safeSendJson(socket: WebSocket | undefined, value: object) {
  return safeSend(socket, JSON.stringify(value))
}

function sendLocation(socket: WebSocket | undefined, location: AppLocation) {
  return safeSendJson(socket, {
    type: "location",
    latitude: location.latitude,
    longitude: location.longitude,
    accuracy_meters: location.accuracy,
  })
}

function closeQuietly(socket: WebSocket | undefined) {
  if (!socket) {
    return
  }
  try {
    socket.close()
  } catch {
    // The browser may already have finalized the socket.
  }
}

function parseServerMessage(data: unknown): ServerMessage | undefined {
  if (typeof data !== "string") {
    return undefined
  }
  try {
    const value: unknown = JSON.parse(data)
    if (
      typeof value !== "object" ||
      value === null ||
      !("type" in value) ||
      typeof value.type !== "string"
    ) {
      return undefined
    }
    return {
      type: value.type,
      id: "id" in value && typeof value.id === "string" ? value.id : undefined,
      text: "text" in value && typeof value.text === "string"
        ? value.text
        : undefined,
      error: "error" in value && typeof value.error === "string"
        ? value.error
        : undefined,
      awaitingConfirmation:
        "awaiting_confirmation" in value &&
        typeof value.awaiting_confirmation === "boolean"
          ? value.awaiting_confirmation
          : undefined,
    }
  } catch {
    return undefined
  }
}

function reconnectDelay(attempt: number) {
  const exponential = Math.min(
    RECONNECT_MAX_DELAY_MS,
    RECONNECT_BASE_DELAY_MS * 2 ** Math.min(attempt, 5),
  )
  const jitter = 0.75 + Math.random() * 0.5
  return Math.min(
    RECONNECT_MAX_DELAY_MS,
    Math.round(exponential * jitter),
  )
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
): Promise<() => void> {
  if (pageSuspended) {
    pageSuspended = false
    startup = undefined
  }
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
  const bridge = await getBridge()

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
    return socket?.readyState === WebSocket.OPEN
  }

  function sendControl(type: string) {
    return safeSendJson(socket, { type })
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
      buildCompactPage("LISTENING  ·  TAP TO FINISH"),
      "compact",
    )
  }

  async function showConnectionLost() {
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
    clearThinkingAnimation()
    await setPage(
      buildMessagePage(
        presentation.message,
        presentation.awaitingConfirmation ? "TAP TO RESPOND" : "TAP TO DISMISS",
      ),
      "message",
    )
  }

  async function showIdlePrompt(prompt: string) {
    clearThinkingAnimation()
    idlePrompt = prompt
    await setPage(buildCompactPage(prompt), "compact")
  }

  async function enterSleep() {
    if (sleeping) {
      return
    }

    clearThinkingAnimation()
    latestTranscript = ""
    sleeping = true
    visibleAssistant = undefined
    if (listeningState !== "idle") {
      awaitingResponse =
        listeningState === "listening" || listeningState === "stopping"
      sendControl("listening_stop")
      listeningState = "idle"
      await bridge.audioControl(false).catch(() => undefined)
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

  async function presentNotification(message: ServerMessage) {
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
    clearThinkingAnimation()
    awaitingResponse = false
    latestTranscript = ""
    idlePrompt = undefined
    const wasListening = listeningState !== "idle"
    listeningState = "idle"
    if (wasListening) {
      await bridge.audioControl(false).catch(() => undefined)
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
      const message = parseServerMessage(event.data)
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
      closeQuietly(nextSocket)
      scheduleReconnect()
      return
    }

    clearReconnectTimer()
    reconnectAttempt = 0
    if (socket && socket !== nextSocket) {
      unbindSocket(socket)
      closeQuietly(socket)
    }
    socket = nextSocket
    bindSocket(nextSocket)

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
          closeQuietly(nextSocket)
          return
        }
        connecting = false
        connectionAbort = undefined
        await enqueueTransition(async () => {
          await handleConnected(nextSocket)
        })
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

  async function handleServerMessage(message: ServerMessage) {
    switch (message.type) {
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
        return
      }

      case "assistant_response": {
        if (!message.text) {
          return
        }
        onResponse(message.text)
        const presentation: AssistantPresentation = {
          message: presentGlassesMessage(message.text),
          awaitingConfirmation: message.awaitingConfirmation === true,
        }
        awaitingResponse = false
        clearThinkingAnimation()
        if (listeningState !== "idle") {
          if (
            listeningState === "starting" ||
            listeningState === "listening"
          ) {
            sendControl("listening_stop")
          }
          listeningState = "idle"
          await bridge.audioControl(false).catch(() => undefined)
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
        return
      }

      case "listening_stopped": {
        const stoppedUnexpectedly =
          listeningState === "starting" || listeningState === "listening"
        const wasThinking = thinking
        clearThinkingAnimation()
        listeningState = "idle"
        if (stoppedUnexpectedly) {
          await bridge.audioControl(false).catch(() => undefined)
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
      const sent = sendControl("listening_stop")
      await bridge.audioControl(false).catch(() => undefined)
      if (!sent) {
        listeningState = "idle"
        awaitingResponse = false
        reportStatus("Reconnecting")
        closeQuietly(socket)
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
      const shouldRespond = visibleAssistant.awaitingConfirmation
      visibleAssistant = undefined
      if (!shouldRespond) {
        await showReady()
        return
      }
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

    latestTranscript = ""
    idlePrompt = undefined
    visibleAssistant = undefined
    listeningState = "starting"
    reportStatus("Starting microphone")
    await setPage(buildCompactPage("STARTING MICROPHONE"), "compact")
    if (!active || listeningState !== "starting") {
      return
    }
    const started = await bridge.audioControl(
      true,
      AudioInputSource.Glasses,
    ).catch(() => false)
    if (!active || listeningState !== "starting") {
      if (started) {
        await bridge.audioControl(false).catch(() => undefined)
      }
      return
    }
    if (!started) {
      listeningState = "idle"
      reportStatus("Microphone unavailable")
      await showIdlePrompt("MIC UNAVAILABLE  ·  TAP TO RETRY")
      return
    }
    if (!sendControl("listening_start")) {
      listeningState = "idle"
      await bridge.audioControl(false).catch(() => undefined)
      reportStatus("Reconnecting")
      closeQuietly(socket)
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
      if (listeningState === "listening") {
        try {
          safeSend(socket, Uint8Array.from(pcm).buffer)
        } catch {
          // Ignore malformed or late audio frames.
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
    listeningState = "idle"
    stopEvents()
    stopLocationEvents()
    unbindSocket()
    const closingSocket = socket
    socket = undefined
    closeQuietly(closingSocket)
    void bridge.audioControl(false).catch(() => undefined)
    if (locationStarted) {
      locationStarted = false
      void bridge.stopAppLocationUpdates().catch(() => undefined)
    }
  }

  return teardown
}
