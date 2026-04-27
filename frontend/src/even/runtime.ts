import {
  CreateStartUpPageContainer,
  ListContainerProperty,
  ListItemContainerProperty,
  TextContainerProperty,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"

export async function initializeEvenExperience(): Promise<void> {
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

  const statusText = new TextContainerProperty({
    xPosition: 16,
    yPosition: 16,
    width: 160,
    height: 50,
    containerID: 2,
    containerName: "micStatus",
    content: "Mic: off",
    isEventCapture: 0,
  })

  await bridge.createStartUpPageContainer(new CreateStartUpPageContainer({
    containerTotalNum: 2,
    listObject: [menuOptions],
    textObject: [statusText],
  }))
}
