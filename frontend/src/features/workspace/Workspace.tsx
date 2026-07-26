import { useEffect, useMemo, useRef, useState } from 'react'
import type { FormEvent } from 'react'

import type {
  MemoryKind,
  NewMemoryInput,
  TaskItem,
  WatchItem,
  WorkspaceData,
  WorkspaceView,
} from './workspaceTypes'

type WorkspaceProps = {
  data: WorkspaceData
  email: string
  glassesStatus: string
  latestResponse: string
  dataError?: string
  isDemo: boolean
  onCreateMemory: (input: NewMemoryInput) => Promise<void>
  onSignOut: () => void
}

type NavItem = {
  id: WorkspaceView
  label: string
}

const navItems: NavItem[] = [
  { id: 'now', label: 'Home' },
  { id: 'conversations', label: 'Conversations' },
  { id: 'memories', label: 'Memories' },
  { id: 'watches', label: 'Watches' },
  { id: 'tasks', label: 'Tasks' },
]

const viewTitles: Record<WorkspaceView, string> = {
  now: 'Home',
  conversations: 'Conversations',
  memories: 'Memories',
  watches: 'Watches',
  tasks: 'Tasks',
}

const memoryKinds: MemoryKind[] = [
  'fact',
  'preference',
  'relationship',
  'event',
  'goal',
  'instruction',
]

const memoryTopics = [
  'work',
  'personal',
  'friends',
  'family',
  'relationships',
  'health',
  'preferences',
  'goals',
  'places',
  'other',
]

function isWorkspaceView(value: string): value is WorkspaceView {
  return navItems.some((item) => item.id === value)
}

function initialView(): WorkspaceView {
  const hash = window.location.hash.replace(/^#/u, '')
  return isWorkspaceView(hash) ? hash : 'now'
}

function formatClock(value: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  }).format(value)
}

function formatDay(value: Date): string {
  return new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
  }).format(value)
}

function greetingForTime(value: Date): string {
  const hour = value.getHours()
  if (hour < 12) {
    return 'Good morning'
  }
  if (hour < 18) {
    return 'Good afternoon'
  }
  return 'Good evening'
}

function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  }).format(new Date(value))
}

function relativeTime(value: string): string {
  const difference = new Date(value).getTime() - Date.now()
  const absolute = Math.abs(difference)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

  if (absolute >= 86_400_000) {
    return formatter.format(Math.round(difference / 86_400_000), 'day')
  }
  if (absolute >= 3_600_000) {
    return formatter.format(Math.round(difference / 3_600_000), 'hour')
  }
  if (absolute >= 60_000) {
    return formatter.format(Math.round(difference / 60_000), 'minute')
  }
  return 'just now'
}

function formatInterval(minutes: number): string {
  if (minutes < 60) {
    return `${minutes} min`
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60
    return `${hours} ${hours === 1 ? 'hour' : 'hours'}`
  }
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

function shorten(value: string, limit: number): string {
  const text = value.replace(/\s+/gu, ' ').trim()
  return text.length <= limit ? text : `${text.slice(0, limit - 1).trimEnd()}…`
}

function connectedFromStatus(status: string): boolean {
  const normalized = status.toLowerCase().trim()
  return (
    normalized.length > 0 &&
    !['connecting', 'reconnecting', 'disconnected', 'offline'].includes(
      normalized,
    )
  )
}

function friendlyDeviceStatus(status: string): string {
  const normalized = status.toLowerCase()
  if (normalized === 'listening') {
    return 'Listening'
  }
  if (normalized === 'sleeping') {
    return 'Standby'
  }
  if (normalized === 'thinking') {
    return 'Thinking'
  }
  if (normalized === 'starting microphone') {
    return 'Starting microphone'
  }
  if (normalized === 'microphone unavailable') {
    return 'Mic unavailable'
  }
  if (normalized === 'glasses command failed') {
    return 'Needs attention'
  }
  if (normalized === 'connected') {
    return 'Connected'
  }
  if (normalized.includes('connect') && !normalized.includes('disconnect')) {
    return 'Connecting'
  }
  if (normalized === 'disconnected' || normalized === 'offline') {
    return 'Offline'
  }
  return 'Needs attention'
}

function NavIcon({ view }: { view: WorkspaceView }) {
  const common = {
    width: 20,
    height: 20,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 1.7,
    strokeLinecap: 'round' as const,
    strokeLinejoin: 'round' as const,
    'aria-hidden': true,
  }

  if (view === 'now') {
    return (
      <svg {...common}>
        <path d="M3.5 10.5 12 3l8.5 7.5" />
        <path d="M5.5 9.5V21h13V9.5M9.5 21v-6h5v6" />
      </svg>
    )
  }
  if (view === 'conversations') {
    return (
      <svg {...common}>
        <path d="M4 5.5h16v11H9l-5 4v-15Z" />
        <path d="M8 9h8M8 13h5" />
      </svg>
    )
  }
  if (view === 'memories') {
    return (
      <svg {...common}>
        <path d="M12 20.5c4.2-2.7 7-6 7-10.2A4.8 4.8 0 0 0 14.2 5c-1 0-1.8.3-2.2 1-.4-.7-1.2-1-2.2-1A4.8 4.8 0 0 0 5 10.3c0 4.2 2.8 7.5 7 10.2Z" />
      </svg>
    )
  }
  if (view === 'watches') {
    return (
      <svg {...common}>
        <path d="M2.8 12s3.4-5.5 9.2-5.5 9.2 5.5 9.2 5.5-3.4 5.5-9.2 5.5S2.8 12 2.8 12Z" />
        <circle cx="12" cy="12" r="2.4" />
      </svg>
    )
  }
  return (
    <svg {...common}>
      <path d="M7 3.5v3M17 3.5v3M4 9h16" />
      <rect x="4" y="5.5" width="16" height="15" rx="2" />
      <path d="m8.5 14 2 2 4.5-5" />
    </svg>
  )
}

function SearchIcon() {
  return (
    <svg
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      aria-hidden="true"
    >
      <circle cx="10.5" cy="10.5" r="6.5" />
      <path d="m15.5 15.5 5 5" />
    </svg>
  )
}

function StatusMark({ active = false }: { active?: boolean }) {
  return (
    <span
      className={`status-mark${active ? ' status-mark--active' : ''}`}
      aria-hidden="true"
    />
  )
}

function EmptyState({
  title,
  body,
}: {
  title: string
  body: string
}) {
  return (
    <div className="empty-state">
      <span className="empty-state__rule" aria-hidden="true" />
      <h3>{title}</h3>
      <p>{body}</p>
    </div>
  )
}

function PageIntro({
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow: string
  title: string
  description: string
  action?: React.ReactNode
}) {
  return (
    <header className="page-intro">
      <div>
        <p className="section-label">{eyebrow}</p>
        <h1>{title}</h1>
        <p className="page-intro__description">{description}</p>
      </div>
      {action ? <div className="page-intro__action">{action}</div> : null}
    </header>
  )
}

type HomeEvent = {
  id: string
  view: Exclude<WorkspaceView, 'now'>
  label: string
  title: string
  body: string
  searchText: string
  timestamp: string
}

type HomeFilter =
  | 'all'
  | 'attention'
  | 'waiting'
  | 'memories'
  | 'conversations'

const homeFilters: Array<{ id: HomeFilter; label: string }> = [
  { id: 'all', label: 'All context' },
  { id: 'attention', label: 'Needs action' },
  { id: 'waiting', label: 'Watching' },
  { id: 'memories', label: 'Memories' },
  { id: 'conversations', label: 'Conversations' },
]
