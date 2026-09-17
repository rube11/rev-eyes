import type { WorkspaceView } from './workspaceTypes'

export type NavItem = {
  id: WorkspaceView
  label: string
}

export const navItems: NavItem[] = [
  { id: 'now', label: 'Home' },
  { id: 'conversations', label: 'Chats' },
  { id: 'memories', label: 'Memories' },
  { id: 'watches', label: 'Watches' },
  { id: 'tasks', label: 'Tasks' },
]

export const viewTitles: Record<WorkspaceView, string> = {
  now: 'Home',
  conversations: 'Chats',
  memories: 'Memories',
  watches: 'Watches',
  tasks: 'Tasks',
}

export function isWorkspaceView(value: string): value is WorkspaceView {
  return navItems.some((item) => item.id === value)
}

export function initialView(): WorkspaceView {
  const hash = window.location.hash.replace(/^#/u, '')
  return isWorkspaceView(hash) ? hash : 'now'
}
