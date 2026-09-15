import { useEffect, useMemo, useRef, useState } from 'react'
import { loadConversationTranscript } from './workspaceData'
import { ChatComposer } from './ChatComposer'
import type { SendChatMessage } from './ChatComposer'
import type { ConversationItem, TranscriptItem } from './workspaceTypes'

const dateTime = (value: string) => new Date(value).toLocaleString(undefined, {
  month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
})

function ConversationThread({ conversation, userId, onBack, onSend }: {
  conversation: ConversationItem
  userId?: string
  onBack: () => void
  onSend?: SendChatMessage
}) {
  const [transcript, setTranscript] = useState<TranscriptItem[] | undefined>(
    userId ? undefined : conversation.transcript,
  )
  const [error, setError] = useState(false)
  const [retry, setRetry] = useState(0)
  const heading = useRef<HTMLHeadingElement>(null)
  const end = useRef<HTMLDivElement>(null)
  const showLatest = useRef(false)

  useEffect(() => {
    if (showLatest.current && transcript !== undefined) {
      showLatest.current = false
      end.current?.scrollIntoView({ block: 'end' })
    }
  }, [transcript])

  useEffect(() => { heading.current?.focus() }, [])
  useEffect(() => {
    if (!userId) return
    const controller = new AbortController()
    void loadConversationTranscript(userId, conversation.id, controller.signal)
      .then((messages) => {
        if (!controller.signal.aborted) { setTranscript(messages); setError(false) }
      })
      .catch(() => { if (!controller.signal.aborted) setError(true) })
    return () => controller.abort()
  }, [userId, conversation.id, conversation.lastActivityAt, retry])

  const messages = transcript ?? conversation.transcript
  return (
    <section className="conversation-thread" aria-label="Conversation log">
      <button className="text-action chat-back" onClick={onBack}>← All chats</button>
      <header className="chat-heading">
        <h1 ref={heading} tabIndex={-1}>{conversation.title}</h1>
        <p className="item-meta">{dateTime(conversation.startedAt)} · {conversation.status === 'active' ? 'Active' : 'Ended'}</p>
      </header>
      {error ? <div className="chat-load-notice" role="alert">
        <span>Couldn’t load the full log. Showing cached messages.</span>
        <button className="text-action" onClick={() => {
          setError(false); setTranscript(undefined); setRetry((value) => value + 1)
        }}>Try again</button>
      </div> : transcript === undefined ? <p className="item-meta" role="status">Loading conversation…</p> : null}
      <ol className="chat-messages" aria-label="Messages, oldest first">
        {messages.map((line) => (
          <li key={line.id} className={`chat-message chat-message--${line.speaker}`}>
            <header>
              <strong>{line.speaker === 'user' ? 'You' : line.speaker === 'assistant' ? 'Eyes' : 'Unknown speaker'}</strong>
              <time dateTime={line.startedAt}>{dateTime(line.startedAt)}</time>
            </header>
            <p>{line.text}</p>
          </li>
        ))}
      </ol>
      {transcript !== undefined && !messages.length ? <p className="item-meta">No messages were saved for this chat.</p> : null}
      <ChatComposer sessionId={conversation.id} onSend={onSend} onSettled={() => {
        showLatest.current = true
        setRetry((value) => value + 1)
      }} />
      <div ref={end} />
    </section>
  )
}

export function ConversationLog({ conversations, userId, onSend }: {
  conversations: ConversationItem[]
  userId?: string
  onSend?: SendChatMessage
}) {
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState<string>()
  const selected = conversations.find((item) => item.id === selectedId)
  const selectedButton = useRef<HTMLButtonElement | null>(null)
  const filtered = useMemo(() => {
    const normalized = query.toLowerCase().trim()
    return conversations.filter((conversation) => !normalized || [
      conversation.title, conversation.summary, ...conversation.transcript.map((line) => line.text),
    ].join(' ').toLowerCase().includes(normalized))
  }, [conversations, query])

  return <>
    <div hidden={Boolean(selected)}>
      <header className="page-intro"><h1>Chats</h1></header>
      <label className="search-field">
        <span>Search conversations</span>
        <input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search chats" />
      </label>
      <p className="result-count">{filtered.length} {filtered.length === 1 ? 'conversation' : 'conversations'}</p>
      <div className="compact-list">
        {filtered.map((conversation) => <button key={conversation.id} className="chat-log-row"
          onClick={(event) => { selectedButton.current = event.currentTarget; setSelectedId(conversation.id) }}>
          <span className="compact-item__title">{conversation.title}</span>
          <time dateTime={conversation.lastActivityAt}>{dateTime(conversation.lastActivityAt)}</time>
          <span className="chat-log-arrow" aria-hidden="true">→</span>
        </button>)}
      </div>
      {!filtered.length ? <p className="item-meta compact-empty">{query ? 'No matches. Try another search.' : 'No conversations yet.'}</p> : null}
    </div>
    {selected ? <ConversationThread key={`${userId ?? 'demo'}:${selected.id}`} conversation={selected} userId={userId} onSend={onSend}
      onBack={() => { setSelectedId(undefined); requestAnimationFrame(() => selectedButton.current?.focus()) }} /> : null}
  </>
}
