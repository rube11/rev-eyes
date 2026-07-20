import {
  AppLocationAccuracy,
  AudioInputSource,
  CreateStartUpPageContainer,
  ListContainerProperty,
  ListItemContainerProperty,
  OsEventTypeList,
  StartUpPageCreateResult,
  TextContainerProperty,
  TextContainerUpgrade,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"
import type {
  AppLocation,
  EvenHubEvent,
} from "@evenrealities/even_hub_sdk"

import { connectAudioSocket } from "../shared/api/client"

const responseContainer = {
  id: 2,
  name: "response",
}

type ServerMessage = {
  type: string
  text?: string
}

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
    const result = await bridge.createStartUpPageContainer(
      new CreateStartUpPageContainer({
        containerTotalNum: 2,
        listObject: [
          new ListContainerProperty({
            xPosition: 0,
            yPosition: 0,
            width: 160,
            height: 288,
            containerID: 1,
            containerName: "controls",
            itemContainer: new ListItemContainerProperty({
              itemCount: 1,
              itemWidth: 160,
              isItemSelectBorderEn: 1,
              itemName: ["Toggle listening"],
            }),
            isEventCapture: 1,
          }),
        ],
        textObject: [
          new TextContainerProperty({
            xPosition: 176,
            yPosition: 0,
            width: 400,
            height: 288,
            containerID: responseContainer.id,
            containerName: responseContainer.name,
            content: "Open phone to sign in",
            isEventCapture: 0,
          }),
        ],
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

function displayText(text: string) {
  const trimmed = text.trim()
  if (trimmed.length <= 320) {
    return trimmed || "Ready"
  }
  return `${trimmed.slice(0, 317)}...`
}

export async function showEvenMessage(text: string): Promise<void> {
  const bridge = await ensurePage()
  const updated = await bridge.textContainerUpgrade(
    new TextContainerUpgrade({
      containerID: responseContainer.id,
      containerName: responseContainer.name,
      content: displayText(text),
    }),
  )
  if (!updated) {
    throw new Error("Glasses display update failed")
  }
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

export async function initializeEvenExperience(
  accessToken: string,
  onResponse: (text: string) => void,
  onStatus: (status: string) => void,
): Promise<() => void> {
  const bridge = await ensurePage()
  onStatus("Connecting")
  await showEvenMessage("Connecting...")

  const socket = await connectAudioSocket(accessToken)
  let active = true
  let listening = false
  let locationStarted = false
  onStatus("Connected")
  await showEvenMessage("Ready\nPress Toggle listening")

  const handleMessage = (event: MessageEvent<string>) => {
    try {
      const message = JSON.parse(event.data) as ServerMessage
      if (message.type === "assistant_response" && message.text) {
        onResponse(message.text)
        const state = listening ? "Listening..." : "Ready"
        const display = `${state}\n\n${message.text}`
        void showEvenMessage(display).catch(() => {
          onStatus("Display unavailable")
        })
      }
    } catch {
      // Ignore unknown server messages.
    }
  }
  const handleClose = () => {
    if (!active) {
      return
    }
    listening = false
    onStatus("Disconnected")
    void showEvenMessage("Connection lost\nReopen the app").catch(() => undefined)
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
    if (pcm && socket.readyState === WebSocket.OPEN) {
      socket.send(new Uint8Array(pcm))
      return
    }

    const listEvent = event.listEvent
    const listClick =
      listEvent !== undefined &&
      (listEvent.eventType ?? OsEventTypeList.CLICK_EVENT) ===
        OsEventTypeList.CLICK_EVENT
    const systemClick =
      event.sysEvent !== undefined &&
      (event.sysEvent.eventType ?? OsEventTypeList.CLICK_EVENT) ===
        OsEventTypeList.CLICK_EVENT
    if (!listClick && !systemClick) {
      return
    }

    if (listening) {
      await bridge.audioControl(false)
      listening = false
      onStatus("Connected")
      await showEvenMessage("Ready")
      return
    }

    if (socket.readyState !== WebSocket.OPEN) {
      await showEvenMessage("Connection lost\nReopen the app")
      return
    }
    onStatus("Starting microphone")
    await showEvenMessage("Starting microphone...")
    const started = await bridge.audioControl(
      true,
      AudioInputSource.Glasses,
    )
    listening = started
    onStatus(started ? "Listening" : "Microphone unavailable")
    await showEvenMessage(
      started ? "Listening..." : "Could not start microphone",
    )
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
    listening = false
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
