import { useEffect, useRef, useState } from 'react'
import { loadConversationTranscript } from '../workspaceData'
import { ChatComposer } from '../components/ChatComposer'
import type { SendChatMessage } from '../components/ChatComposer'
import { ArrowIcon, BackIcon } from '../components/icons'
import { dayLabel, formatDateTime, formatTime } from '../format'
import type { ConversationItem, MemoryItem, Speaker, TranscriptItem } from '../workspaceTypes'
import './ConversationThread.css'

const speakerLabel: Record<Speaker, string> = {
  user: 'You',
  assistant: 'Eyes',
  unknown: 'Unknown',
}

function sameDay(a: string, b: string): boolean {
  const first = new Date(a)
  const second = new Date(b)
  return first.getFullYear() === second.getFullYear()
    && first.getMonth() === second.getMonth()
    && first.getDate() === second.getDate()
}

export function ConversationThread({ conversation, userId, related = [], onBack, onSend, onOpenMemory }: {
  conversation: ConversationItem
  userId?: string
  related?: MemoryItem[]
  onBack: () => void
  onSend?: SendChatMessage
  onOpenMemory?: (memoryId: string) => void
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
  const multiDay = messages.length > 1 && !sameDay(messages[0].startedAt, messages[messages.length - 1].startedAt)
  const now = new Date()

  return (
    <section className="thread" aria-label="Conversation log">
      <button className="text-action text-action--back thread__back" type="button" onClick={onBack}>
        <BackIcon />All chats
      </button>
      <header className="thread__head">
        <h1 ref={heading} tabIndex={-1}>{conversation.title}</h1>
        {conversation.summary ? <p className="thread__lede">{conversation.summary}</p> : null}
        <p className="thread__meta tnum">
          <time dateTime={conversation.startedAt}>{formatDateTime(conversation.startedAt)}</time>
          <span aria-hidden="true">·</span>
          {conversation.status === 'active'
            ? <span className="thread__live"><span className="pill__dot" aria-hidden="true" />Active</span>
            : conversation.status === 'expired' ? 'Expired' : 'Ended'}
        </p>
      </header>
      {related.length ? (
        <section className="thread__kept" aria-label="Remembered from this chat">
          <p className="eyebrow">Eyes remembered</p>
          <ul>
            {related.map((memory) => (
              <li key={memory.id}>
                <button type="button" onClick={() => onOpenMemory?.(memory.id)} disabled={!onOpenMemory}>
                  <span className="thread__kept-kind">{memory.kind}</span>
                  <span className="thread__kept-title">{memory.title}</span>
                  <ArrowIcon />
                </button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}
      {error ? <div className="thread__notice" role="alert">
        <span>Couldn’t load the full log. Showing cached messages.</span>
        <button className="text-action" type="button" onClick={() => {
          setError(false); setTranscript(undefined); setRetry((value) => value + 1)
        }}>Try again</button>
      </div> : transcript === undefined ? <p className="thread__loading" role="status">Loading conversation…</p> : null}
      <ol className="transcript" aria-label="Messages, oldest first">
        {messages.map((line, index) => {
          const previous = messages[index - 1]
          const newDay = multiDay && (!previous || !sameDay(previous.startedAt, line.startedAt))
          return (
            <li key={line.id} className={`turn turn--${line.speaker}${newDay ? ' turn--new-day' : ''}`}>
              {newDay ? <span className="turn__day eyebrow">{dayLabel(line.startedAt, now)}</span> : null}
              <div className="turn__who">
                <span className="turn__speaker">{speakerLabel[line.speaker]}</span>
                <time className="turn__time tnum" dateTime={line.startedAt}>{formatTime(line.startedAt)}</time>
              </div>
              <p className="turn__text">{line.text}</p>
            </li>
          )
        })}
      </ol>
      {transcript !== undefined && !messages.length ? <p className="thread__loading">No messages were saved for this chat.</p> : null}
      <ChatComposer sessionId={conversation.id} onSend={onSend} onSettled={() => {
        showLatest.current = true
        setRetry((value) => value + 1)
      }} />
      <div ref={end} />
    </section>
  )
}
