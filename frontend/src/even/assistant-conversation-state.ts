// Owns the small state machine between a displayed assistant response and the
// optional hands-free reply that follows it. Timers, rendering, and audio
// transport stay with their existing owners.
export class AssistantConversationState {
  private replyInProgress = false
  private voiceReplyIsAvailable = false

  get replyActive(): boolean {
    return this.replyInProgress
  }

  get voiceReplyAvailable(): boolean {
    return this.voiceReplyIsAvailable
  }

  openResponseWindow(voiceReplyAvailable: boolean): void {
    this.replyInProgress = false
    this.voiceReplyIsAvailable = voiceReplyAvailable
  }

  closeResponseWindow(): void {
    this.voiceReplyIsAvailable = false
  }

  beginReply(): boolean {
    if (!this.voiceReplyIsAvailable || this.replyInProgress) {
      return false
    }
    this.voiceReplyIsAvailable = false
    this.replyInProgress = true
    return true
  }

  finishReply(): void {
    this.replyInProgress = false
  }

  reset(): void {
    this.replyInProgress = false
    this.voiceReplyIsAvailable = false
  }
}
