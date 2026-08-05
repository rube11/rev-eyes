import {
  CreateStartUpPageContainer,
  OsEventTypeList,
  StartUpPageCreateResult,
  TextContainerUpgrade,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"
import type { RebuildPageContainer } from "@evenrealities/even_hub_sdk"

import { buildCompactPage } from "./glasses-ui"

let bridgePromise: ReturnType<typeof waitForEvenAppBridge> | undefined
let startup: Promise<void> | undefined
let exitEventsRegistered = false
let pageMutationTail: Promise<void> = Promise.resolve()
let pageSuspended = false

export function getEvenBridge() {
  if (!bridgePromise) {
    const attempt = waitForEvenAppBridge()
    bridgePromise = attempt
    void attempt.catch(() => {
      if (bridgePromise === attempt) {
        bridgePromise = undefined
      }
    })
  }
  return bridgePromise
}

export function resumeGlassesPage(): void {
  if (!pageSuspended) {
    return
  }
  pageSuspended = false
  startup = undefined
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
  const bridge = await getEvenBridge()

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
    exitEventsRegistered = true
  }

  return bridge
}

export async function renderGlassesPage(
  page: RebuildPageContainer,
): Promise<void> {
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

export async function upgradeTranscriptText(content: string): Promise<boolean> {
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
  resumeGlassesPage()
  await renderGlassesPage(buildCompactPage(text))
}
