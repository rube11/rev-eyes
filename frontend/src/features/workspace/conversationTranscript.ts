import type { TranscriptItem } from './workspaceTypes.js'

// Fetch one session at a time; the workspace's recent-message batch is only a preview.
export async function readConversationTranscript(
  fetchPage: (offset: number, limit: number) => Promise<TranscriptItem[]>,
  signal: AbortSignal,
): Promise<TranscriptItem[]> {
  const messages = new Map<string, TranscriptItem>()
  const pageSize = 500
  for (let offset = 0; ; offset += pageSize) {
    signal.throwIfAborted()
    const page = await fetchPage(offset, pageSize)
    signal.throwIfAborted()
    for (const message of page) messages.set(message.id, message)
    if (page.length < pageSize) break
  }
  return [...messages.values()].sort((a, b) =>
    Date.parse(a.startedAt) - Date.parse(b.startedAt) || a.id.localeCompare(b.id),
  )
}
