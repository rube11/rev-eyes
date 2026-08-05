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
