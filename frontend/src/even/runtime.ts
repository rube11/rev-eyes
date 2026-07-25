import {
  AppLocationAccuracy,
  AudioInputSource,
  CreateStartUpPageContainer,
  OsEventTypeList,
  StartUpPageCreateResult,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"
import type {
  AppLocation,
  EvenHubEvent,
  RebuildPageContainer,
} from "@evenrealities/even_hub_sdk"

import {
  buildMessagePage,
  buildSystemPage,
  presentGlassesMessage,
} from "./glasses-ui"
import { connectRealtimeSocket } from "../shared/api/client"

type ServerMessage = {
  type: string
  text?: string
  error?: string
}

type ListeningState = "idle" | "starting" | "listening" | "stopping"

let bridgePromise: ReturnType<typeof waitForEvenAppBridge> | undefined
let startup: Promise<void> | undefined
let exitEventsRegistered = false

function getBridge() {
  bridgePromise ??= waitForEvenAppBridge()
  return bridgePromise
}

async function ensurePage() {
  const bridge = await getBridge()

  startup ??= (async () => {
    const initialPage = buildSystemPage(
      "SIGNED OUT",
      "WELCOME",
      "Open the phone app to sign in.",
      "Open phone to continue",
    )
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
        void bridge.shutDownPageContainer(1).catch(() => undefined)
      }
    })
  }

  return bridge
}

async function renderGlassesPage(page: RebuildPageContainer): Promise<void> {
  const bridge = await ensurePage()
  const rebuilt = await bridge.rebuildPageContainer(page)
  if (!rebuilt) {
    throw new Error("Glasses display update failed")
  }
}

export async function showEvenMessage(text: string): Promise<void> {
  await renderGlassesPage(
    buildSystemPage(
      "SIGNED OUT",
      "WELCOME",
      text,
      "Open phone to continue",
    ),
  )
}

function sendLocation(socket: WebSocket, location: AppLocation) {
  if (socket.readyState !== WebSocket.OPEN) {
    return
  }
  socket.send(JSON.stringify({
    type: "location",
    latitude: location.latitude,
    longitude: location.longitude,
    accuracy_meters: location.accuracy,
  }))
}

function sendControl(socket: WebSocket, type: string) {
  if (socket.readyState !== WebSocket.OPEN) {
    return false
  }
  try {
    socket.send(JSON.stringify({ type }))
    return true
  } catch {
    return false
  }
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
