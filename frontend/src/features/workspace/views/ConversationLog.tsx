import { useMemo, useRef, useState } from 'react'
import { PageIntro } from '../components/PageIntro'
import { SearchIcon } from '../components/icons'
import type { SendChatMessage } from '../components/ChatComposer'
import { formatDateTime, relativeTime } from '../format'
import type { ConversationItem, MemoryItem } from '../workspaceTypes'
import { ConversationThread } from './ConversationThread'
import './ConversationLog.css'

export function ConversationLog({
  conversations, memories, userId, onSend, initialConversationId, onOpenMemory,
}: {
  conversations: ConversationItem[]
  memories: MemoryItem[]
  userId?: string
  onSend?: SendChatMessage
  initialConversationId?: string
  onOpenMemory?: (memoryId: string) => void
}) {
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState<string | undefined>(initialConversationId)
  const selected = conversations.find((item) => item.id === selectedId)
  const selectedButton = useRef<HTMLButtonElement | null>(null)
  const filtered = useMemo(() => {
    const normalized = query.toLowerCase().trim()
    return conversations.filter((conversation) => !normalized || [
      conversation.title, conversation.summary, ...conversation.transcript.map((line) => line.text),
    ].join(' ').toLowerCase().includes(normalized))
  }, [conversations, query])
  const activeCount = conversations.filter((item) => item.status === 'active').length

  return <>
    <div hidden={Boolean(selected)}>
      <PageIntro
        title="Chats"
        lede={activeCount
          ? `${activeCount} ${activeCount === 1 ? 'conversation is' : 'conversations are'} still open on your glasses.`
          : 'Every conversation with Eyes, from the glasses and from here.'}
      />
      <label className="search-field">
        <SearchIcon />
        <span className="visually-hidden">Search conversations</span>
        <input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search chats" />
      </label>
      <p className="result-count">{filtered.length} {filtered.length === 1 ? 'conversation' : 'conversations'}</p>
      <ul className="chat-list">
        {filtered.map((conversation) => (
          <li key={conversation.id}>
            <button className={`chat-row${conversation.status === 'active' ? ' chat-row--active' : ''}`} type="button"
              onClick={(event) => { selectedButton.current = event.currentTarget; setSelectedId(conversation.id) }}>
              <span className="chat-row__main">
                <span className="chat-row__title">{conversation.title}</span>
                {conversation.summary ? <span className="chat-row__summary">{conversation.summary}</span> : null}
              </span>
              <span className="chat-row__aside">
                <time className="chat-row__time tnum" dateTime={conversation.lastActivityAt}
                  title={formatDateTime(conversation.lastActivityAt)}>
                  {relativeTime(conversation.lastActivityAt)}
                </time>
                {conversation.status === 'active' ? (
                  <span className="pill pill--accent"><span className="pill__dot" aria-hidden="true" />Active</span>
                ) : conversation.status === 'expired' ? (
                  <span className="chat-row__state">Expired</span>
                ) : null}
              </span>
            </button>
          </li>
        ))}
      </ul>
      {!filtered.length ? <p className="chat-list__empty">{query ? 'No matches. Try another search.' : 'No conversations yet. Tap your glasses and say something.'}</p> : null}
    </div>
    {selected ? <ConversationThread key={`${userId ?? 'demo'}:${selected.id}`} conversation={selected} userId={userId} onSend={onSend}
      related={memories.filter((memory) => memory.sourceConversationId === selected.id && memory.status === 'active')}
      onOpenMemory={onOpenMemory}
      onBack={() => { setSelectedId(undefined); requestAnimationFrame(() => selectedButton.current?.focus()) }} /> : null}
  </>
}
