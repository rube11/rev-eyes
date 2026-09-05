export function getAssistantStatus(status: string, preview = false) {
  if (preview) return { label: 'Preview', detail: 'Sample data only. Your glasses and account are not connected in this preview.', active: false }
  switch (status.toLowerCase().trim()) {
    case 'connected':
    case 'sleeping':
      return { label: 'Ready', detail: 'Tap your glasses, then speak. Pause when you’re finished; Eyes will take it from there.', active: true }
    case 'listening':
      return { label: 'Listening', detail: 'Speak naturally. A pause ends your turn—no second tap needed.', active: true }
    case 'thinking':
      return { label: 'Thinking', detail: 'Your reply will appear on your glasses when it’s ready.', active: true }
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
