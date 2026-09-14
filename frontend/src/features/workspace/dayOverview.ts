import type { WorkspaceErrors } from './workspaceLoad.js'
import type { WorkspaceData } from './workspaceTypes.js'

export function getDayOverview(data: WorkspaceData, currentTime: Date, errors: WorkspaceErrors = {}) {
  // Calendar boundaries, not a rolling 24 hours: this also follows local DST changes.
  const start = new Date(currentTime)
  start.setHours(0, 0, 0, 0)
  const end = new Date(start)
  end.setDate(end.getDate() + 1)
  const now = currentTime.getTime()
  const isToday = (value: string) => Date.parse(value) >= start.getTime() && Date.parse(value) < end.getTime()
  const reminders = errors.tasks === 'unavailable' ? [] : data.tasks
    .filter((task) => task.status === 'accepted' && Number.isFinite(Date.parse(task.dueAt)))
    .sort((a, b) => Date.parse(a.dueAt) - Date.parse(b.dueAt))
  const today = reminders.filter((task) => isToday(task.dueAt))
  const upcoming = today.filter((task) => Date.parse(task.dueAt) > now)
  const earlier = today.filter((task) => Date.parse(task.dueAt) <= now)
  const activeWatches = errors.watches === 'unavailable' ? [] : data.watches
    .filter((watch) => watch.status === 'active' && Date.parse(watch.expiresAt) > now)
  const memoriesToday = errors.memories === 'unavailable' ? [] : data.memories
    .filter((memory) => memory.status === 'active' && isToday(memory.createdAt)
      && Date.parse(memory.createdAt) <= now && (!memory.expiresAt || Date.parse(memory.expiresAt) > now))
    .sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
  const hour = currentTime.getHours()

  return {
    upcoming,
    earlier,
    activeWatches,
    memoriesToday,
    nextAfterToday: reminders.find((task) => Date.parse(task.dueAt) >= end.getTime()),
    futureReminderCount: reminders.filter((task) => Date.parse(task.dueAt) > now).length,
    taskReviews: errors.tasks === 'unavailable' ? 0 : data.tasks.filter((task) => task.status === 'proposed').length,
    watchReviews: errors.watches === 'unavailable' ? 0 : data.watches.filter((watch) => watch.status === 'proposed').length,
    period: hour < 5 || hour >= 22 ? 'Tonight' : hour < 12 ? 'This morning' : hour < 18 ? 'This afternoon' : 'This evening',
    heading: errors.tasks === 'unavailable' ? 'Reminders couldn’t load.'
      : errors.tasks === 'stale' ? 'Your day, last saved.'
      : upcoming.length ? `${upcoming.length} ${upcoming.length === 1 ? 'reminder' : 'reminders'} ahead.`
      : earlier.length ? 'No more reminders today.' : 'No reminders today.',
    description: errors.tasks === 'unavailable' ? 'Use Try again above to reload them.'
      : errors.tasks === 'stale' ? 'Showing saved reminders. Today may have changed.'
      : upcoming.length ? '' : 'Anything you schedule with Eyes will appear here.',
  }
}
