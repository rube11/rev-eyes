import { useEffect, useState } from 'react'

import { HomeView } from '../views/HomeView'
import { ConversationLog } from '../views/ConversationLog'
import { MemoriesView } from '../views/MemoriesView'
import { WatchesView } from '../views/WatchesView'
import { TasksView } from '../views/TasksView'
import { MemoryComposer } from '../components/MemoryComposer'
import { EmptyState } from '../components/EmptyState'
import type { SendChatMessage } from '../components/ChatComposer'
import { workspaceResources } from '../workspaceTypes'
import { getAssistantStatus } from '../assistantStatus'
import type { WorkspaceErrors } from '../workspaceLoad'
import { initialView, isWorkspaceView, viewTitles } from '../navigation'
import { shorten } from '../format'
import type {
  AutomationKind,
  MemoryEdit,
  NewMemoryInput,
  ProposalDecision,
  WorkspaceData,
  WorkspaceView,
} from '../workspaceTypes'
import { Sidebar } from './Sidebar'
import { Topbar } from './Topbar'
import { DataNotice } from './DataNotice'
import './Workspace.css'

type WorkspaceProps = {
  onSendChat?: SendChatMessage
  userId?: string
  data: WorkspaceData
  email: string
  glassesStatus: string
  reconnecting?: boolean
  onReconnectGlasses?: () => void
  resourceErrors?: WorkspaceErrors
  lastSyncedAt?: string
  syncing?: boolean
  onRetry?: () => void
  isDemo: boolean
  onCreateMemory: (input: NewMemoryInput) => Promise<void>
  onEditMemory: (memoryId: string, edit: MemoryEdit) => Promise<void>
  onDeleteAutomation: (
    kind: AutomationKind,
    resourceId: string,
  ) => Promise<void>
  onResolveAutomation: (
    kind: AutomationKind,
    resourceId: string,
    decision: ProposalDecision,
  ) => Promise<void>
  onSignOut: () => void
}

export function Workspace({
  onSendChat,
  userId,
  data,
  email,
  glassesStatus,
  reconnecting = false,
  onReconnectGlasses,
  resourceErrors = {},
  lastSyncedAt,
  syncing = false,
  onRetry,
  isDemo,
  onCreateMemory,
  onEditMemory,
  onDeleteAutomation,
  onResolveAutomation,
  onSignOut,
}: WorkspaceProps) {
  const [view, setView] = useState<WorkspaceView>(initialView)
  const [composerOpen, setComposerOpen] = useState(false)
  const [openConversationId, setOpenConversationId] = useState<string>()
  const [focusMemoryId, setFocusMemoryId] = useState<string>()
  const [now, setNow] = useState(() => new Date())

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  useEffect(() => {
    const handleHashChange = () => {
      const next = window.location.hash.replace(/^#/u, '')
      if (isWorkspaceView(next)) {
        setView(next)
      }
    }
    window.addEventListener('hashchange', handleHashChange)
    return () => window.removeEventListener('hashchange', handleHashChange)
  }, [])

  const navigate = (
    nextView: WorkspaceView,
    target: { conversationId?: string; memoryId?: string } = {},
  ) => {
    setOpenConversationId(target.conversationId)
    setFocusMemoryId(target.memoryId)
    setView(nextView)
    window.history.replaceState(
      null,
      '',
      `${window.location.pathname}${window.location.search}#${nextView}`,
    )
    window.scrollTo({ top: 0, behavior: 'smooth' })
  }

  const pendingCounts: Partial<Record<WorkspaceView, number>> = {
    watches: data.watches.filter((watch) => watch.status === 'proposed').length || undefined,
    tasks: data.tasks.filter((task) => task.status === 'proposed').length || undefined,
  }

  const assistant = getAssistantStatus(glassesStatus, isDemo)
  const connected = assistant.active
  const failedResources = workspaceResources.filter((resource) => resourceErrors[resource])
  const unavailable = view !== 'now' && resourceErrors[view] === 'unavailable'
  const accountLabel = isDemo ? 'Preview mode' : shorten(email, 24)
  const accountInitial = isDemo
    ? 'P'
    : (email.trim().charAt(0).toUpperCase() || 'A')
  const accountAction = isDemo ? 'Exit preview' : 'Sign out'

  return (
    <div className="workspace">
      <Sidebar
        view={view}
        onNavigate={navigate}
        pendingCounts={pendingCounts}
        assistant={assistant}
        isDemo={isDemo}
        accountLabel={accountLabel}
        accountInitial={accountInitial}
        accountAction={accountAction}
        onSignOut={onSignOut}
      />

      <main className="workspace-main">
        <Topbar
          title={viewTitles[view]}
          syncing={syncing}
          lastSyncedAt={lastSyncedAt}
          onRetry={onRetry}
          assistant={assistant}
          isDemo={isDemo}
          reconnecting={reconnecting}
          onReconnectGlasses={onReconnectGlasses}
          accountNote={isDemo ? 'Sample data · changes are not saved' : email}
          accountInitial={accountInitial}
          accountAction={accountAction}
          onSignOut={onSignOut}
        />

        {failedResources.length > 0 ? (
          <DataNotice
            failures={failedResources.map((resource) => ({
              label: viewTitles[resource],
              state: resourceErrors[resource] === 'stale' ? 'stale' : 'unavailable',
            }))}
            syncing={syncing}
            onRetry={onRetry}
          />
        ) : null}

        <div className={`page${view === 'now' ? ' page--home' : ''}`} key={view}>
          {unavailable ? (
            <EmptyState
              title={`${viewTitles[view]} couldn’t load`}
              body="This section is unavailable right now. Use Try again above to reload it. Other sections remain available."
            />
          ) : null}
          {view === 'now' ? (
            <HomeView
              data={data}
              resourceErrors={resourceErrors}
              assistant={assistant}
              isDemo={isDemo}
              reconnecting={reconnecting}
              onReconnectGlasses={connected ? undefined : onReconnectGlasses}
              onNavigate={navigate}
              onAddMemory={() => setComposerOpen(true)}
              currentTime={now}
            />
          ) : null}
          {!unavailable && view === 'conversations' ? (
            <ConversationLog
              conversations={data.conversations}
              memories={data.memories}
              userId={userId}
              onSend={onSendChat}
              initialConversationId={openConversationId}
              onOpenMemory={(memoryId) => navigate('memories', { memoryId })}
            />
          ) : null}
          {!unavailable && view === 'memories' ? (
            <MemoriesView
              data={data}
              currentTime={now}
              initialExpandedId={focusMemoryId}
              onAdd={() => setComposerOpen(true)}
              onEdit={onEditMemory}
              onOpenConversation={(conversationId) => navigate('conversations', { conversationId })}
            />
          ) : null}
          {!unavailable && view === 'watches' ? (
            <WatchesView
              data={data}
              currentTime={now}
              onDelete={(resourceId) => onDeleteAutomation('watch', resourceId)}
              onResolve={(resourceId, decision) => onResolveAutomation('watch', resourceId, decision)}
            />
          ) : null}
          {!unavailable && view === 'tasks' ? (
            <TasksView
              data={data}
              currentTime={now}
              onDelete={(resourceId) => onDeleteAutomation('reminder', resourceId)}
              onResolve={(resourceId, decision) => onResolveAutomation('reminder', resourceId, decision)}
            />
          ) : null}
        </div>
      </main>

      <MemoryComposer
        open={composerOpen}
        onClose={() => setComposerOpen(false)}
        onSave={onCreateMemory}
      />
    </div>
  )
}
