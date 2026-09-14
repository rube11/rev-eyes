import type { Transcriber } from "@moonshine-ai/moonshine-js"

import {
  guardMoonshineInference,
  type MoonshineInferenceLifecycle,
} from "./moonshine-inference-guard"

export type { MoonshineInferenceLifecycle } from "./moonshine-inference-guard"

type TranscriberRuntime = {
  audioContext?: AudioContext
  sttModel?: unknown
}

type LoadingModel = {
  loadPromise?: Promise<void>
  isModelLoading?: boolean
  isLoaded?: () => boolean
  model?: { encoder?: { release(): Promise<void> }; decoder?: { release(): Promise<void> } }
}

export async function loadMoonshineTranscriber(transcriber: Transcriber): Promise<void> {
  const model = (transcriber as unknown as TranscriberRuntime).sttModel as LoadingModel | undefined
  const work = transcriber.load()
  const attempt = model?.loadPromise
  try {
    await work
  } catch (error) {
    // 0.1.29 caches rejected loadModel() promises and never clears its loading
    // flag on failure. Only clear the attempt that actually rejected; do not
    // interrupt another loader or reset a healthy/shared inference model.
    if (model && attempt && model.loadPromise === attempt && model.isLoaded?.() === false) {
      model.loadPromise = undefined
      model.isModelLoading = false
      const partial = model.model
      const encoder = partial?.encoder
      const decoder = partial?.decoder
      if (partial) { partial.encoder = undefined; partial.decoder = undefined }
      await Promise.allSettled([encoder?.release(), decoder?.release()])
    }
    throw error
  }
}

// Moonshine does not currently expose lifecycle access for its internally
// created AudioContext. Keep the compatibility cast in one place so repeated
// app/session initialization does not leak browser audio contexts.
export function transcriberAudioContext(
  transcriber: Transcriber,
): AudioContext | undefined {
  return (transcriber as unknown as TranscriberRuntime).audioContext
}

export function createMoonshineInferenceLifecycle(
  transcriber: Transcriber,
): MoonshineInferenceLifecycle {
  const model = (transcriber as unknown as TranscriberRuntime).sttModel
  const lifecycle = guardMoonshineInference(model)
  if (!lifecycle) {
    throw new Error("Moonshine inference lifecycle is unavailable")
  }
  return lifecycle
}

export async function disposeMoonshineTranscriber(
  transcriber: Transcriber,
): Promise<void> {
  try {
    transcriber.stop()
  } catch {
    // Continue with context cleanup if Moonshine is already stopped.
  }
  const context = transcriberAudioContext(transcriber)
  if (context && context.state !== "closed") {
    await context.close().catch(() => undefined)
  }
}
