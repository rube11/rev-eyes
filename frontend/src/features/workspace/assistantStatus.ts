// `thread` is the T3 thread on the glasses; speech goes there instead of Eyes.
export function getAssistantStatus(status: string, preview = false, thread?: string) {
  if (preview) return { label: 'Preview', detail: 'Sample data only. Your glasses and account are not connected in this preview.', active: false }
  const normalized = status.toLowerCase().trim()
  // Display failures carry SDK detail, e.g. "Glasses page failed (2)".
  if (normalized.startsWith('glasses display update') || normalized.startsWith('glasses page failed')) {
    return { label: 'Display issue', detail: 'Your glasses didn’t accept the last screen update. Check them in the Even app, or reconnect.', active: false }
  }
  switch (normalized) {
    case 'connected':
    case 'sleeping':
      return thread
        ? { label: 'Ready', detail: `Tap your glasses, then speak. A pause sends your message to “${thread}” in T3.`, active: true }
        : { label: 'Ready', detail: 'Tap your glasses, then speak. Pause when you’re finished; Eyes will take it from there.', active: true }
    case 'listening':
      return { label: 'Listening', detail: 'Speak naturally. A pause ends your turn—no second tap needed.', active: true }
    case 'thinking':
      return { label: 'Thinking', detail: 'Your reply will appear on your glasses when it’s ready.', active: true }
    case 'sending':
      return { label: 'Sending', detail: 'Sending your message to the selected thread.', active: true }
    case 'reading reply':
      return { label: 'Reading reply', detail: 'Checking the selected thread for a reply.', active: true }
    case 't3 working':
      return { label: 'T3 working', detail: 'T3 is replying. Its reply appears on your glasses as it’s written.', active: true }
    // T3 failed, not the glasses link: don't send the user to the Even app.
    case 'couldn’t read reply':
    case 'couldn’t load threads':
      return { label: 'T3 unavailable', detail: 'Your glasses are connected, but T3 didn’t respond. Check that T3 Code is running, then scroll to retry.', active: true }
    // Server speech errors; the glasses connection itself is still up.
    case 'transcription unavailable':
    case 'server listening unavailable':
    case 'candidate audio mode is active':
      return { label: 'Speech unavailable', detail: 'Eyes couldn’t transcribe that. Tap your glasses to try again.', active: true }
    case 'starting microphone':
      return { label: 'Starting mic', detail: 'Wait for the listening cue before speaking.', active: true }
    case 'connecting':
    case 'reconnecting':
      return { label: 'Connecting', detail: 'Keep the Even app open while Eyes establishes the connection.', active: false }
    case 'microphone unavailable':
      return { label: 'Mic unavailable', detail: 'Check the microphone permission and glasses connection in the Even app, then tap to try again.', active: false }
    case 'glasses command failed':
      return { label: 'Needs attention', detail: 'Check the connection in the Even app, then try again.', active: false }
    case 'offline':
    case 'disconnected':
      return { label: 'Not connected', detail: 'Open Eyes in the Even app to connect your glasses.', active: false }
    default:
      return { label: 'Needs attention', detail: 'Check Eyes in the Even app before starting a conversation.', active: false }
  }
}
