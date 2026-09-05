import { useEffect, useRef } from 'react'
import type { FocusEvent, FormEvent } from 'react'

export function SignIn({
  email,
  password,
  error,
  submitting,
  onEmailChange,
  onPasswordChange,
  onSubmit,
}: {
  email: string
  password: string
  error: string
  submitting: boolean
  onEmailChange: (value: string) => void
  onPasswordChange: (value: string) => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) {
  const authRef = useRef<HTMLElement>(null)
  const focusRevealTimer = useRef<number | undefined>(undefined)

  useEffect(() => {
    const viewport = window.visualViewport
    let animationFrame: number | undefined

    const keepFocusedFieldVisible = () => {
      if (animationFrame !== undefined) {
        window.cancelAnimationFrame(animationFrame)
      }
      animationFrame = window.requestAnimationFrame(() => {
        animationFrame = undefined
        const focused = document.activeElement
        if (
          focused instanceof HTMLInputElement &&
          authRef.current?.contains(focused)
        ) {
          focused.scrollIntoView({ block: 'center', inline: 'nearest' })
        }
      })
    }

    viewport?.addEventListener('resize', keepFocusedFieldVisible)
    return () => {
      viewport?.removeEventListener('resize', keepFocusedFieldVisible)
      if (animationFrame !== undefined) {
        window.cancelAnimationFrame(animationFrame)
      }
      if (focusRevealTimer.current !== undefined) {
        window.clearTimeout(focusRevealTimer.current)
      }
    }
  }, [])

  const handleFieldFocus = (event: FocusEvent<HTMLInputElement>) => {
    const field = event.currentTarget
    if (focusRevealTimer.current !== undefined) {
      window.clearTimeout(focusRevealTimer.current)
    }
    focusRevealTimer.current = window.setTimeout(() => {
      focusRevealTimer.current = undefined
      if (document.activeElement === field) {
        field.scrollIntoView({ block: 'center', inline: 'nearest' })
      }
    }, 250)
  }

  return (
    <main className="auth" ref={authRef}>
      <section className="auth-brand">
        <div className="auth-brand__top">
          <span className="auth-wordmark">rev/eyes</span>
          <span className="auth-edition">Wearable assistant</span>
        </div>
        <div className="auth-brand__statement">
          <h1>Your assistant, in one place.</h1>
          <p>
            Revisit conversations, manage memories, and see what is coming up.
          </p>
        </div>
        <div className="auth-brand__status">
          <span>Designed for Even G2</span>
        </div>
      </section>

      <section className="auth-access">
        <form className="auth-form" onSubmit={onSubmit}>
          <header>
            <p className="section-label">Your account</p>
            <h2>Welcome back</h2>
            <p>Sign in to open your assistant.</p>
          </header>
          <label className="field">
            <span>Email</span>
            <input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => onEmailChange(event.target.value)}
              onFocus={handleFieldFocus}
              placeholder="you@example.com"
              required
            />
          </label>
          <label className="field">
            <span>Password</span>
            <input
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => onPasswordChange(event.target.value)}
              onFocus={handleFieldFocus}
              placeholder="••••••••••••"
              required
            />
          </label>
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <button className="auth-submit" type="submit" disabled={submitting}>
            <span>{submitting ? 'Signing in…' : 'Sign in'}</span>
            <span aria-hidden="true">↗</span>
          </button>
          <footer>
            <span>Private to your account</span>
            <span>REV/EYES 2026</span>
          </footer>
        </form>
      </section>
    </main>
  )
}
