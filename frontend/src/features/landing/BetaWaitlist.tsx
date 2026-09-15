import { useState, type FormEvent } from 'react'
import './join.css'
import { supabase } from '../../shared/api/supabase'

export function BetaWaitlist() {
  const [status, setStatus] = useState<'idle' | 'pending' | 'success' | 'error'>('idle')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (status === 'pending') return
    const data = new FormData(event.currentTarget)
    setStatus('pending')
    try {
      const { error } = await supabase.rpc('join_beta_waitlist', {
        email_address: String(data.get('email') ?? '').trim().toLowerCase(),
        website: String(data.get('website') ?? ''),
      })
      setStatus(error ? 'error' : 'success')
    } catch {
      setStatus('error')
    }
  }

  return (
    <main className="re-join">
      <header className="re-join-nav">
        <a className="re-join-brand" href="/" aria-label="rev eyes home">rev<span>eyes</span></a>
        <a href="/">Back to overview <span aria-hidden="true">↗</span></a>
      </header>
      <section className="re-join-content" aria-labelledby="join-title">
        <div className="re-join-form-area">
          {status === 'success' ? <div role="status"><h1 id="join-title">You’re on the list.</h1><p>We’ll email you when your invite is ready.</p><a className="re-join-back" href="/">Back to rev eyes ↗</a></div> : (
            <form onSubmit={submit}>
              <h1 id="join-title">Join the private beta.</h1>
              <label htmlFor="beta-email">Your email</label>
              <input id="beta-email" name="email" type="email" autoComplete="email" maxLength={254} required placeholder="you@example.com" />
              <div className="re-join-trap" aria-hidden="true"><label>Website<input name="website" tabIndex={-1} autoComplete="off" /></label></div>
              <button className="re-join-submit" disabled={status === 'pending'}>{status === 'pending' ? 'Requesting access…' : 'Request access'}<span aria-hidden="true">↗</span></button>
              {status === 'error' && <p role="alert">We couldn’t save your request. Please try again in a moment.</p>}
            </form>
          )}
        </div>
      </section>
    </main>
  )
}
