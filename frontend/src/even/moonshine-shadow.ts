      }
      if (!this.isCurrentRun(generation, run)) {
        return false
      }
      if (context.state !== "running" || !prepared.transcriber.isActive) {
        throw new Error(
          `Moonshine did not become active (context=${context.state})`,
        )
      }
      this.logLifecycle("running")
      return true
    } catch (error) {
      await this.handleActivationError(generation, error)
      return false
    }
  }

  private isCurrentRun(
    generation: number,
    run: MoonshineRunDiagnostics,
  ): boolean {
    return (
      generation === this.runGeneration &&
      this.running &&
      this.shouldRun &&
      this.runDiagnostics === run
    )
  }

  private async handleActivationError(
    generation: number,
    error: unknown,
  ): Promise<void> {
    if (generation !== this.runGeneration || !this.prepared) {
      return
    }
    console.warn(
      `[Moonshine shadow] could not start: ${describeError(error)}`,
    )
    this.shouldRun = false
    this.voiceReplyArmed = false
    this.runGeneration += 1
    const run = this.runDiagnostics
    this.candidates.finalizePending("activation_failed")
    this.runDiagnostics = undefined
    this.speechActive = false
    this.captureAndClearPcm(run)
    this.running = false
    const prepared = this.prepared
    prepared.inference.endSession()
    await this.stopPrepared(prepared)
    this.logRunSummary(run, "failed", prepared)
    this.notifyRunComplete(run)
  }

  private logTranscript(kind: "partial" | "committed", text: string): void {
    this.diagnostics.transcript(kind, text, this.running)
  }

  private logLifecycle(event: string): void {
    this.diagnostics.lifecycle(event, this.runDiagnostics, this.prepared)
  }

  private logRunSummary(
    run: MoonshineRunDiagnostics | undefined,
    event: string,
    prepared: PreparedMoonshine,
  ): void {
    this.diagnostics.runSummary(run, event, prepared)
  }

  private finishStop(event: string): void {
    if (!this.prepared || !this.running) {
      return
    }
    const run = this.runDiagnostics
    this.candidates.finalizePending("run_stop")
    this.runDiagnostics = undefined
    this.speechActive = false
    this.voiceReplyArmed = false
    this.captureAndClearPcm(run)
    this.running = false
    const prepared = this.prepared
    prepared.inference.endSession()
    void this.stopPrepared(prepared).then(() => {
      this.logRunSummary(run, event, prepared)
      this.notifyRunComplete(run)
    })
  }

  private stopPrepared(prepared: PreparedMoonshine): Promise<void> {
    try {
      prepared.transcriber.stop()
    } catch (error) {
      console.warn(
        `[Moonshine shadow] could not stop cleanly: ${describeError(error)}`,
      )
    }
    try {
      // Keep the synthetic MediaStream's AudioContext alive between runs.
      // This WebView leaves resume() pending indefinitely after suspend().
      prepared.audio.clear()
      return Promise.resolve()
    } catch (error) {
      console.warn(
        `[Moonshine shadow] could not clear PCM adapter: ${describeError(error)}`,
      )
      // Never let the shadow path interfere with the G2 microphone lifecycle.
      return Promise.resolve()
    }
  }

  private captureAndClearPcm(
    run: MoonshineRunDiagnostics | undefined,
  ): void {
    const snapshot = this.candidates.clear()
    if (run) {
      run.ringStartSampleOffset = snapshot.startSampleOffset
      run.ringEndSampleOffset = snapshot.endSampleOffset
      run.ringRetainedSamples = snapshot.retainedSamples
    }
  }

  private recordCandidateTrigger(event: CandidateTriggerEvent): void {
    if (this.runDiagnostics) {
      this.runDiagnostics.gateTriggers += 1
    }
    this.diagnostics.candidateTrigger(event)
  }

  private recordCandidateFinalized(event: CandidateFinalizedEvent): void {
    if (this.runDiagnostics) {
      this.runDiagnostics.candidates += 1
      this.runDiagnostics.candidateBytes += event.byteLength
      this.runDiagnostics.candidateSubmitted ||= event.submitted
    }
    this.diagnostics.candidateFinalized(event)
    try {
      this.onCandidateFinalized?.(event)
    } catch {
      // Presentation bookkeeping must not change candidate cleanup.
    }
  }

  private notifyRunComplete(
    run: MoonshineRunDiagnostics | undefined,
  ): void {
    this.onRunComplete?.({
      candidateSubmitted: run?.candidateSubmitted === true,
    })
  }
}
