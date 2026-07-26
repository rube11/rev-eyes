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
}

type ListeningState = "idle" | "starting" | "listening" | "stopping"
type DisplaySurface = "compact" | "message" | "offline" | "sleep" | "transcript"
type NotificationPresentation = {
  id: string
  message: GlassesMessage
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
  const bridge = await ensurePage()
  onStatus("Connecting")
  await renderGlassesPage(buildSystemPage(
    "CONNECTING",
    "SECURE LINK",
    "Connecting to your assistant…",
    "Please wait",
  ))

  const socket = await connectRealtimeSocket(accessToken)
  let active = true
  let listeningState: ListeningState = "idle"
  let locationStarted = false
  let popupVisible = false

  const showReady = () => renderGlassesPage(buildSystemPage(
    "READY",
    "ASK ANYTHING",
    "Tap below, then speak naturally.",
    "Tap to talk",
  ))
  const showListening = () => renderGlassesPage(buildSystemPage(
    "LISTENING",
    "I'M LISTENING",
    "Speak naturally. Tap again when you're done.",
    "Tap when done",
  ))
  const showConnectionLost = () => renderGlassesPage(buildSystemPage(
    "OFFLINE",
    "CONNECTION LOST",
    "Reopen the phone app to reconnect.",
    "Open phone to continue",
  ))

  onStatus("Connected")
  await showReady()

  const handleServerMessage = async (message: ServerMessage) => {
    switch (message.type) {
      case "assistant_response": {
        if (!message.text) {
          return
        }
        onResponse(message.text)
        const presentation = presentGlassesMessage(message.text)
        popupVisible = presentation.kind === "reminder" ||
          presentation.kind === "update"
        const state = listeningState === "listening" ? "LISTENING" : "READY"
        await renderGlassesPage(buildMessagePage(presentation, state))
        return
      }

      case "listening_stopped": {
        const stoppedUnexpectedly =
          listeningState === "starting" || listeningState === "listening"
        listeningState = "idle"
        if (stoppedUnexpectedly) {
          await bridge.audioControl(false).catch(() => undefined)
        }
        if (message.error) {
          onStatus(message.error)
          popupVisible = false
          await renderGlassesPage(buildSystemPage(
            "UNAVAILABLE",
            "MICROPHONE",
            message.error,
            "Tap to retry",
          ))
        } else {
          onStatus("Connected")
          if (stoppedUnexpectedly && !popupVisible) {
            await showReady()
          }
        }
        return
      }
    }
  }
  const handleMessage = (event: MessageEvent<string>) => {
    try {
      const message = JSON.parse(event.data) as ServerMessage
      void handleServerMessage(message).catch(() => {
        onStatus("Glasses command failed")
      })
    } catch {
      // Ignore unknown server messages.
    }
  }
  const handleClose = () => {
    if (!active) {
      return
    }
    listeningState = "idle"
    popupVisible = false
    onStatus("Disconnected")
    void showConnectionLost().catch(() => undefined)
    void bridge.audioControl(false).catch(() => undefined)
    if (locationStarted) {
      locationStarted = false
      void bridge.stopAppLocationUpdates().catch(() => undefined)
    }
  }
  socket.addEventListener("message", handleMessage)
  socket.addEventListener("close", handleClose)

  const handleEvenEvent = async (event: EvenHubEvent) => {
    const pcm = event.audioEvent?.audioPcm
    if (
      pcm &&
      listeningState === "listening" &&
      socket.readyState === WebSocket.OPEN
    ) {
      socket.send(new Uint8Array(pcm))
      return
    }

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
    if (!listClick && !textClick && !systemClick) {
      return
    }

    if (popupVisible) {
      popupVisible = false
      if (listeningState === "listening") {
        await showListening()
      } else {
        await showReady()
      }
      return
    }

    if (listeningState === "listening") {
      listeningState = "stopping"
      sendControl(socket, "listening_stop")
      await bridge.audioControl(false).catch(() => undefined)
      onStatus("Connected")
      await showReady()
      return
    }

    if (listeningState !== "idle") {
      return
    }

    if (socket.readyState !== WebSocket.OPEN) {
      await showConnectionLost()
      return
    }
    listeningState = "starting"
    onStatus("Starting microphone")
    await renderGlassesPage(buildSystemPage(
      "STARTING",
      "MICROPHONE",
      "Opening the glasses microphone…",
      "Please wait",
    ))
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
      onStatus("Microphone unavailable")
      await renderGlassesPage(buildSystemPage(
        "UNAVAILABLE",
        "MICROPHONE",
        "Could not start the glasses microphone.",
        "Tap to retry",
      ))
      return
    }
    if (!sendControl(socket, "listening_start")) {
      listeningState = "idle"
      await bridge.audioControl(false).catch(() => undefined)
      onStatus("Disconnected")
      await showConnectionLost()
      return
    }
    listeningState = "listening"
    onStatus("Listening")
    await showListening()
  }
  const stopEvents = bridge.onEvenHubEvent((event) => {
    void handleEvenEvent(event).catch(() => {
      onStatus("Glasses command failed")
    })
  })

  const stopLocationEvents = bridge.onAppLocationChanged((location) => {
    sendLocation(socket, location)
  })
  void bridge.startAppLocationUpdates({
    accuracy: AppLocationAccuracy.Medium,
    intervalMs: 5000,
    distanceFilter: 10,
  }).then(async (started) => {
    locationStarted = started
    if (!active || socket.readyState !== WebSocket.OPEN) {
      if (started) {
        locationStarted = false
        await bridge.stopAppLocationUpdates()
      }
      return
    }
    if (started) {
      const location = await bridge.getAppLocation({
        accuracy: AppLocationAccuracy.Medium,
        timeoutMs: 5000,
      })
      if (active && location) {
        sendLocation(socket, location)
      }
    }
  }).catch(() => undefined)

  return () => {
    active = false
    listeningState = "idle"
    stopEvents()
    stopLocationEvents()
    socket.removeEventListener("message", handleMessage)
    socket.removeEventListener("close", handleClose)
    socket.close()
    void bridge.audioControl(false).catch(() => undefined)
    if (locationStarted) {
      void bridge.stopAppLocationUpdates().catch(() => undefined)
    }
  }
}
