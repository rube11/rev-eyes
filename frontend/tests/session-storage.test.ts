import assert from 'node:assert/strict'
import test from 'node:test'
import { AuthClient } from '@supabase/supabase-js'

import { createSessionStorage, type EvenSessionBridge } from '../src/shared/api/session-storage.js'

function memoryStorage() {
  const data = new Map<string, string>()
  return {
    data,
    getItem: (key: string) => data.get(key) ?? null,
    setItem: (key: string, value: string) => { data.set(key, value) },
    removeItem: (key: string) => { data.delete(key) },
  }
}

function nativeBridge(store = memoryStorage(), uid: number | string = 42): EvenSessionBridge {
  return {
    getUserInfo: async () => ({ uid }),
    getLocalStorage: async (key) => store.getItem(key) ?? '',
    setLocalStorage: async (key, value) => { store.setItem(key, value); return true },
  }
}

function adapter(bridge: EvenSessionBridge | null, browser = memoryStorage(), timeoutMs = 1_000) {
  return createSessionStorage({
    resolveBridge: async () => bridge,
    browserStorage: () => browser,
    timeoutMs,
  })
}

test('web mode preserves the existing browser storage keys', async () => {
  const browser = memoryStorage()
  browser.setItem('session', 'existing-web-session')
  const storage = adapter(null, browser)
  assert.equal(await storage.getItem('session'), 'existing-web-session')
  await storage.setItem('session', 'refreshed')
  assert.equal(browser.getItem('session'), 'refreshed')
  await storage.removeItem('session')
  assert.equal(browser.getItem('session'), null)
})

test('native session survives a new WebView with empty browser storage', async () => {
  const native = nativeBridge()
  const browser = memoryStorage()
  await adapter(native, browser).setItem('session', 'saved-token')
  assert.equal(browser.data.size, 0)
  assert.equal(await adapter(native).getItem('session'), 'saved-token')
})

test('rotated tokens replace the native value, and logout clears it', async () => {
  const native = nativeBridge()
  const storage = adapter(native)
  await storage.setItem('session', 'old-token')
  await storage.setItem('session', 'rotated-token')
  assert.equal(await adapter(native).getItem('session'), 'rotated-token')
  await storage.removeItem('session')
  assert.equal(await adapter(native).getItem('session'), null)
})

test('native empty state never resurrects an old browser session', async () => {
  const browser = memoryStorage()
  browser.setItem('session', 'stale-token')
  const storage = adapter(nativeBridge(), browser)
  assert.equal(await storage.getItem('session'), null)
  await storage.setItem('session', 'new-token')
  await storage.removeItem('session')
  assert.equal(await storage.getItem('session'), null)
})

test('relaunch under another Even UID cannot load the previous account session', async () => {
  const native = memoryStorage()
  await adapter(nativeBridge(native, 42)).setItem('session', 'account-a')
  assert.equal(await adapter(nativeBridge(native, 43)).getItem('session'), null)
  assert.equal(await adapter(nativeBridge(native, 42)).getItem('session'), 'account-a')
})

test('default or invalid Even UID fails closed', async () => {
  for (const uid of [0, -1, NaN, Infinity, 1.5, Number.MAX_SAFE_INTEGER + 1,
    '', '0', '-1', '1.5', 'NaN', 'Infinity', ' 42', '42 ', '042', '+42', '4.2e1',
    '0x2a', '42suffix', 'simulator', '9007199254740992']) {
    await assert.rejects(adapter(nativeBridge(memoryStorage(), uid)).getItem('session'), /storage is unavailable/)
  }
})

test('decimal-string Even IDs use the same session namespace as numeric IDs', async () => {
  const native = memoryStorage()
  await adapter(nativeBridge(native, 42)).setItem('session', 'saved-token')
  const simulator = adapter(nativeBridge(native, '42'))
  assert.equal(await simulator.getItem('session'), 'saved-token')
  await simulator.setItem('session', 'rotated-token')
  assert.equal(await adapter(nativeBridge(native, 42)).getItem('session'), 'rotated-token')
  assert.equal(await adapter(nativeBridge(native, '43')).getItem('session'), null)
  await simulator.removeItem('session')
  assert.equal(await adapter(nativeBridge(native, 42)).getItem('session'), null)
})

test('restoration awaits native bridge readiness', async () => {
  const native = nativeBridge()
  await adapter(native).setItem('session', 'restored')
  let ready!: (bridge: EvenSessionBridge) => void
  const storage = createSessionStorage({
    resolveBridge: () => new Promise((resolve) => { ready = resolve }),
    browserStorage: memoryStorage,
  })
  let finished = false
  const result = storage.getItem('session').then((value) => { finished = true; return value })
  await Promise.resolve()
  assert.equal(finished, false)
  ready(native)
  assert.equal(await result, 'restored')
})

test('read failure is visible and does not fall back to browser credentials', async () => {
  const browser = memoryStorage()
  browser.setItem('session', 'stale-token')
  const native = nativeBridge()
  native.getLocalStorage = async () => { throw new Error('private native details') }
  const storage = adapter(native, browser)
  let notifications = 0
  const unsubscribe = storage.subscribe(() => { notifications += 1 })
  await assert.rejects(storage.getItem('session'), /storage is unavailable/)
  assert.equal(notifications, 1)
  assert.ok(storage.getError())
  assert.doesNotMatch(storage.getError(), /private native details/)
  await assert.rejects(storage.setItem('session', 'replacement'), /storage is unavailable/)
  assert.equal(browser.getItem('session'), 'stale-token')
  unsubscribe()
})

test('false acknowledgements fail both save and logout', async () => {
  for (const operation of ['save', 'logout']) {
    const native = nativeBridge()
    native.setLocalStorage = async () => false
    const storage = adapter(native)
    await assert.rejects(
      operation === 'save' ? storage.setItem('session', 'token') : storage.removeItem('session'),
      /storage is unavailable/,
    )
  }
})

test('timeout is bounded; late bridge readiness cannot start a write', async () => {
  let ready!: (bridge: EvenSessionBridge) => void
  const native = memoryStorage()
  const storage = createSessionStorage({
    resolveBridge: () => new Promise((resolve) => { ready = resolve }),
    browserStorage: memoryStorage,
    timeoutMs: 5,
  })
  await assert.rejects(storage.setItem('session', 'token'), /storage is unavailable/)
  ready(nativeBridge(native))
  await new Promise((resolve) => setTimeout(resolve, 0))
  assert.equal(native.data.size, 0)
  await assert.rejects(storage.getItem('session'), /storage is unavailable/)
})

test('Supabase login, refresh, cold restore and logout use native storage (mock HTTP)', async () => {
  const native = nativeBridge()
  const user = { id: '00000000-0000-0000-0000-000000000042', aud: 'authenticated', email: 'test@example.com' }
  const exp = Math.floor(Date.now() / 1000) + 3600
  const accessToken = [
    Buffer.from(JSON.stringify({ alg: 'HS256', typ: 'JWT' })).toString('base64url'),
    Buffer.from(JSON.stringify({ sub: user.id, exp })).toString('base64url'),
    'fake-signature',
  ].join('.')
  const requests: string[] = []
  const newClient = () => new AuthClient({
    url: 'https://auth.example.test/auth/v1',
    storage: adapter(native),
    persistSession: true,
    autoRefreshToken: false,
    detectSessionInUrl: false,
    fetch: async (input) => {
        const url = String(input)
        requests.push(url)
        if (url.includes('/logout')) return new Response(null, { status: 204 })
        assert.ok(url.includes('/token?grant_type='), `Unexpected request: ${url}`)
        return new Response(JSON.stringify({
          access_token: accessToken,
          refresh_token: url.includes('refresh_token') ? 'rotated-refresh' : 'initial-refresh',
          expires_in: 3600,
          token_type: 'bearer',
          user,
        }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    },
  })

  const first = newClient()
  const signedIn = await first.signInWithPassword({ email: user.email, password: 'fake-test-password' })
  assert.equal(signedIn.error, null)
  assert.equal(signedIn.data.session?.refresh_token, 'initial-refresh')
  const refreshed = await first.refreshSession()
  assert.equal(refreshed.error, null)
  assert.equal(refreshed.data.session?.refresh_token, 'rotated-refresh')

  const reopened = newClient()
  const restored = await reopened.getSession()
  assert.equal(restored.error, null)
  assert.equal(restored.data.session?.refresh_token, 'rotated-refresh')
  assert.equal(requests.length, 2, 'cold restore of an unexpired session needs no login request')

  assert.equal((await reopened.signOut({ scope: 'local' })).error, null)
  assert.equal((await newClient().getSession()).data.session, null)
})
