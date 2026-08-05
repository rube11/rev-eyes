// Tracks the one candidate whose terminal message owns the interactive UI.
// Ambient candidates may still upload concurrently, but cannot replace it.
export class FocusedCandidateTracker {
  private candidateID: string | undefined

  get active(): boolean {
    return this.candidateID !== undefined
  }

  focus(candidateID: string, allowed: boolean): boolean {
    if (!allowed) {
      return false
    }
    this.candidateID ??= candidateID
    return this.candidateID === candidateID
  }

  matches(candidateID: string | undefined): boolean {
    return candidateID !== undefined && candidateID === this.candidateID
  }

  competes(candidateID: string | undefined): boolean {
    return (
      this.candidateID !== undefined &&
      candidateID !== undefined &&
      candidateID !== this.candidateID
    )
  }

  clear(): void {
    this.candidateID = undefined
  }
}
