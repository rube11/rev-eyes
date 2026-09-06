const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const ts = require('typescript')

// Exercise the real runtime, lifecycle and audio controller with fake SDK/socket
// boundaries. No hardware microphone, network requests, or model calls.
async function harness(t) {
  let now = 0
  let eventHandler
  let allowAudio = true
  let audioGate
  let sequence = 0
  const timers = new Map()
  const audio = []
  const controls = []
  const pages = []
  const statuses = []
  const listeners = new Map()
  const socket = {
    readyState: 1,
    addEventListener: (type, fn) => listeners.set(type, fn),
    removeEventListener: (type, fn) => { if (listeners.get(type) === fn) listeners.delete(type) },
    send: data => { if (typeof data === 'string') controls.push(JSON.parse(data).type) },
    close: () => { socket.readyState = 3; listeners.get('close')?.() },
  }
  const bridge = {
    audioControl: async enabled => {
      audio.push(enabled)
      if (enabled && audioGate) await audioGate
      return enabled ? allowAudio : true
    },
    onEvenHubEvent: callback => { eventHandler = callback; return () => { eventHandler = undefined } },
    onAppLocationChanged: () => () => {},
    startAppLocationUpdates: async () => false,
    stopAppLocationUpdates: async () => {},
  }
  const mocks = {
    '@evenrealities/even_hub_sdk': {
      AppLocationAccuracy: { Medium: 1 }, AudioInputSource: { Glasses: 1 },
      OsEventTypeList: { CLICK_EVENT: 0, DOUBLE_CLICK_EVENT: 1 },
    },
    './glasses-page-host': {
      getEvenBridge: async () => bridge, resumeGlassesPage: () => {},
      renderGlassesPage: async page => pages.push(page),
      upgradeTranscriptText: async () => {},
    },
    './glasses-ui': {
      buildCompactPage: label => ({ label }), buildSleepPage: () => ({ label: 'sleep' }),
      buildMessagePage: (message, footer) => ({ message, footer }),
      buildTranscriptContent: text => text,
      buildTranscriptPage: text => ({ transcript: text }),
      presentGlassesMessage: text => ({ kind: 'answer', body: text }),
    },
    '../shared/api/client': { connectRealtimeSocket: async () => socket },
  }
  const context = vm.createContext({
    console, AbortController, Uint8Array, WebSocket: { OPEN: 1 },
    Date: class extends Date { static now() { return now } },
    setTimeout: (fn, delay) => { const id = ++sequence; timers.set(id, { fn, at: now + delay }); return id },
    clearTimeout: id => timers.delete(id),
  })
  const cache = new Map()
  function load(file) {
    if (cache.has(file)) return cache.get(file).exports
    const module = { exports: {} }
    cache.set(file, module)
    const source = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
    }).outputText
    const requireMock = name => {
      if (mocks[name]) return mocks[name]
      if (!name.startsWith('.')) throw Error('Unexpected dependency: ' + name)
      return load(path.resolve(path.dirname(file), name.replace(/\.js$/, '') + '.ts'))
    }
    vm.runInContext('(function(require,module,exports){' + source + '\n})', context, { filename: file })(requireMock, module, module.exports)
    return module.exports
  }
  const flush = async () => { for (let i = 0; i < 12; i++) await new Promise(setImmediate) }
  async function advance(ms) {
    const target = now + ms
    while (true) {
      const next = [...timers].filter(([, task]) => task.at <= target).sort((a, b) => a[1].at - b[1].at)[0]
      if (!next) break
      now = next[1].at
      timers.delete(next[0])
      next[1].fn()
      await flush()
    }
    now = target
    await flush()
  }
  const { initializeEvenExperience } = load(path.resolve(__dirname, '../src/even/runtime.ts'))
  const dispose = await initializeEvenExperience('synthetic-test-token', value => statuses.push(value))
  t.after(dispose)
  await advance(0)
  const message = async (type, extra = {}) => { listeners.get('message')?.({ data: JSON.stringify({ type, ...extra }) }); await flush() }
  const click = async () => { eventHandler?.({ sysEvent: { eventType: 0 } }); await flush() }
  const sleep = async () => { eventHandler?.({ jsonData: { gesture: 'LONG_PRESS' } }); await flush() }
  async function reply() {
    await click()
    await message('user_transcript', { text: 'What time is it?' })
    await message('assistant_thinking')
    await message('assistant_response', { text: 'It is noon.' })
  }
  return { audio, controls, pages, statuses, message, click, sleep, reply, advance, dispose,
    disconnect: async () => { socket.close(); await flush() },
    denyAudio: () => { allowAudio = false },
    delayAudio: () => { let release; audioGate = new Promise(resolve => { release = resolve }); return release },
  }
}

test('a reply restarts the mic after stop acknowledgement and listens for exactly 30 seconds', async t => {
  const h = await harness(t)
  assert.deepEqual(h.audio, [])
  await h.reply()
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 1)
  assert.equal(h.audio.at(-1), false)
  await h.message('listening_stopped')
  assert.equal(h.audio.at(-1), true)
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 2)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  assert.match(h.pages.at(-1).footer, /LISTENING/)
  await h.advance(5_000)
  assert.equal(h.pages.at(-1).label, 'LISTENING  ·  PAUSE TO SEND')
  await h.advance(24_999)
  assert.equal(h.audio.at(-1), true)
  await h.advance(1)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.at(-1), 'listening_stop')
  assert.equal(h.pages.at(-1).label, 'TAP TO TALK')
})

test('speech near expiry is not cut off and the next reply gets a fresh window', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.advance(29_000)
  await h.message('user_transcript', { text: 'And tomorrow' })
  await h.advance(2_000)
  assert.equal(h.audio.at(-1), true)
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'Tomorrow is Monday.' })
  await h.message('listening_stopped')
  await h.advance(29_999)
  assert.equal(h.audio.at(-1), true)
  await h.advance(1)
  assert.equal(h.audio.at(-1), false)
})

test('an acknowledgement before the response also permits hands-free follow-up', async t => {
  const h = await harness(t)
  await h.click()
  await h.message('assistant_thinking')
  await h.message('listening_stopped')
  await h.message('assistant_response', { text: 'Hello.' })
  assert.equal(h.audio.at(-1), true)
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 2)
})

for (const action of ['sleep', 'disconnect', 'dispose']) {
  test(action + ' cancels pending follow-up without reopening the mic', async t => {
    const h = await harness(t)
    await h.reply()
    await h[action]()
    await h.message('listening_stopped')
    await h.advance(30_000)
    assert.equal(h.audio.filter(Boolean).length, 1)
  })
}

test('transcription errors do not reopen a failed stream', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped', { error: 'Transcription unavailable' })
  assert.equal(h.audio.filter(Boolean).length, 1)
  assert.match(h.pages.at(-1).label, /MIC UNAVAILABLE/)
  await h.advance(30_000)
  assert.equal(h.audio.at(-1), false)
})

test('failed microphone restart falls back to an explicit retry', async t => {
  const h = await harness(t)
  await h.reply()
  h.denyAudio()
  await h.message('listening_stopped')
  assert.match(h.pages.at(-1).label, /MIC UNAVAILABLE/)
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 1)
})

test('notifications stop automatic capture rather than leaving an unbounded mic', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.message('notification', { id: 'test-notification', text: 'A reminder.' })
  assert.equal(h.audio.at(-1), false)
  await h.message('listening_stopped')
  await h.advance(30_000)
  assert.equal(h.audio.filter(Boolean).length, 2)
})

test('a tap can still stop follow-up capture immediately', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.click()
  assert.equal(h.audio.at(-1), false)
  await h.message('listening_stopped')
  await h.advance(30_000)
  assert.equal(h.audio.filter(Boolean).length, 2)
})

test('a late stop acknowledgement cannot reopen an expired follow-up window', async t => {
  const h = await harness(t)
  await h.reply()
  await h.advance(30_000)
  await h.message('listening_stopped')
  assert.equal(h.audio.filter(Boolean).length, 1)
  assert.equal(h.pages.at(-1).label, 'TAP TO TALK')
})

test('a microphone start completing after expiry is stopped without opening a stream', async t => {
  const h = await harness(t)
  await h.reply()
  const release = h.delayAudio()
  await h.message('listening_stopped')
  await h.advance(30_000)
  release()
  await h.advance(0)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 1)
  assert.equal(h.pages.at(-1).label, 'TAP TO TALK')
})

test('sleep stops an already-open follow-up stream and cancels its timer', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.sleep()
  assert.equal(h.audio.at(-1), false)
  await h.message('listening_stopped')
  await h.advance(30_000)
  assert.equal(h.audio.filter(Boolean).length, 2)
  assert.equal(h.pages.at(-1).label, 'sleep')
})
