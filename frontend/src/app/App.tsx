import { useEffect, useRef, useState } from 'react'
import type { FocusEvent, FormEvent } from 'react'
import type { Session } from '@supabase/supabase-js'

import {
  initializeEvenExperience,
  showEvenMessage,
} from '../even/runtime'
import {
  createDemoWorkspaceData,
  loadWorkspaceData,
  saveMemory,
} from '../features/workspace/workspaceData'
import type {
  NewMemoryInput,
  WorkspaceData,
} from '../features/workspace/workspaceTypes'
import { Workspace } from '../features/workspace/Workspace'
import { supabase } from '../shared/api/supabase'

const isDemoMode = new URLSearchParams(window.location.search).has('demo')
const workspaceRefreshIntervalMs = 30_000
const workspaceRetryDelaysMs = [2_000, 5_000, 15_000, 30_000]

function emptyWorkspaceData(): WorkspaceData {
  return {
    conversations: [],
    memories: [],
    watches: [],
    tasks: [],
  }
}

function mergeWorkspaceData(
  fresh: WorkspaceData,
  current: WorkspaceData | undefined,
  locallyAddedMemoryIds: Set<string>,
): WorkspaceData {
  const refreshedMemoryIds = new Set(fresh.memories.map((memory) => memory.id))
  for (const id of locallyAddedMemoryIds) {
    if (refreshedMemoryIds.has(id)) {
      locallyAddedMemoryIds.delete(id)
    }
  }

  const pendingMemories =
    current?.memories.filter(
      (memory) =>
        locallyAddedMemoryIds.has(memory.id) &&
        !refreshedMemoryIds.has(memory.id),
    ) ?? []

  return pendingMemories.length > 0
    ? { ...fresh, memories: [...pendingMemories, ...fresh.memories] }
    : fresh
}

function LoadingScreen({ label = 'Opening your assistant' }: { label?: string }) {
  return (
    <main className="boot-screen">
      <span className="boot-screen__brand">rev/eyes</span>
      <div className="boot-screen__status">
        <span className="status-mark status-mark--active" />
        <span>{label}</span>
      </div>
    </main>
  )
}

function SignIn({
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

function App() {
  const [session, setSession] = useState<Session | null | undefined>(
    isDemoMode ? null : undefined,
  )
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [authError, setAuthError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [glassesStatus, setGlassesStatus] = useState(
    isDemoMode ? 'Connected' : 'Connecting',
  )
  const [latestResponse, setLatestResponse] = useState('')
  const [workspaceData, setWorkspaceData] = useState<WorkspaceData | undefined>(
    () => (isDemoMode ? createDemoWorkspaceData() : undefined),
  )
  const [dataError, setDataError] = useState('')
  const [workspaceRefreshRequest, setWorkspaceRefreshRequest] = useState(0)
  const locallyAddedMemoryIds = useRef(new Set<string>())
  const accessToken = session?.access_token

  useEffect(() => {
    if (isDemoMode) {
      return
    }

    let active = true
    void supabase.auth.getSession().then(({ data }) => {
      if (active) {
        setSession(data.session)
      }
    })
    const { data } = supabase.auth.onAuthStateChange((_event, nextSession) => {
      if (active) {
        setSession(nextSession)
        if (!nextSession) {
          locallyAddedMemoryIds.current.clear()
          setWorkspaceData(undefined)
        }
      }
    })
    return () => {
      active = false
      data.subscription.unsubscribe()
    }
  }, [])

  useEffect(() => {
    if (isDemoMode) {
      return
    }
    if (session === null) {
      void showEvenMessage('Sign in to rev-eyes').catch(() => undefined)
    }
  }, [session])

  useEffect(() => {
    if (isDemoMode || !session?.user.id) {
      return
    }

    const userId = session.user.id
    let active = true
    let inFlight = false
    let refreshRequested = false
    let retryAttempt = 0
    let timer: number | undefined
    let requestController: AbortController | undefined

    const clearTimer = () => {
      if (timer !== undefined) {
        window.clearTimeout(timer)
        timer = undefined
      }
    }

    const scheduleRefresh = (delay: number) => {
      if (!active) {
        return
      }
      clearTimer()
      timer = window.setTimeout(() => {
        timer = undefined
        void refresh()
      }, delay)
    }

    const refresh = async () => {
      if (!active) {
        return
      }
      if (inFlight) {
        refreshRequested = true
        return
      }

      inFlight = true
      const controller = new AbortController()
      requestController = controller
      let nextDelay = workspaceRefreshIntervalMs

      try {
        const data = await loadWorkspaceData(userId, controller.signal)
        if (!active || controller.signal.aborted) {
          return
        }
        retryAttempt = 0
        setDataError('')
        setWorkspaceData((current) =>
          mergeWorkspaceData(data, current, locallyAddedMemoryIds.current),
        )
      } catch {
        if (!active || controller.signal.aborted) {
          return
        }
        setDataError('refresh-unavailable')
        setWorkspaceData((current) => current ?? emptyWorkspaceData())
        nextDelay =
          workspaceRetryDelaysMs[
            Math.min(retryAttempt, workspaceRetryDelaysMs.length - 1)
          ]
        retryAttempt += 1
      } finally {
        if (requestController === controller) {
          requestController = undefined
        }
        inFlight = false
        if (active) {
          if (refreshRequested) {
            refreshRequested = false
            scheduleRefresh(0)
          } else {
            scheduleRefresh(nextDelay)
          }
        }
      }
    }

    const requestRefresh = () => {
      if (!active) {
        return
      }
      clearTimer()
      if (inFlight) {
        refreshRequested = true
        return
      }
      void refresh()
    }

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        requestRefresh()
      }
    }

    window.addEventListener('focus', requestRefresh)
    document.addEventListener('visibilitychange', handleVisibilityChange)
    requestRefresh()

    return () => {
      active = false
      clearTimer()
      requestController?.abort()
      window.removeEventListener('focus', requestRefresh)
      document.removeEventListener('visibilitychange', handleVisibilityChange)
    }
  }, [session?.user.id, workspaceRefreshRequest])

  useEffect(() => {
    if (isDemoMode || !accessToken) {
      return
    }

    let disposed = false
    let stop: (() => void) | undefined
    initializeEvenExperience(
      accessToken,
      (text) => {
        if (!disposed) {
          setLatestResponse(text)
          setWorkspaceRefreshRequest((current) => current + 1)
        }
      },
      (nextStatus) => {
        if (!disposed) {
          setGlassesStatus(nextStatus)
        }
      },
    )
      .then((cleanup) => {
        if (disposed) {
          cleanup()
          return
        }
        stop = cleanup
      })
      .catch(() => {
        if (!disposed) {
          setGlassesStatus('Offline')
        }
      })

    return () => {
      disposed = true
      stop?.()
    }
  }, [accessToken])

  const signIn = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setAuthError('')
    setSubmitting(true)
    const { error } = await supabase.auth.signInWithPassword({
      email,
      password,
    })
    setSubmitting(false)
    if (error) {
      setAuthError(
        'We could not sign you in. Check your email and password and try again.',
      )
      return
    }
    setPassword('')
    setGlassesStatus('Connecting')
  }

  const createMemory = async (input: NewMemoryInput) => {
    if (isDemoMode) {
      const timestamp = new Date().toISOString()
      setWorkspaceData((current) => {
        if (!current) {
          return current
        }
        return {
          ...current,
          memories: [
            {
              id: crypto.randomUUID(),
              title: input.title.trim(),
              summary: input.summary.trim(),
              topics: [input.topic],
              kind: input.kind,
              status: 'active',
              createdAt: timestamp,
              updatedAt: timestamp,
            },
            ...current.memories,
          ],
        }
      })
      return
    }

    if (!session?.user.id) {
      throw new Error('Please sign in again to save this memory.')
    }
    const memory = await saveMemory(session.user.id, input)
    locallyAddedMemoryIds.current.add(memory.id)
    setWorkspaceData((current) =>
      current
        ? {
            ...current,
            memories: [
              memory,
              ...current.memories.filter((item) => item.id !== memory.id),
            ],
          }
        : current,
    )
    setWorkspaceRefreshRequest((current) => current + 1)
  }

  const signOut = () => {
    if (isDemoMode) {
      const url = new URL(window.location.href)
      url.searchParams.delete('demo')
      url.hash = ''
      window.location.assign(url.toString())
      return
    }
    void supabase.auth.signOut()
  }

  if (isDemoMode) {
    return workspaceData ? (
      <Workspace
        data={workspaceData}
        email="demo@rev-eyes.com"
        glassesStatus={glassesStatus}
        latestResponse={latestResponse}
        isDemo
        onCreateMemory={createMemory}
        onSignOut={signOut}
      />
    ) : (
      <LoadingScreen />
    )
  }

  if (session === undefined) {
    return <LoadingScreen label="Checking your account" />
  }

  if (!session) {
    return (
      <SignIn
        email={email}
        password={password}
        error={authError}
        submitting={submitting}
        onEmailChange={setEmail}
        onPasswordChange={setPassword}
        onSubmit={signIn}
      />
    )
  }

  if (!workspaceData) {
    return <LoadingScreen />
  }

  return (
    <Workspace
      data={workspaceData}
      email={session.user.email ?? 'Account'}
      glassesStatus={glassesStatus}
      latestResponse={latestResponse}
      dataError={dataError}
      isDemo={false}
      onCreateMemory={createMemory}
      onSignOut={signOut}
    />
  )
}

export default App
