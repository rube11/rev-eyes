import { useEffect, useRef } from 'react'
import type { FocusEvent, FormEvent } from 'react'

export function SignIn({
  email,
  password,
  error,
  submitting,
  storageUnavailable = false,
  onEmailChange,
  onPasswordChange,
  onSubmit,
}: {
  email: string
  password: string
  error: string
  submitting: boolean
  storageUnavailable?: boolean
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
      <section className="auth-access">
        <form className="auth-form" onSubmit={onSubmit}>
          <header>
            <a className="auth-wordmark" href="#">rev/eyes</a>
            <h2>Sign in</h2>
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
          <button className="auth-submit" type="submit" disabled={submitting || storageUnavailable}>
            <span>{submitting ? 'Signing in…' : 'Sign in'}</span>
          </button>
        </form>
      </section>
    </main>
  )
}
