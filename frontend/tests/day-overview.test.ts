import assert from 'node:assert/strict'
import { test } from 'node:test'
import { getDayOverview } from '../src/features/workspace/dayOverview.js'
import type { MemoryItem, TaskItem, WatchItem, WorkspaceData } from '../src/features/workspace/workspaceTypes.js'

const at = (day: number, hour = 0, minute = 0) => new Date(2026, 8, day, hour, minute).toISOString()
const now = () => new Date(2026, 8, 5, 12)
const empty = (): WorkspaceData => ({ tasks: [], watches: [], memories: [], conversations: [] })
const task = (id: string, dueAt: string, status: TaskItem['status'] = 'accepted'): TaskItem =>
  ({ id, title: id, dueAt, status, schedule: 'Once', createdAt: at(4) })
const memory = (id: string, createdAt: string, extra: Partial<MemoryItem> = {}): MemoryItem =>
  ({ id, title: id, summary: id, createdAt, updatedAt: createdAt, kind: 'fact', topics: [], status: 'active', ...extra })
const watch = (id: string, expiresAt: string, status: WatchItem['status'] = 'active'): WatchItem =>
  ({ id, query: id, condition: '', expiresAt, status, intervalMinutes: 60, createdAt: at(4), seenCount: 0 })

test('today contains only accepted reminders in the local calendar day, sorted by time', () => {
  const data = empty()
  data.tasks = [task('late', at(5, 23, 59)), task('tomorrow', at(6)), task('yesterday', at(4, 23, 59)),
    task('afternoon', at(5, 14)), task('midnight', at(5)), task('now', at(5, 12)),
    task('suggestion', at(5, 15), 'proposed'), task('declined', at(5, 16), 'rejected'), task('bad date', 'invalid')]
  const originalOrder = data.tasks.map((item) => item.id)
  const day = getDayOverview(data, now())
  assert.deepEqual(day.upcoming.map((item) => item.id), ['afternoon', 'late'])
  assert.deepEqual(day.earlier.map((item) => item.id), ['midnight', 'now'])
  assert.equal(day.nextAfterToday?.id, 'tomorrow')
  assert.equal(day.futureReminderCount, 3)
  assert.equal(day.taskReviews, 1)
  assert.equal(day.heading, '2 reminders ahead.')
  assert.deepEqual(data.tasks.map((item) => item.id), originalOrder)
})

test('no reminders is distinct from elapsed reminders and never claims completion', () => {
  const data = empty()
  assert.equal(getDayOverview(data, now()).heading, 'No reminders today.')
  data.tasks = [task('earlier', at(5, 10))]
  assert.equal(getDayOverview(data, now()).heading, 'No more reminders today.')
  data.tasks.push(task('later', at(5, 18)))
  assert.equal(getDayOverview(data, now()).heading, '1 reminder ahead.')
})

test('unavailable resources do not leak cached items or claim a clear day', () => {
  const data = empty()
  data.tasks = [task('later', at(5, 14)), task('suggestion', at(5, 15), 'proposed')]
  data.memories = [memory('new', at(5, 10))]
  data.watches = [watch('active', at(6)), watch('proposed', at(6), 'proposed')]
  const day = getDayOverview(data, now(), { tasks: 'unavailable', watches: 'unavailable', memories: 'unavailable' })
  assert.equal(day.heading, 'Reminders couldn’t load.')
  assert.deepEqual(day.upcoming, [])
  assert.deepEqual(day.memoriesToday, [])
  assert.deepEqual(day.activeWatches, [])
  assert.equal(day.taskReviews + day.watchReviews + day.futureReminderCount, 0)
})

test('stale reminders remain visible with explicitly uncertain copy', () => {
  const data = empty()
  data.tasks = [task('cached', at(5, 14))]
  const day = getDayOverview(data, now(), { tasks: 'stale' })
  assert.equal(day.upcoming[0]?.id, 'cached')
  assert.equal(day.heading, 'Your day, last saved.')
  assert.match(day.description, /may have changed/)
  assert.equal(getDayOverview(empty(), now(), { tasks: 'stale' }).heading, 'Your day, last saved.')
})

test('remembered today means created today, not an old memory edited today', () => {
  const data = empty()
  data.memories = [memory('morning', at(5, 8)), memory('recent', at(5, 11)),
    memory('edited', at(4), { updatedAt: at(5, 11) }), memory('future', at(5, 16)),
    memory('forgotten', at(5, 10), { status: 'forgotten' }), memory('expired', at(5, 9), { expiresAt: at(5, 12) })]
  assert.deepEqual(getDayOverview(data, now()).memoriesToday.map((item) => item.id), ['recent', 'morning'])
})

test('expired watches are not presented as active background work', () => {
  const data = empty()
  data.watches = [watch('active', at(6)), watch('expired at now', at(5, 12)), watch('old', at(4)),
    watch('rejected', at(6), 'rejected'), watch('unknown expiry', 'invalid')]
  assert.deepEqual(getDayOverview(data, now()).activeWatches.map((item) => item.id), ['active'])
})

test('the day rolls over at midnight without keeping yesterday’s agenda or memories', () => {
  const data = empty()
  data.tasks = [task('yesterday', at(5, 23, 59)), task('today', at(6, 9))]
  data.memories = [memory('yesterday', at(5, 23, 58))]
  const day = getDayOverview(data, new Date(2026, 8, 6))
  assert.deepEqual(day.earlier, [])
  assert.deepEqual(day.upcoming.map((item) => item.id), ['today'])
  assert.deepEqual(day.memoriesToday, [])
  assert.equal(day.period, 'Tonight')
})

test('local-day boundaries work east of UTC and on short and long DST days', () => {
  const previousTimezone = process.env.TZ
  try {
    for (const [timezone, month, date] of [
      ['Asia/Tokyo', 8, 5], ['America/Los_Angeles', 2, 8], ['America/Los_Angeles', 10, 1],
    ] as const) {
      process.env.TZ = timezone
      const data = empty()
      const local = (dayOffset: number, hour: number) => new Date(2026, month, date + dayOffset, hour).toISOString()
      data.tasks = [task('early', local(0, 0)), task('late', local(0, 23)), task('tomorrow', local(1, 0))]
      const day = getDayOverview(data, new Date(2026, month, date, 12))
      assert.deepEqual(day.earlier.map((item) => item.id), ['early'], timezone)
      assert.deepEqual(day.upcoming.map((item) => item.id), ['late'], timezone)
      assert.equal(day.nextAfterToday?.id, 'tomorrow', timezone)
    }
  } finally {
    if (previousTimezone === undefined) delete process.env.TZ
    else process.env.TZ = previousTimezone
  }
})
