import { useRef, useState } from 'react'
import type { FormEvent } from 'react'
import './ChatComposer.css'

export type SendChatMessage = (sessionId: string, text: string) => Promise<string>

export function ChatComposer({ sessionId, onSend, onSettled }: {
  sessionId: string
  onSend?: SendChatMessage
  onSettled: () => void
}) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const busy = useRef(false)
  const input = useRef<HTMLTextAreaElement>(null)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    const text = draft.trim()
    if (!text || !onSend || busy.current) return
    busy.current = true
    setSending(true); setError(''); setNotice('')
    try {
      const reply = await onSend(sessionId, text)
      setDraft('')
      setNotice(reply ? 'Reply saved.' : 'Saved. Eyes didn’t need to reply.')
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : 'Couldn’t confirm delivery. Check the log before sending again.')
    } finally {
      busy.current = false; setSending(false); onSettled()
      input.current?.focus()
    }
  }

  return (
    <form className="chat-composer" onSubmit={(event) => void submit(event)}>
      <div className="chat-composer__field">
        <textarea
          ref={input}
          aria-label="Message Eyes"
          value={draft}
          rows={1}
          maxLength={4000}
          disabled={sending || !onSend}
          placeholder={onSend ? 'Message Eyes…' : 'Sign in to message Eyes'}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault(); event.currentTarget.form?.requestSubmit()
            }
          }}
        />
        <button className="primary-action chat-composer__send" disabled={sending || !draft.trim() || !onSend} type="submit">
          {sending ? 'Sending…' : 'Send'}
        </button>
      </div>
      <div className="chat-composer__status">
        {sending ? <p role="status"><span className="chat-composer__thinking" aria-hidden="true" />Eyes is thinking…</p> : null}
        {error ? <p className="form-error" role="alert">{error}</p> : null}
        {notice ? <p role="status">{notice}</p> : null}
        {!sending && !error && !notice ? <p className="chat-composer__hint">Enter to send · Shift+Enter for a new line</p> : null}
      </div>
    </form>
  )
}
