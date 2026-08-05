declare module "@moonshine-ai/moonshine-js" {
  export type TranscriberCallbacks = {
    onPermissionsRequested: () => void
    onError: (error: unknown) => void
    onModelLoadStarted: () => void
    onModelLoaded: () => void
    onTranscribeStarted: () => void
    onTranscribeStopped: () => void
    onTranscriptionUpdated: (text: string) => void
    onTranscriptionCommitted: (text: string, buffer?: AudioBuffer) => void
    onFrame: (
      probabilities: unknown,
      frame: Float32Array,
      exponentialMovingAverage: number,
    ) => void
    onSpeechStart: () => void
    onSpeechEnd: () => void
  }

  export class Transcriber {
    constructor(
      modelUrl: string,
      callbacks?: Partial<TranscriberCallbacks>,
      useVad?: boolean,
      precision?: string,
    )

    readonly isActive: boolean

    load(): Promise<void>
    attachStream(stream: MediaStream): void
    start(): Promise<void>
    stop(): void
  }
}
