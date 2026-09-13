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
  const upgrades = []
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
      OsEventTypeList: { CLICK_EVENT: 0, SCROLL_TOP_EVENT: 1, SCROLL_BOTTOM_EVENT: 2, DOUBLE_CLICK_EVENT: 3 },
    },
    './glasses-page-host': {
      getEvenBridge: async () => bridge, resumeGlassesPage: () => {},
      renderGlassesPage: async page => pages.push(page),
      upgradeTranscriptText: async text => { upgrades.push(text); pages.at(-1).transcript = text; return true },
      upgradeMessageStatus: async footer => { upgrades.push(footer); pages.at(-1).footer = footer; return true },
    },
    './glasses-ui': {
      buildCompactPage: label => ({ label }), buildSleepPage: () => ({ label: 'sleep' }),
      buildMessagePage: (message, footer, pageIndex = 0) => ({ message, footer, pageIndex }),
      buildMessageStatus: (_message, footer) => footer,
      glassesMessagePages: message => message.body.match(/.{1,100}/g) ?? [' '],
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
  const scroll = async direction => { eventHandler?.({ textEvent: { eventType: direction > 0 ? 2 : 1 } }); await flush() }
  async function reply() {
    await click()
    await message('user_transcript', { text: 'What time is it?' })
    await message('assistant_thinking')
    await message('assistant_response', { text: 'It is noon.' })
  }
  return { audio, controls, pages, statuses, upgrades, message, click, sleep, scroll, reply, advance, dispose,
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
  assert.match(h.pages.at(-1).footer, /Listening/)
  await h.advance(5_000)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  await h.advance(24_999)
  assert.equal(h.audio.at(-1), true)
  await h.advance(1)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.at(-1), 'listening_stop')
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  assert.equal(h.pages.at(-1).footer, 'Tap to talk')
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
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
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
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
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

test('paging preserves the mic deadline and reading position after expiry', async t => {
  const h = await harness(t)
  await h.click()
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'Long complete answer. '.repeat(40) })
  const answerPageCount = h.pages.length
  await h.message('listening_stopped')
  assert.equal(h.pages.length, answerPageCount, 'mic status must not rebuild the answer')
  await h.scroll(1)
  assert.equal(h.pages.at(-1).pageIndex, 1)
  await h.advance(30_000)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.pages.at(-1).pageIndex, 1)
  await h.scroll(1)
  assert.equal(h.pages.at(-1).pageIndex, 2)
  await h.scroll(-1)
  assert.equal(h.pages.at(-1).pageIndex, 1)
  assert.equal(h.controls.filter(x => x === 'listening_start').length, 2)
})

test('transcript bursts coalesce and duplicates do not redraw', async t => {
  const h = await harness(t)
  await h.click()
  const count = h.pages.length
  await h.message('user_transcript', { text: 'One' })
  await h.message('user_transcript', { text: 'One two' })
  await h.message('user_transcript', { text: 'One two three' })
  assert.equal(h.pages.length, count)
  await h.advance(250)
  assert.equal(h.pages.length, count + 1)
  assert.equal(h.pages.at(-1).transcript, 'One two three')
  const upgrades = h.upgrades.length
  await h.message('user_transcript', { text: 'One two three' })
  await h.advance(250)
  assert.equal(h.pages.length, count + 1)
  assert.equal(h.upgrades.length, upgrades)
})

test('thinking is static and a pending transcript cannot overwrite the answer', async t => {
  const h = await harness(t)
  await h.click()
  await h.message('user_transcript', { text: 'Last words' })
  await h.message('assistant_thinking')
  assert.equal(h.pages.at(-1).transcript, 'Last words')
  const count = h.pages.length
  await h.advance(3_000)
  assert.equal(h.pages.length, count)
  assert.equal(h.upgrades.length, 0)
  await h.message('assistant_response', { text: 'A complete answer.' })
  await h.advance(500)
  assert.equal(h.pages.at(-1).message.body, 'A complete answer.')
})

test('speech received just before expiry keeps capture alive despite paced rendering', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.advance(29_990)
  await h.message('user_transcript', { text: 'A follow-up' })
  await h.advance(20)
  assert.equal(h.audio.at(-1), true)
  await h.advance(230)
  assert.equal(h.pages.at(-1).transcript, 'A follow-up')
})

test('sleep discards scheduled transcript updates', async t => {
  const h = await harness(t)
  await h.click()
  await h.message('user_transcript', { text: 'Pending words' })
  await h.sleep()
  await h.advance(500)
  assert.equal(h.pages.at(-1).label, 'sleep')
})

test('real layouts keep body and status disjoint and preserve all list content', () => {
  const cache = new Map()
  function load(file) {
    if (cache.has(file)) return cache.get(file).exports
    const module = { exports: {} }
    cache.set(file, module)
    const source = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2023 },
    }).outputText
    const sdk = { RebuildPageContainer: class { constructor(data) { Object.assign(this, data) } },
      TextContainerProperty: class { constructor(data) { Object.assign(this, data) } } }
    const localRequire = name => name === '@evenrealities/even_hub_sdk' ? sdk
      : load(path.resolve(path.dirname(file), name.replace(/\.js$/, '') + '.ts'))
    vm.runInNewContext('(function(require,module,exports){' + source + '\n})')(localRequire, module, module.exports)
    return module.exports
  }
  const ui = load(path.resolve(__dirname, '../src/even/glasses-ui.ts'))
  const source = 'A meal plan\n\n' + Array.from({ length: 8 }, (_, i) => '- Ingredient ' + i).join('\n')
    + '\n\nDo not forget the final advice.'
  const message = ui.presentGlassesMessage(source)
  assert.equal(message.body, source)
  const parts = ui.glassesMessagePages(message)
  assert.equal(parts.join(' ').replace(/\s+/g, ' '), source.replace(/\s+/g, ' '))
  const pages = parts.map((_, i) => ui.buildMessagePage(message, 'Listening', i))
  pages.push(ui.buildTranscriptPage('word '.repeat(100)), ui.buildCompactPage('MIC UNAVAILABLE · TAP TO RETRY'))
  for (const page of pages) {
    assert.equal(page.textObject.filter(box => box.isEventCapture === 1).length, 1)
    for (const box of page.textObject) {
      assert.equal(box.borderWidth, 0)
      assert.equal(box.paddingLength, 0)
      assert.ok(box.xPosition >= 0 && box.xPosition + box.width <= 576)
      assert.ok(box.yPosition >= 0 && box.yPosition + box.height <= 288)
    }
    if (page.textObject.length === 2) {
      const [body, status] = page.textObject
      assert.ok(body.yPosition + body.height < status.yPosition)
      assert.ok(status.content.length <= 32)
      assert.ok(body.content.split('\n').length <= 6)
    }
  }
})
