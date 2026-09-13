import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import type { FormEvent } from 'react'
import type { Session } from '@supabase/supabase-js'

import { initializeEvenExperience } from '../even/runtime'
import { showEvenMessage } from '../even/glasses-page-host'
import {
  createDemoWorkspaceData,
  deleteWorkspaceAutomation,
  loadWorkspaceData,
  sendChatMessage,
  refreshWorkspaceData,
  resolveWorkspaceProposal,
  saveMemory,
} from '../features/workspace/workspaceData'
import { workspaceResources } from '../features/workspace/workspaceTypes'
import type {
  AutomationKind,
  NewMemoryInput,
  ProposalDecision,
  WorkspaceData,
  WorkspaceResource,
} from '../features/workspace/workspaceTypes'
import { Workspace } from '../features/workspace/Workspace'
import { ConnectionSession } from '../even/connection-session'
import { updateWorkspaceErrors } from '../features/workspace/workspaceLoad'
import type { WorkspaceErrors, WorkspaceLoadResult } from '../features/workspace/workspaceLoad'
import { Homepage } from '../features/landing/Homepage'
import { SignIn } from '../features/auth/SignIn'
import { sessionStorage, supabase } from '../shared/api/supabase'

const isDemoMode = new URLSearchParams(window.location.search).has('demo')
const workspaceRefreshDebounceMs = 100
const workspaceRetryDelaysMs = [2_000, 5_000, 15_000, 30_000]

type WorkspaceRefreshRequester = (
  resources?: readonly WorkspaceResource[],
  full?: boolean,
) => void

function emptyWorkspaceData(): WorkspaceData {
  return {
    conversations: [],
    memories: [],
    watches: [],
    tasks: [],
  }
}

function mergeWorkspaceData(
  fresh: Partial<WorkspaceData>,
  current: WorkspaceData | undefined,
  locallyAddedMemoryIds: Set<string>,
): WorkspaceData {
  const next = { ...(current ?? emptyWorkspaceData()), ...fresh }
  if (fresh.memories === undefined) {
    return next
  }

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
    ? { ...next, memories: [...pendingMemories, ...fresh.memories] }
    : next
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

function App() {
  const storageError = useSyncExternalStore(sessionStorage.subscribe, sessionStorage.getError)
  const [sessionError, setSessionError] = useState('')
  const [session, setSession] = useState<Session | null | undefined>(
    isDemoMode ? null : undefined,
  )
  const [showSignIn, setShowSignIn] = useState(window.location.hash === '#sign-in')

  useEffect(() => {
    const onHashChange = () => setShowSignIn(window.location.hash === '#sign-in')
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [authError, setAuthError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [glassesStatus, setGlassesStatus] = useState(
    isDemoMode ? 'Connected' : 'Connecting',
  )
  const [connectionSession] = useState(() => new ConnectionSession())
  const [connectionRevision, setConnectionRevision] = useState(0)
  const [reconnecting, setReconnecting] = useState(false)
  const reconnectPending = useRef(false)
  const [workspaceData, setWorkspaceData] = useState<WorkspaceData | undefined>(
    () => (isDemoMode ? createDemoWorkspaceData() : undefined),
  )
  const [workspaceOwnerId, setWorkspaceOwnerId] = useState<string | undefined>(
    undefined,
  )
  const [resourceErrors, setResourceErrors] = useState<WorkspaceErrors>({})
  const [lastSyncedAt, setLastSyncedAt] = useState<string>()
  const [syncing, setSyncing] = useState(false)
  const locallyAddedMemoryIds = useRef(new Set<string>())
  const workspaceDataRef = useRef(workspaceData)
  const workspaceOwnerIdRef = useRef<string | undefined>(undefined)
  const sessionUserIdRef = useRef<string | undefined>(undefined)
  const requestWorkspaceRefreshRef = useRef<WorkspaceRefreshRequester>(
    () => undefined,
  )
  const accessToken = session?.access_token
  const visibleWorkspaceData =
    isDemoMode || workspaceOwnerId === session?.user.id
      ? workspaceData
      : undefined

  useEffect(() => {
    workspaceDataRef.current = workspaceData
  }, [workspaceData])

  useEffect(() => {
    if (!workspaceData) {
      return
    }

    const nextExpiry = Math.min(
      ...workspaceData.memories
        .flatMap((memory) =>
          memory.expiresAt ? [Date.parse(memory.expiresAt)] : [],
        )
        .filter(Number.isFinite),
    )
    if (!Number.isFinite(nextExpiry)) {
      return
    }

    const timer = window.setTimeout(
      () =>
        setWorkspaceData((current) => {
          if (!current) {
            return current
          }
          const memories = current.memories.filter(
            (memory) =>
              !memory.expiresAt || Date.parse(memory.expiresAt) > Date.now(),
          )
          return memories.length === current.memories.length
            ? current
            : { ...current, memories }
        }),
      Math.max(0, nextExpiry - Date.now() + 50),
    )
    return () => window.clearTimeout(timer)
  }, [workspaceData])

  useEffect(() => {
    if (isDemoMode) {
      return
    }

    let active = true
    const updateSession = (nextSession: Session | null) => {
      if (nextSession && window.location.hash === '#sign-in') {
        window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}`)
        setShowSignIn(false)
      }
      const nextUserId = nextSession?.user.id
      if (sessionUserIdRef.current !== nextUserId) {
        sessionUserIdRef.current = nextUserId
        workspaceOwnerIdRef.current = undefined
        workspaceDataRef.current = undefined
        locallyAddedMemoryIds.current.clear()
        setWorkspaceOwnerId(undefined)
        setWorkspaceData(undefined)
        setResourceErrors({})
        setLastSyncedAt(undefined)
        setSyncing(false)
      }
      setSession(nextSession)
    }
    // Ignore Supabase's initial event; getSession owns restoration. Later
    // explicit sign-ins must still work if restoration returned an error.
    const { data: { subscription } } = supabase.auth.onAuthStateChange((event, nextSession) => {
      if (active && event !== 'INITIAL_SESSION') {
        setSessionError('')
        updateSession(nextSession)
      }
    })
    void supabase.auth.getSession().then(({ data, error }) => {
      if (active) {
        if (error) {
          setSessionError('Could not restore your sign-in. Check your connection and retry.')
        }
        updateSession(error ? null : data.session)
      }
    }).catch(() => {
      if (active) {
        updateSession(null)
        setSessionError('Could not restore your sign-in. Reopen the app to retry.')
      }
    })
    return () => {
      active = false
      subscription.unsubscribe()
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
    workspaceOwnerIdRef.current = undefined
    workspaceDataRef.current = undefined
    locallyAddedMemoryIds.current.clear()
    let active = true
    let inFlight = false
    let pendingFullRefresh = true
    let retryAttempt = 0
    let timer: number | undefined
    let requestController: AbortController | undefined
    const pendingResources = new Set<WorkspaceResource>()
    const loadedResources = new Set<WorkspaceResource>()

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
      if (!active || inFlight) {
        return
      }

      const fullRefresh =
        pendingFullRefresh || workspaceDataRef.current === undefined
      const resources = [...pendingResources]
      pendingFullRefresh = false
      pendingResources.clear()
      if (!fullRefresh && resources.length === 0) {
        return
      }

      inFlight = true
      setSyncing(true)
      const controller = new AbortController()
      requestController = controller
      let retryDelay: number | undefined

      try {
        const current = workspaceDataRef.current
        let result: WorkspaceLoadResult
        try {
          result = fullRefresh || !current
            ? await loadWorkspaceData(userId, controller.signal)
            : await refreshWorkspaceData(userId, current, resources, controller.signal)
        } catch {
          result = {
            data: {},
            failedResources: fullRefresh ? [...workspaceResources] : resources,
          }
        }
        if (!active || controller.signal.aborted) {
          return
        }

        const succeeded = Object.keys(result.data) as WorkspaceResource[]
        for (const resource of succeeded) loadedResources.add(resource)
        const loadedSnapshot = new Set(loadedResources)
        setResourceErrors((errors) => updateWorkspaceErrors(errors, result, loadedSnapshot))
        if (succeeded.length > 0) setLastSyncedAt(new Date().toISOString())

        const ownsCurrentData = workspaceOwnerIdRef.current === userId
        workspaceOwnerIdRef.current = userId
        setWorkspaceOwnerId(userId)
        setWorkspaceData((current) => {
          const next = mergeWorkspaceData(
            result.data,
            ownsCurrentData ? current : undefined,
            locallyAddedMemoryIds.current,
          )
          workspaceDataRef.current = next
          return next
        })

        if (result.failedResources.length > 0) {
          for (const resource of result.failedResources) pendingResources.add(resource)
          retryDelay = workspaceRetryDelaysMs[
            Math.min(retryAttempt, workspaceRetryDelaysMs.length - 1)
          ]
          retryAttempt += 1
        } else {
          retryAttempt = 0
        }
      } finally {
        if (requestController === controller) {
          requestController = undefined
        }
        inFlight = false
        if (active) setSyncing(false)
        if (
          active &&
          (retryDelay !== undefined ||
            pendingFullRefresh ||
            pendingResources.size > 0)
        ) {
          scheduleRefresh(retryDelay ?? workspaceRefreshDebounceMs)
        }
      }
    }

    const requestRefresh: WorkspaceRefreshRequester = (
      resources = [],
      full = false,
    ) => {
      if (!active) {
        return
      }
      pendingFullRefresh ||= full
      for (const resource of resources) {
        pendingResources.add(resource)
      }
      if (!inFlight) {
        scheduleRefresh(workspaceRefreshDebounceMs)
      }
    }
    requestWorkspaceRefreshRef.current = requestRefresh

    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        requestRefresh([], true)
      }
    }

    document.addEventListener('visibilitychange', handleVisibilityChange)
    requestRefresh([], true)

    return () => {
      active = false
      clearTimer()
      requestController?.abort()
      document.removeEventListener('visibilitychange', handleVisibilityChange)
      if (requestWorkspaceRefreshRef.current === requestRefresh) {
        requestWorkspaceRefreshRef.current = () => undefined
      }
    }
  }, [session?.user.id])

  useEffect(() => {
    if (isDemoMode || !accessToken) {
      return
    }

    let disposed = false
    void connectionSession.start(() => initializeEvenExperience(
      accessToken,
      (nextStatus) => {
        if (!disposed) {
          setGlassesStatus(nextStatus)
        }
      },
      (resources) => {
        if (!disposed) {
          requestWorkspaceRefreshRef.current(resources)
        }
      },
      () => {
        if (!disposed) {
          requestWorkspaceRefreshRef.current(workspaceResources)
        }
      },
    ))
      .catch(() => {
        if (!disposed) {
          setGlassesStatus('Offline')
        }
      })
      .finally(() => {
        if (!disposed) {
          reconnectPending.current = false
          setReconnecting(false)
        }
      })

    return () => {
      disposed = true
      void connectionSession.stop().catch(() => undefined)
    }
  }, [accessToken, connectionRevision, connectionSession])

  const reconnectGlasses = () => {
    if (isDemoMode || !accessToken || reconnectPending.current) return
    reconnectPending.current = true
    setReconnecting(true)
    setGlassesStatus('Reconnecting')
    setConnectionRevision((revision) => revision + 1)
  }

  const signIn = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setAuthError('')
    setSubmitting(true)
    try {
      const { error } = await supabase.auth.signInWithPassword({ email, password })
      if (error) {
        setAuthError('We could not sign you in. Check your email and password and try again.')
        return
      }
      setSessionError('')
      setPassword('')
      setGlassesStatus('Connecting')
    } catch {
      setAuthError(
        'Could not finish saving your sign-in. Reopen the app and try again.',
      )
    } finally {
      setSubmitting(false)
    }
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
  }

  const resolveAutomation = async (
    kind: AutomationKind,
    resourceId: string,
    decision: ProposalDecision,
  ) => {
    if (!isDemoMode) {
      if (!session?.access_token) {
        throw new Error('Please sign in again to update this item.')
      }
      await resolveWorkspaceProposal(
        session.access_token,
        kind,
        resourceId,
        decision,
      )
    }

    const resolvedAt = new Date().toISOString()
    setWorkspaceData((current) => {
      if (!current) {
        return current
      }
      if (kind === 'reminder') {
        return {
          ...current,
          tasks: current.tasks.map((task) =>
            task.id === resourceId
              ? { ...task, status: decision, resolvedAt }
              : task,
          ),
        }
      }
      return {
        ...current,
        watches: current.watches.map((watch) =>
          watch.id === resourceId
            ? {
                ...watch,
                status: decision === 'accepted' ? 'active' : 'rejected',
                nextCheckAt:
                  decision === 'accepted' ? resolvedAt : undefined,
              }
            : watch,
        ),
      }
    })
    if (!isDemoMode) {
      requestWorkspaceRefreshRef.current([
        kind === 'reminder' ? 'tasks' : 'watches',
      ])
    }
  }

  const deleteAutomation = async (
    kind: AutomationKind,
    resourceId: string,
  ) => {
    if (!isDemoMode) {
      if (!session?.access_token) {
        throw new Error('Please sign in again to delete this item.')
      }
      await deleteWorkspaceAutomation(session.access_token, kind, resourceId)
    }

    setWorkspaceData((current) =>
      current
        ? {
            ...current,
            tasks:
              kind === 'reminder'
                ? current.tasks.filter((task) => task.id !== resourceId)
                : current.tasks,
            watches:
              kind === 'watch'
                ? current.watches.filter((watch) => watch.id !== resourceId)
                : current.watches,
          }
        : current,
    )
    if (!isDemoMode) {
      requestWorkspaceRefreshRef.current([
        kind === 'reminder' ? 'tasks' : 'watches',
      ])
    }
  }

  const signOut = () => {
    if (isDemoMode) {
      const url = new URL(window.location.href)
      url.searchParams.delete('demo')
      url.hash = ''
      window.location.assign(url.toString())
      return
    }
    void supabase.auth.signOut().then(({ error }) => {
      if (error) setSessionError('Could not sign out. Check your connection and retry.')
    }).catch(() => {
      setSessionError('Could not clear your saved sign-in. Reopen the app to retry.')
    })
  }

  if (isDemoMode) {
    return visibleWorkspaceData ? (
      <Workspace
        data={visibleWorkspaceData}
        email="demo@rev-eyes.com"
        glassesStatus={glassesStatus}
        isDemo
        onCreateMemory={createMemory}
        onDeleteAutomation={deleteAutomation}
        onResolveAutomation={resolveAutomation}
        onSignOut={signOut}
      />
    ) : (
      <LoadingScreen />
    )
  }

  // Storage failure must never expose a workspace or block the public homepage.
  if (!storageError && !sessionError && session === undefined) {
    return <LoadingScreen label="Checking your account" />
  }

  if (storageError || !session) {
    if (!showSignIn) return <Homepage />
    return (
      <SignIn
        email={email}
        password={password}
        error={storageError || authError || sessionError}
        submitting={submitting}
        storageUnavailable={Boolean(storageError)}
        onEmailChange={setEmail}
        onPasswordChange={setPassword}
        onSubmit={signIn}
      />
    )
  }

  if (sessionError) {
    return (
      <main className="boot-screen">
        <span className="boot-screen__brand">rev/eyes</span>
        <p role="alert">{sessionError}</p>
        <button className="auth-submit" onClick={() => window.location.reload()}>Reload app</button>
      </main>
    )
  }

  if (!visibleWorkspaceData) {
    return <LoadingScreen />
  }

  return (
    <Workspace
      data={visibleWorkspaceData}
      email={session.user.email ?? 'Account'}
      userId={session.user.id}
      onSendChat={async (sessionId, text) => {
        try { return await sendChatMessage(session.access_token, sessionId, text) }
        finally { requestWorkspaceRefreshRef.current(workspaceResources) }
      }}
      glassesStatus={glassesStatus}
      resourceErrors={resourceErrors}
      reconnecting={reconnecting}
      onReconnectGlasses={reconnectGlasses}
      lastSyncedAt={lastSyncedAt}
      syncing={syncing}
      onRetry={() => requestWorkspaceRefreshRef.current([], true)}
      isDemo={false}
      onCreateMemory={createMemory}
      onDeleteAutomation={deleteAutomation}
      onResolveAutomation={resolveAutomation}
      onSignOut={signOut}
    />
  )
}

export default App
