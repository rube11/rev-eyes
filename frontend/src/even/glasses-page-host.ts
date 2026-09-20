import {
  CreateStartUpPageContainer,
  OsEventTypeList,
  StartUpPageCreateResult,
  TextContainerUpgrade,
  waitForEvenAppBridge,
} from "@evenrealities/even_hub_sdk"
import type { RebuildPageContainer } from "@evenrealities/even_hub_sdk"

import { buildCompactPage } from "./glasses-ui"

type EvenBridge = Awaited<ReturnType<typeof waitForEvenAppBridge>>

const NATIVE_OPERATION_TIMEOUT_MS = 2_000

let bridgePromise: Promise<EvenBridge> | undefined
let startup: Promise<void> | undefined
let stopExitEvents: (() => void) | undefined
let pageMutationTail: Promise<void> = Promise.resolve()
let pageSuspended = false
let hostGeneration = 0

function withNativeTimeout<T>(operation: Promise<T>, name: string): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const timer = setTimeout(
      () => reject(new Error(`${name} timed out`)),
      NATIVE_OPERATION_TIMEOUT_MS,
    )
    operation.then(
      value => { clearTimeout(timer); resolve(value) },
      error => { clearTimeout(timer); reject(error) },
    )
  })
}

export function resetGlassesPageHost(): void {
  hostGeneration += 1
  try { stopExitEvents?.() } catch { /* The old bridge may already be gone. */ }
  stopExitEvents = undefined
  bridgePromise = undefined
  startup = undefined
  pageMutationTail = Promise.resolve()
  pageSuspended = false
}

export function getEvenBridge() {
  if (!bridgePromise) {
    const generation = hostGeneration
    const attempt = withNativeTimeout(waitForEvenAppBridge(), "Even bridge connection")
    bridgePromise = attempt
    void attempt.catch(() => {
      if (hostGeneration === generation && bridgePromise === attempt) {
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
  const generation = hostGeneration
  const run = () => {
    if (generation !== hostGeneration) {
      throw new Error("Glasses page operation was superseded")
    }
    return mutation()
  }
  const result = pageMutationTail.then(run, run)
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
    const result = await withNativeTimeout(
      bridge.createStartUpPageContainer(new CreateStartUpPageContainer({
        containerTotalNum: initialPage.containerTotalNum,
        listObject: initialPage.listObject,
        textObject: initialPage.textObject,
        imageObject: initialPage.imageObject,
      })),
      "Create glasses page",
    )
    if (result !== StartUpPageCreateResult.success) {
      throw new Error(`Glasses page failed (${result})`)
    }
  })().catch((error: unknown) => {
    startup = undefined
    throw error
  })

  await startup
  if (!stopExitEvents) {
    stopExitEvents = bridge.onEvenHubEvent((event) => {
      const eventType =
        event.listEvent?.eventType ??
        event.textEvent?.eventType ??
        event.sysEvent?.eventType
      if (eventType === OsEventTypeList.DOUBLE_CLICK_EVENT) {
        pageSuspended = true
        void serializePageMutation(async () => {
          const stopped = await withNativeTimeout(
            bridge.shutDownPageContainer(0),
            "Shut down glasses page",
          )
          if (stopped) {
            startup = undefined
          }
        }).catch(() => undefined)
      }
    })
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
    try {
      const rebuilt = await withNativeTimeout(
        bridge.rebuildPageContainer(page),
        "Render glasses page",
      )
      if (!rebuilt) {
        throw new Error("Glasses display update failed")
      }
    } catch (error) {
      // A disconnect can drop the native page. Let the next attempt create it again.
      startup = undefined
      throw error
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
    return withNativeTimeout(bridge.textContainerUpgrade(new TextContainerUpgrade({
      containerID: 1,
      containerName: "live-transcript",
      content,
    })), "Update glasses transcript")
  })
}

export async function showEvenMessage(text: string): Promise<void> {
  resumeGlassesPage()
  await renderGlassesPage(buildCompactPage(text))
}

export async function upgradeMessageStatus(content: string): Promise<boolean> {
  if (pageSuspended) return false
  return serializePageMutation(async () => {
    if (pageSuspended) return false
    const bridge = await ensurePage()
    return withNativeTimeout(bridge.textContainerUpgrade(new TextContainerUpgrade({
      containerID: 2, containerName: "message-status", content,
    })), "Update glasses message status")
  })
}
