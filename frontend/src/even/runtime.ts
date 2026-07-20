import {
  CreateStartUpPageContainer,
  ListContainerProperty,
  ListItemContainerProperty,
  RebuildPageContainer,
  TextContainerProperty,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"

import { connectAudioSocket } from "../shared/api/client"

type ServerMessage = {
  type: string
  text?: string
}

let startup: Promise<unknown> | undefined

export async function initializeEvenExperience(
  accessToken: string,
  onResponse: (text: string) => void,
): Promise<() => void> {
  const bridge = await waitForEvenAppBridge()
  const menuOptions = new ListContainerProperty({
    xPosition: 100,
    yPosition: 70,
    width: 200,
    height: 150,
    containerID: 1,
    containerName: "menOp",
    itemContainer: new ListItemContainerProperty({
      itemCount: 2,
      itemWidth: 0,
      isItemSelectBorderEn: 1,
      itemName: ["Start audio", "Stop audio"],
    }),
    isEventCapture: 1,
  })

  const statusText = (content: string) => new TextContainerProperty({
    xPosition: 16,
    yPosition: 16,
    width: 160,
    height: 50,
    containerID: 2,
    containerName: "micStatus",
    content,
    isEventCapture: 0,
  })

  if (!startup) {
    startup = bridge.createStartUpPageContainer(new CreateStartUpPageContainer({
      containerTotalNum: 2,
      listObject: [menuOptions],
      textObject: [statusText("Mic: off")],
    })).catch((error: unknown) => {
      startup = undefined
      throw error
    })
  }
  await startup
  const socket = await connectAudioSocket(accessToken)

  const showText = (text: string) => bridge.rebuildPageContainer(
    new RebuildPageContainer({
      containerTotalNum: 2,
      listObject: [menuOptions],
      textObject: [statusText(text)],
    }),
  )

  const handleMessage = (event: MessageEvent<string>) => {
    try {
      const message = JSON.parse(event.data) as ServerMessage
      if (message.type === "assistant_response" && message.text) {
        onResponse(message.text)
        void showText(message.text)
      }
    } catch {
      // Ignore unknown server messages.
    }
  }
  socket.addEventListener("message", handleMessage)

  const stopEvents = bridge.onEvenHubEvent(async (event) => {
    if (event.listEvent?.currentSelectItemIndex === 1) {
      await bridge.audioControl(false)
      await showText("Mic: off")
      return
    }

    if (event.listEvent?.currentSelectItemIndex === 0) {
      await bridge.audioControl(true)
      await showText("Mic: on")
      return
    }

    const pcm = event.audioEvent?.audioPcm
    if (pcm && socket.readyState === WebSocket.OPEN) {
      socket.send(new Uint8Array(pcm))
    }
  })

  const locationWatch = navigator.geolocation?.watchPosition(({ coords }) => {
    if (socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({
        type: "location",
        latitude: coords.latitude,
        longitude: coords.longitude,
        accuracy_meters: coords.accuracy,
      }))
    }
  })

  return () => {
    stopEvents()
    socket.removeEventListener("message", handleMessage)
    socket.close()
    if (locationWatch !== undefined) {
      navigator.geolocation.clearWatch(locationWatch)
    }
    void bridge.audioControl(false)
  }
}
