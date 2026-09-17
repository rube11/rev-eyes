import type { TaskItem } from './workspaceTypes'

const DAY = 86_400_000

export function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}

export function formatDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
  }).format(new Date(value))
}

export function formatTime(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    hour: 'numeric',
    minute: '2-digit',
  }).format(new Date(value))
}

export function formatShortDate(value: string): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
  }).format(new Date(value))
}

export function relativeTime(value: string, now = Date.now()): string {
  const difference = new Date(value).getTime() - now
  const absolute = Math.abs(difference)
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

  if (absolute >= DAY) {
    return formatter.format(Math.round(difference / DAY), 'day')
  }
  if (absolute >= 3_600_000) {
    return formatter.format(Math.round(difference / 3_600_000), 'hour')
  }
  if (absolute >= 60_000) {
    return formatter.format(Math.round(difference / 60_000), 'minute')
  }
  return 'just now'
}

export function formatInterval(minutes: number): string {
  if (minutes < 60) {
    return `${minutes} min`
  }
  if (minutes % 60 === 0) {
    const hours = minutes / 60
    return `${hours} ${hours === 1 ? 'hour' : 'hours'}`
  }
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

export function shorten(value: string, limit: number): string {
  const text = value.replace(/\s+/gu, ' ').trim()
  return text.length <= limit ? text : `${text.slice(0, limit - 1).trimEnd()}…`
}

function startOfDay(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
}

export function dayLabel(value: string, now: Date): string {
  const target = new Date(value)
  const days = Math.round((startOfDay(target) - startOfDay(now)) / DAY)
  if (days === 0) return 'Today'
  if (days === 1) return 'Tomorrow'
  if (days === -1) return 'Yesterday'
  return new Intl.DateTimeFormat(undefined, {
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    ...(target.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
  }).format(target)
}

export type DayGroup = {
  key: string
  label: string
  items: TaskItem[]
}

export function groupByDay(tasks: TaskItem[], now: Date): DayGroup[] {
  const groups = new Map<string, DayGroup>()
  for (const task of [...tasks].sort((a, b) => Date.parse(a.dueAt) - Date.parse(b.dueAt))) {
    const key = String(startOfDay(new Date(task.dueAt)))
    const group = groups.get(key) ?? { key, label: dayLabel(task.dueAt, now), items: [] }
    group.items.push(task)
    groups.set(key, group)
  }
  return [...groups.values()]
}
