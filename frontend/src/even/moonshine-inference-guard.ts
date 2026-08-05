const DEFAULT_MAX_PENDING_INFERENCES = 4

type InferenceModel = {
  generate: (audio: Float32Array) => Promise<string | undefined>
}

type InferenceGuardState = {
  activeOwner: object | undefined
  epoch: number
  maxPending: number
  onError: (message: string) => void
  originalGenerate: InferenceModel["generate"]
  pending: number
  tail: Promise<void>
}

type InferenceGuardOptions = {
  maxPending?: number
  onError?: (message: string) => void
}

export type MoonshineInferenceLifecycle = {
  beginSession: () => void
  endSession: () => void
}

const guardedModels = new WeakMap<object, InferenceGuardState>()

function describeError(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function reportError(state: InferenceGuardState, message: string): void {
  try {
    state.onError(message)
  } catch {
    // Diagnostics must not reject Moonshine's otherwise unhandled promise.
  }
}

function isTransientSessionError(error: unknown): boolean {
  const message = describeError(error).toLowerCase()
  return (
    message.includes("session already started") ||
    message.includes("session mismatch")
  )
}

function isCurrentSession(
  state: InferenceGuardState,
  owner: object,
  epoch: number,
): boolean {
  return state.activeOwner === owner && state.epoch === epoch
}

async function runInference(
  state: InferenceGuardState,
  owner: object,
  epoch: number,
  audio: Float32Array,
): Promise<string> {
  if (!isCurrentSession(state, owner, epoch)) {
    return ""
  }

  let lastError: unknown
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const text = await state.originalGenerate(audio)
      return isCurrentSession(state, owner, epoch) && typeof text === "string"
        ? text
        : ""
    } catch (error) {
      lastError = error
      if (
        attempt > 0 ||
        !isTransientSessionError(error) ||
        !isCurrentSession(state, owner, epoch)
      ) {
        break
      }
      // ONNX clears its session marker in the rejected run's finally block.
      await Promise.resolve()
    }
  }

  if (isCurrentSession(state, owner, epoch)) {
    reportError(
      state,
      `[Moonshine shadow] local inference failed: ${describeError(lastError)}`,
    )
  }
  return ""
}

function enqueueInference(
  state: InferenceGuardState,
  audio: Float32Array,
): Promise<string> {
  const owner = state.activeOwner
  if (!owner) {
    return Promise.resolve("")
  }
  if (state.pending >= state.maxPending) {
    reportError(
      state,
      "[Moonshine shadow] local inference queue full; dropped one speech segment",
    )
    return Promise.resolve("")
  }

  const epoch = state.epoch
  state.pending += 1
  const result = state.tail.then(() =>
    runInference(state, owner, epoch, audio),
  )
  state.tail = result.then(
    () => {
      state.pending -= 1
    },
    () => {
      state.pending -= 1
    },
  )
  return result
}

function inferenceModel(value: unknown): InferenceModel | undefined {
  if (
    typeof value !== "object" ||
    value === null ||
    !("generate" in value) ||
    typeof value.generate !== "function"
  ) {
    return undefined
  }
  return value as InferenceModel
}

// Moonshine 0.1.x can invoke its shared ONNX model concurrently at a forced
// VAD commit and again at speech end. ONNX's WASM runtime only permits one run
// at a time. Install one bounded queue per shared model and give each
// transcriber its own session owner so stale results cannot cross restarts.
export function guardMoonshineInference(
  value: unknown,
  options: InferenceGuardOptions = {},
): MoonshineInferenceLifecycle | undefined {
  const model = inferenceModel(value)
  if (!model) {
    return undefined
  }

  let state = guardedModels.get(model)
  if (!state) {
    const requestedMaxPending = Math.trunc(
      options.maxPending ?? DEFAULT_MAX_PENDING_INFERENCES,
    )
    const newState: InferenceGuardState = {
      activeOwner: undefined,
      epoch: 0,
      maxPending:
        Number.isFinite(requestedMaxPending) && requestedMaxPending > 0
          ? requestedMaxPending
          : DEFAULT_MAX_PENDING_INFERENCES,
      onError: options.onError ?? ((message) => console.warn(message)),
      originalGenerate: model.generate.bind(model),
      pending: 0,
      tail: Promise.resolve(),
    }
    state = newState
    guardedModels.set(model, newState)
    model.generate = (audio) => enqueueInference(newState, audio)
  }

  const owner = {}
  let sessionActive = false
  return {
    beginSession: () => {
      if (sessionActive && state.activeOwner === owner) {
        return
      }
      sessionActive = true
      state.activeOwner = owner
      state.epoch += 1
    },
    endSession: () => {
      if (!sessionActive) {
        return
      }
      sessionActive = false
      if (state.activeOwner === owner) {
        state.activeOwner = undefined
        state.epoch += 1
      }
    },
  }
}
