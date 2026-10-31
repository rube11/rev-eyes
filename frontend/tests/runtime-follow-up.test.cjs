const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const ts = require('typescript')

// Exercise the real runtime, lifecycle and audio controller with fake SDK/socket
// boundaries. No hardware microphone, network requests, or model calls.
async function harness(t, serverListeningEnabled = false, reconnect = false, realDisplay = false, agentOptions = {}) {
  const selections = []
  const connectionOptions = []
  const threadReads = []
  let now = 0
  let eventHandler
  let allowAudio = true
  let audioGate
  let sequence = 0
  const timers = new Map()
  const audio = []
  const pcmFrames = []
  const controls = []
  const pages = []
  const statuses = []
  const upgrades = []
  const nativeCalls = []
  const workspaceEvents = []
  let nativePage
  let failedNativeRenders = 0
  let nativeAckGate
  let nativeAckDelay = 0
  let nativePageCreated = false
  let connectionGate
  const sockets = []
  function newSocket() {
    const listeners = new Map()
    const socket = {
      listeners, readyState: 1,
      addEventListener: (type, fn) => listeners.set(type, fn),
      removeEventListener: (type, fn) => { if (listeners.get(type) === fn) listeners.delete(type) },
      send: data => { if (typeof data === 'string') controls.push(JSON.parse(data).type); else pcmFrames.push(Array.from(new Uint8Array(data))) },
      close: () => { socket.readyState = 3; listeners.get('close')?.() },
    }
    sockets.push(socket)
    return socket
  }
  let socket = newSocket()
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
    "../shared/config/env": { env: { serverListeningEnabled } },
    "../../shared/config/env": { env: { serverListeningEnabled } },
    "../features/workspace/agentClient": {
      loadAgentConnections: async (...args) => agentOptions.loadConnections ? agentOptions.loadConnections(...args) : agentOptions.connections ?? [],
      openAgentThread: async (_token, target, _signal, after) => {
        threadReads.push({ target, after })
        return agentOptions.readThread ? agentOptions.readThread(_signal, after) : { messages: [{id:'m',role:'assistant',text:agentOptions.replyMessage ?? 'The fix is ready for review.'}], hasMore:false, nextPosition:12, truncated:false }
      },
    },
    '@evenrealities/even_hub_sdk': {
      AppLocationAccuracy: { Medium: 1 }, AudioInputSource: { Glasses: 1 },
      OsEventTypeList: { CLICK_EVENT: 0, SCROLL_TOP_EVENT: 1, SCROLL_BOTTOM_EVENT: 2, DOUBLE_CLICK_EVENT: 3 },
    },
    './glasses-page-host': {
      getEvenBridge: async () => bridge, resumeGlassesPage: () => {},
      renderGlassesPage: async page => pages.push(page),
      upgradeCompactText: async text => { upgrades.push(text); pages.at(-1).label = text; return true },
      upgradeTranscriptText: async text => { upgrades.push(text); pages.at(-1).transcript = text; return true },
      upgradeTranscriptStatus: async footer => { upgrades.push(footer); pages.at(-1).footer = footer; return true },
      upgradeMessageStatus: async footer => { upgrades.push(footer); pages.at(-1).footer = footer; return true },
    },
    './glasses-ui': {
      buildCompactPage: label => ({ label }), buildSleepPage: () => ({ label: 'sleep' }),
      buildCompactContent: label => label,
      fitGlassesText: text => text,
      buildThreadPage: (title, detail, footer) => ({ message: { kind: 'answer', body: `${title}\n\n${detail}` }, footer }),
      buildMessagePage: (message, footer, pageIndex = 0) => ({ message, footer, pageIndex }),
      buildMessageStatus: (_message, footer) => footer,
      glassesMessagePages: message => message.body.replace(/\n/g, ' ').match(/.{1,100}/g) ?? [' '],
      buildTranscriptContent: text => text,
      buildTranscriptPage: (text, thinking, listeningStatus) => ({
        transcript: text, footer: thinking ? 'Thinking' : listeningStatus,
      }),
      presentGlassesMessage: text => ({ kind: 'answer', body: text }),
    },
    '../shared/api/client': { connectRealtimeSocket: async (_token, options) => {
      connectionOptions.push(options)
      if (reconnect && socket.readyState !== 1) socket = newSocket()
      const opened = socket
      if (connectionGate) await connectionGate
      return opened
    } },
  }
  if (realDisplay) {
    const sdkContext = vm.createContext({ console, exports: {}, setTimeout, clearTimeout })
    vm.runInContext(fs.readFileSync(require.resolve('@evenrealities/even_hub_sdk'), 'utf8'), sdkContext)
    sdkContext.window = {}
    const sdk = sdkContext.exports
    const nativeBridge = Object.create(sdk.EvenAppBridge.prototype)
    nativeBridge.callEvenApp = async (method, params) => {
      nativeCalls.push(method)
      // The native protocol limits names to 16 characters; SDK 0.0.12 does
      // not validate this before forwarding the page to the host.
      if (params?.textObject?.some(item => item.containerName.length > 16)) {
        return method === 'createStartUpPageContainer' ? sdk.StartUpPageCreateResult.invalid : false
      }
      if (method === 'createStartUpPageContainer') {
        if (nativePageCreated) return sdk.StartUpPageCreateResult.invalid
        nativePageCreated = true
        nativePage = structuredClone(params)
        return sdk.StartUpPageCreateResult.success
      }
      if (method === 'rebuildPageContainer' || method === 'textContainerUpgrade') {
        if (failedNativeRenders > 0) { failedNativeRenders -= 1; return false }
        if (nativeAckDelay) {
          await new Promise(resolve => {
            const id = ++sequence
            timers.set(id, { fn: resolve, at: now + nativeAckDelay })
          })
        }
        if (method === 'rebuildPageContainer') nativePage = structuredClone(params)
        else {
          const target = nativePage.textObject.find(item => item.containerID === params.containerID && item.containerName === params.containerName)
          if (!target) return false
          target.content = params.content
        }
        // The device may apply an update but never acknowledge it to the SDK.
        const gate = nativeAckGate
        nativeAckGate = undefined
        if (gate) await gate
        return true
      }
      throw new Error('Unexpected native method: ' + method)
    }
    Object.assign(bridge, {
      createStartUpPageContainer: nativeBridge.createStartUpPageContainer.bind(nativeBridge),
      rebuildPageContainer: nativeBridge.rebuildPageContainer.bind(nativeBridge),
      textContainerUpgrade: nativeBridge.textContainerUpgrade.bind(nativeBridge),
    })
    mocks['@evenrealities/even_hub_sdk'] = { ...sdk, waitForEvenAppBridge: async () => bridge }
    delete mocks['./glasses-page-host']
    delete mocks['./glasses-ui']
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
  const dispose = await initializeEvenExperience(
    'synthetic-test-token', value => statuses.push(value), resources => workspaceEvents.push(resources), undefined,
    { target: agentOptions.target, onTargetChange: target => selections.push(target) },
  )
  t.after(dispose)
  await advance(0)
  const message = async (type, extra = {}) => { socket.listeners.get('message')?.({ data: JSON.stringify({ type, ...extra }) }); await flush() }
  const click = async () => { eventHandler?.({ sysEvent: { eventType: 0 } }); await flush() }
  const sleep = async () => { eventHandler?.({ jsonData: { gesture: 'LONG_PRESS' } }); await flush() }
  const scroll = async direction => { eventHandler?.({ textEvent: { eventType: direction > 0 ? 2 : 1 } }); await flush() }
  async function reply() {
    await click()
    await message('user_transcript', { text: 'What time is it?' })
    await message('assistant_thinking')
    await message('assistant_response', { text: 'It is noon.' })
  }
  return { audio, controls, pages, statuses, upgrades, pcmFrames, sockets, workspaceEvents, selections, connectionOptions, threadReads,
    nativeCalls, nativeText: () => nativePage?.textObject.map(item => item.content).join('\n'),
    failNativeRender: (count = 1) => { failedNativeRenders = count },
    setNativeAckDelay: milliseconds => { nativeAckDelay = milliseconds },
    delayNativeAck: () => { let release; nativeAckGate = new Promise(resolve => { release = resolve }); return release },
    delayConnection: () => { let release; connectionGate = new Promise(resolve => { release = resolve }); return release },
    pcm: async (pcm, speakerRole, source = 'glasses') => { eventHandler?.({ audioEvent: { audioPcm: pcm, speakerRole, source } }); await flush() }, message, click, sleep, scroll, reply, advance, dispose,
    disconnect: async () => { socket.close(); await flush() },
    denyAudio: () => { allowAudio = false },
    delayAudio: () => { let release; audioGate = new Promise(resolve => { release = resolve }); return release },
  }
}

test('real display host delivers a Moonshine conversation reply through the installed SDK', async t => {
  const h = await harness(t, true, false, true)
  await h.message('conversation_started')
  assert.match(h.nativeText(), /LISTENING \. · PAUSE TO SEND/)
  await h.advance(800)
  assert.match(h.nativeText(), /LISTENING \. \. · PAUSE TO SEND/)
  await h.message('user_transcript', { text: 'Hey glasses, what time is it?' })
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'It is noon.' })
  assert.match(h.nativeText(), /It is noon\./)
  assert.equal(h.audio.filter(Boolean).length, 1)
  assert.equal(h.audio.includes(false), false)
})

test('phone chat refreshes immediately while a display acknowledgment is stalled', async t => {
  const h = await harness(t, true, false, true)
  await h.message('conversation_started')
  await h.message('user_transcript', { text: 'Hey glasses, what time is it?' })
  await h.advance(250)
  const release = h.delayNativeAck()
  try {
    await h.message('assistant_thinking')
    await h.message('workspace_changed', { resources: ['conversations'] })
    assert.equal(h.workspaceEvents.length, 1)
    assert.equal(h.workspaceEvents[0][0], 'conversations')
  } finally {
    release()
    await h.advance(0)
  }
})

for (const acknowledgmentMs of [300, 500]) {
  test(`sustained ${acknowledgmentMs}ms display acknowledgments do not accumulate stale transcripts`, async t => {
    const h = await harness(t, true, false, true)
    await h.message('conversation_started')
    h.setNativeAckDelay(acknowledgmentMs)
    try {
      // Model a continuous recognizer emitting ten interim results per second.
      for (let update = 1; update <= 60; update += 1) {
        await h.message('user_transcript', { text: `Transcript update ${update}` })
        await h.advance(100)
      }
      const displayed = Number(h.nativeText().match(/Transcript update (\d+)/)?.[1])
      assert.ok(displayed >= 45, `latest input was 60 but display was ${displayed}`)

      await h.message('assistant_thinking')
      await h.message('assistant_response', { text: 'The final answer.' })
      await h.advance(acknowledgmentMs * 2)
      assert.match(h.nativeText(), /The final answer\./)
      assert.equal(h.audio.filter(Boolean).length, 1)
      assert.equal(h.audio.includes(false), false)
    } finally {
      h.setNativeAckDelay(0)
      await h.advance(10_000)
    }
  })
}

test('queued transcript batches cannot move speech across conversation boundaries', async t => {
  const h = await harness(t, true, false, true)
  await h.message('conversation_started')
  await h.message('user_transcript', { text: 'The first conversation.' })
  await h.advance(250)
  const release = h.delayNativeAck()
  try {
    await h.advance(800)
    await h.message('user_transcript', { text: 'The previous conversation ended.' })
    await h.advance(250)
    await h.message('conversation_idle')
    await h.message('conversation_started')
    await h.message('user_transcript', { text: 'This belongs to the new conversation.' })
    await h.advance(250)
    release()
    await h.advance(0)
    assert.match(h.nativeText(), /This belongs to the new\s+conversation\./)
    assert.doesNotMatch(h.nativeText(), /previous conversation ended/)
  } finally {
    release()
    await h.advance(0)
  }
})

for (const method of ['rebuildPageContainer', 'textContainerUpgrade']) {
  test(`a stalled ${method} cannot consume the queued answer or a late acknowledgment`, async t => {
    const h = await harness(t, true, false, true)
    await h.message('conversation_started')
    await h.message('user_transcript', { text: 'Hey glasses, what time is it?' })
    await h.advance(250)
    const release = h.delayNativeAck()
    try {
      if (method === 'textContainerUpgrade') await h.advance(800)
      else await h.message('assistant_thinking')
      assert.equal(h.nativeCalls.at(-1), method)
      await h.message('assistant_response', { text: 'It is noon.' })
      await h.message('conversation_idle')
      await h.advance(5_000)
      assert.match(h.nativeText(), /It is noon\./)
      assert.notEqual(h.statuses.at(-1), 'Thinking')
      assert.ok(h.statuses.includes('Glasses display update timed out'))
      const display = h.nativeText()
      release()
      await h.advance(0)
      assert.equal(h.nativeText(), display, 'late acknowledgment must not restore stale state')
      assert.equal(h.nativeCalls.filter(call => call === 'createStartUpPageContainer').length, 1)
      assert.equal(h.audio.filter(Boolean).length, 1)
      assert.equal(h.audio.includes(false), false)
    } finally {
      release()
      await h.advance(0)
    }
  })
}

test('a stalled display acknowledgment cannot hold reconnect or teardown indefinitely', async t => {
  const h = await harness(t, true, true, true)
  await h.message('conversation_started')
  const release = h.delayNativeAck()
  try {
    await h.message('assistant_thinking')
    await h.disconnect()
    await h.advance(6_000)
    assert.equal(h.sockets.length, 2)
    assert.equal(h.audio.at(-1), true)
  } finally {
    release()
    await h.advance(0)
  }

  const releaseAgain = h.delayNativeAck()
  try {
    await h.message('assistant_thinking')
    let disposed = false
    const disposal = h.dispose().then(() => { disposed = true })
    await h.advance(5_000)
    assert.equal(disposed, true)
    await disposal
  } finally {
    releaseAgain()
    await h.advance(0)
  }
})

test('listening dots breathe across compact and transcript views, then stop while thinking', async t => {
  const h = await harness(t)
  await h.click()
  assert.match(h.pages.at(-1).label, /LISTENING \.  ·  PAUSE TO SEND/)

  await h.advance(800)
  assert.match(h.pages.at(-1).label, /LISTENING \. \.  ·  PAUSE TO SEND/)
  await h.message('user_transcript', { text: 'What is next?' })
  await h.advance(250)
  assert.equal(h.pages.at(-1).footer, 'Listening . .')

  await h.advance(800)
  assert.equal(h.pages.at(-1).footer, 'Listening . . .')
  await h.advance(800)
  assert.equal(h.pages.at(-1).footer, 'Listening . .')
  await h.advance(800)
  assert.equal(h.pages.at(-1).footer, 'Listening .')

  await h.message('assistant_thinking')
  const upgradeCount = h.upgrades.length
  await h.advance(3_200)
  assert.equal(h.upgrades.length, upgradeCount)
  assert.equal(h.pages.at(-1).footer, 'Thinking')
})

test('a rejected transcript write cannot consume the following assistant response', async t => {
  const h = await harness(t, true, false, true)
  await h.message('conversation_started')
  h.failNativeRender()
  await h.message('user_transcript', { text: 'Hey glasses, what time is it?' })
  await h.advance(250)
  assert.equal(h.statuses.at(-1), 'Glasses display update rejected')
  await h.message('assistant_response', { text: 'It is noon.' })
  await h.advance(5_000)
  assert.match(h.nativeText(), /It is noon\./)
  assert.equal(h.nativeCalls.filter(method => method === 'createStartUpPageContainer').length, 1)
})




test('native rejection reports the failure without retries or startup recreation', async t => {
  const h = await harness(t, true, false, true)
  const before = h.nativeCalls.length
  h.failNativeRender(100)
  await h.message('assistant_response', { text: 'It is noon.' })
  await h.advance(30_000)
  assert.equal(h.nativeCalls.length - before, 1)
  assert.equal(h.statuses.at(-1), 'Glasses display update rejected')
})

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

test('follow-up speech keeps the answer visible until a replacement is ready', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')

  await h.message('user_transcript', { text: 'And tomorrow?' })
  await h.advance(250)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')

  await h.message('assistant_thinking')
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  assert.equal(h.pages.at(-1).footer, 'Thinking')
  await h.message('assistant_response', { text: 'Tomorrow is Monday.' })
  assert.equal(h.pages.at(-1).message.body, 'Tomorrow is Monday.')
})

test('server-managed follow-up also preserves the answer through transcript updates', async t => {
  const h = await harness(t, true)
  await h.message('conversation_started')
  await h.message('user_transcript', { text: 'What time is it?' })
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'It is noon.' })
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')

  await h.message('user_transcript', { text: 'And tomorrow?' })
  await h.advance(250)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')

  await h.message('assistant_thinking')
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  assert.equal(h.pages.at(-1).footer, 'Thinking')
})

for (const completion of ['assistant_done', 'assistant_response']) {
  test(`a new keyword conversation and silent ${completion} preserve the previous answer`, async t => {
    const h = await harness(t, true, false, true)
    await h.message('assistant_response', { text: 'Your meeting starts at noon.' })
    await h.message('conversation_idle')
    await h.message('conversation_started')
    assert.match(h.nativeText(), /Your meeting starts at noon\./)
    await h.message('user_transcript', { text: 'Hey glasses, never mind.' })
    await h.message('assistant_thinking')
    assert.match(h.nativeText(), /Your meeting starts at noon\./)
    assert.match(h.nativeText(), /Thinking/)
    await h.message(completion)
    assert.match(h.nativeText(), /Your meeting starts at noon\./)
    assert.doesNotMatch(h.nativeText(), /Thinking/)
    await h.message('conversation_idle')
    assert.match(h.nativeText(), /Your meeting starts at noon\./)
    assert.equal(h.audio.filter(Boolean).length, 1)
  })
}

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

test('speech received just before expiry keeps capture alive without hiding the answer', async t => {
  const h = await harness(t)
  await h.reply()
  await h.message('listening_stopped')
  await h.advance(29_990)
  await h.message('user_transcript', { text: 'A follow-up' })
  await h.advance(20)
  assert.equal(h.audio.at(-1), true)
  await h.advance(230)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
})

test('sleep discards scheduled transcript updates', async t => {
  const h = await harness(t)
  await h.click()
  await h.message('user_transcript', { text: 'Pending words' })
  await h.sleep()
  await h.advance(500)
  assert.equal(h.pages.at(-1).label, 'sleep')
})

function loadGlassesUI() {
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
  return load(path.resolve(__dirname, '../src/even/glasses-ui.ts'))
}

test('real layouts keep body and status disjoint and preserve all list content', () => {
  const ui = loadGlassesUI()
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
      assert.ok(box.containerName.length <= 16, box.containerName)
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

test('server listening captures immediately and keeps audio running between manual turns', async t => {
  const h = await harness(t, true)
  assert.equal(h.audio.at(-1), true)
  assert.ok(h.controls.includes('ambient_start'))
  assert.equal(h.controls.includes('listening_start'), false)
  await h.click()
  assert.ok(h.controls.includes('listening_start'))
  await h.click()
  assert.ok(h.controls.includes('conversation_finalize'))
  assert.equal(h.audio.includes(false), false)
  await h.message('ambient_candidate', { id: 'ambient-manual' })
  await h.message('assistant_done', { id: 'ambient-manual' })
  assert.equal(h.audio.at(-1), true)
  assert.match(h.pages.at(-1).label, /LISTENING/)
})

test('server owns follow-up expiry without stopping ambient capture', async t => {
  const h = await harness(t, true)
  await h.message('assistant_response', { text: 'It is noon.' })
  assert.ok(h.controls.includes('ambient_reply_arm'))
  assert.equal(h.controls.includes('listening_start'), false)
  await h.advance(30_000)
  assert.notEqual(h.pages.at(-1).footer, 'Tap to talk')
  await h.message('conversation_idle')
  assert.ok(h.controls.includes('ambient_reply_disarm'))
  assert.equal(h.controls.includes('ambient_stop'), false)
  assert.equal(h.audio.at(-1), true)
  assert.equal(h.pages.at(-1).message.body, 'It is noon.')
  assert.equal(h.pages.at(-1).footer, 'Tap to talk')
})

test('server listening stops on sleep and resumes on wake', async t => {
  const h = await harness(t, true)
  await h.sleep()
  assert.equal(h.audio.at(-1), false)
  assert.ok(h.controls.includes('ambient_stop'))
  await h.advance(751) // Ignore the long-press release click, as on the device.
  await h.click()
  assert.equal(h.audio.at(-1), true)
  assert.equal(h.controls.filter(x => x === 'ambient_start').length, 2)
})

test('server listener failure stops capture without opening a paid stream', async t => {
  const h = await harness(t, true)
  await h.message('listening_stopped', { error: 'Server listening unavailable' })
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.includes('listening_start'), false)
  assert.match(h.pages.at(-1).label, /MIC UNAVAILABLE/)
})


test('server listening forwards copied PCM while the UI is idle', async t => {
  const h = await harness(t, true)
  const frame = new Uint8Array([1, 2, 3, 4])
  await h.pcm(frame)
  assert.deepEqual(h.pcmFrames, [[1, 2, 3, 4]])
  assert.deepEqual(Array.from(frame), [1, 2, 3, 4])
  assert.equal(h.controls.includes('listening_start'), false)
})

test('failed server capture on wake keeps the microphone error visible', async t => {
  const h = await harness(t, true)
  await h.sleep()
  h.denyAudio()
  await h.advance(751)
  await h.click()
  assert.equal(h.controls.at(-2), 'ambient_stop')
  assert.match(h.pages.at(-1).label, /MIC UNAVAILABLE/)
})

test('speaker labels are ignored without restarting capture or sending control commands', async t => {
  const h = await harness(t, true)
  const controls = h.controls.length
  const captures = h.audio.length
  for (const role of ['self', 'other', 'unknown', 'self']) {
    await h.pcm(new Uint8Array([42, 0]), role)
  }
  await h.pcm(new Uint8Array([42, 0]), 'self', 'phone')
  assert.deepEqual(h.pcmFrames, [[42,0], [42,0], [42,0], [42,0], [42,0]])
  assert.equal(h.controls.length, controls)
  assert.equal(h.audio.length, captures)
})

test('server tap dismisses a visible answer and closes only the conversation', async t => {
  const h = await harness(t, true)
  await h.message('assistant_response', { text: 'It is noon.' })
  await h.pcm(new Uint8Array([1, 0, 2, 0]))
  const before = h.controls.length
  await h.click()
  assert.deepEqual(h.controls.slice(before, before + 2), ['conversation_stop', 'ambient_reply_disarm'])
  assert.equal(h.controls.includes('ambient_stop'), false)
  assert.equal(h.audio.at(-1), true)
})

test('streaming conversation accepts a keyword trigger and consecutive replies without restarting capture', async t => {
  const h = await harness(t, true)
  await h.message('conversation_started')
  await h.message('user_transcript', { text: 'Glasses what is for dinner?' })
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'Try a vegetable curry.' })
  await h.advance(45_000)
  await h.message('user_transcript', { text: 'What ingredients do I need?' })
  await h.message('assistant_thinking')
  await h.message('assistant_response', { text: 'Chickpeas, vegetables and coconut milk.' })
  assert.equal(h.audio.filter(Boolean).length, 1)
  assert.equal(h.audio.includes(false), false)
  assert.equal(h.controls.includes('conversation_stop'), false)
  await h.message('conversation_idle')
  assert.equal(h.pages.at(-1).footer, 'Tap to talk')
})


test('reconnect starts fresh capture and ignores events from the old socket', async t => {
  const h = await harness(t, true, true)
  const oldMessage = h.sockets[0].listeners.get('message')
  await h.message('conversation_started')
  await h.disconnect()
  await h.advance(1_000)
  assert.equal(h.sockets.length, 2)
  assert.equal(h.controls.filter(x => x === 'ambient_start').length, 2)
  const pageCount = h.pages.length
  oldMessage({ data: JSON.stringify({ type: 'assistant_response', text: 'Stale reply' }) })
  await h.advance(500)
  assert.equal(h.pages.length, pageCount)
  assert.equal(h.audio.at(-1), true)
})

test('teardown closes a socket that finishes connecting late', async t => {
  const h = await harness(t, true, true)
  const release = h.delayConnection()
  await h.disconnect()
  await h.advance(1_000)
  await h.dispose()
  release()
  await h.advance(0)
  assert.equal(h.sockets.at(-1).readyState, 3)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.filter(x => x === 'ambient_start').length, 1)
})

test('server notifications defer answers and acknowledge dismissal without stopping ambient capture', async t => {
  const h = await harness(t, true)
  await h.message('assistant_response', { text: 'First answer' })
  await h.message('notification', { id: 'n1', text: 'Reminder' })
  await h.message('assistant_response', { text: 'Deferred answer' })
  assert.equal(h.pages.at(-1).message.body, 'Reminder')
  await h.click()
  assert.ok(h.controls.includes('notification_ack'))
  assert.equal(h.pages.at(-1).message.body, 'Deferred answer')
  assert.equal(h.controls.includes('ambient_stop'), false)
  await h.message('conversation_idle')
  assert.equal(h.audio.at(-1), true)
  assert.equal(h.pages.at(-1).footer, 'Tap to talk')
})


test('selected T3 thread uses a bound manual socket and never opens ambient or follow-up capture', async t => {
  const target = {connectionId:'laptop',threadId:'mcp:launch:chosen',title:'Fix audio'}
  const h = await harness(t,true,false,false,{target})
  assert.equal(h.audio.length,0)
  assert.equal(h.connectionOptions[0].agentTarget.threadId,target.threadId)
  await h.click()
  assert.equal(h.controls.includes('listening_start'),true)
  assert.equal(h.controls.includes('ambient_start'),false)
  await h.message('user_transcript',{text:'Please fix the bug'})
  await h.message('assistant_thinking')
  await h.message('assistant_response',{text:'Message queued in your T3 thread.'})
  await h.message('listening_stopped')
  await h.advance(31_000)
  assert.equal(h.audio.filter(Boolean).length,1)
  assert.equal(h.audio.at(-1),false)
  await h.scroll(1)
  assert.equal(h.threadReads[0].target.threadId,target.threadId)
  assert.match(h.pages.at(-1).message.body,/fix is ready for review/)
})

test('glasses thread picker stops ambient capture and selects an exact opaque target', async t => {
  const h = await harness(t,true,false,false,{connections:[{id:'laptop',unavailable:false,canSend:true,threads:[{id:'mcp:launch:chosen',title:'Fix audio',status:'running'}],hasMore:false}]})
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body,/Fix audio/)
  assert.equal(h.controls.includes('ambient_stop'),true)
  assert.equal(h.selections.length,0)
  await h.click()
  assert.equal(h.selections[0].connectionId,'laptop')
  assert.equal(h.selections[0].threadId,'mcp:launch:chosen')
})

test('holding an idle T3 thread returns to Eyes without sending or starting a microphone', async t => {
  const h = await harness(t,true,false,false,{target:{connectionId:'laptop',threadId:'id',title:'Fix audio'}})
  await h.sleep()
  assert.equal(h.selections.length,1)
  assert.equal(h.selections[0],undefined)
  assert.equal(h.audio.filter(Boolean).length,0)
})


test('long T3 replies keep their reading position without starting another microphone', async t => {
  const h=await harness(t,true,false,false,{target:{connectionId:'laptop',threadId:'id',title:'Task'},replyMessage:'A'.repeat(350)})
  await h.click()
  await h.message('assistant_thinking')
  await h.message('assistant_response',{text:'Queued.'})
  await h.message('listening_stopped')
  await h.scroll(1)
  assert.equal(h.pages.at(-1).pageIndex,0)
  await h.scroll(1)
  assert.equal(h.pages.at(-1).pageIndex,1)
  assert.equal(h.audio.filter(Boolean).length,1)
})

function deferred() {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const chosenThread = { connectionId: 'laptop', threadId: 'mcp:launch:chosen', title: 'Fix audio' }
function threadConnection(threads = [{ id: chosenThread.threadId, title: chosenThread.title, status: 'running' }], extra = {}) {
  return { id: 'laptop', unavailable: false, canSend: true, threads, hasMore: false, ...extra }
}

test('thread reads before speaking, refreshes without a cursor, and shows waiting and failures', async t => {
  let fail = false
  const h = await harness(t, true, false, false, {
    target: chosenThread,
    readThread: async () => {
      if (fail) throw new Error('Unavailable')
      return { messages: [], nextPosition: 12, hasMore: false }
    },
  })
  assert.match(h.pages.at(-1).message.body, /Tap to speak\nScroll to read reply/)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /No recent messages/)
  await h.scroll(1)
  assert.equal(h.threadReads.length, 2)
  assert.ok(h.threadReads.every(read => read.after === undefined), 'refresh must also catch edits to existing messages')
  fail = true
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Couldn’t read reply.*\nScroll to retry/)
  assert.equal(h.pages.at(-1).footer, 'Hold: Eyes')
  assert.equal(h.audio.filter(Boolean).length, 0)
})

test('reply timeout is visible, allows retry and rejects late success', async t => {
  const late = deferred()
  let reads = 0
  const h = await harness(t, true, false, false, {
    target: chosenThread,
    readThread: () => ++reads === 1 ? late.promise : Promise.resolve({ messages: [], hasMore: false }),
  })
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Reading reply/)
  await h.advance(8_000)
  assert.match(h.pages.at(-1).message.body, /Couldn’t read reply/)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /No recent messages/)
  late.resolve({ messages: [{ role: 'assistant', text: 'Stale reply' }], nextPosition: 99 })
  await h.advance(0)
  assert.doesNotMatch(h.pages.at(-1).message.body, /Stale reply/)
})

test('hold exits a pending reply read without waiting for the network', async t => {
  const late = deferred()
  let signal
  const h = await harness(t, true, false, false, {
    target: chosenThread, readThread: incoming => { signal = incoming; return late.promise },
  })
  await h.scroll(1)
  await h.sleep()
  assert.equal(h.selections.length, 1)
  assert.equal(h.selections[0], undefined)
  assert.equal(signal.aborted, true)
  const count = h.pages.length
  late.resolve({ messages: [{ role: 'assistant', text: 'Late reply' }] })
  await h.advance(0)
  assert.equal(h.pages.length, count)
  assert.equal(h.audio.filter(Boolean).length, 0)
})

test('a reply read cannot replace a new speech turn', async t => {
  const late = deferred()
  const h = await harness(t, true, false, false, { target: chosenThread, readThread: () => late.promise })
  await h.scroll(1)
  await h.click()
  assert.match(h.pages.at(-1).label, /Listening/)
  late.resolve({ messages: [{ role: 'assistant', text: 'Stale reply' }], nextPosition: 99 })
  await h.advance(0)
  assert.match(h.pages.at(-1).label, /Listening/)
})

for (const [text, expected] of [
  ['Message queued in your T3 thread. Open the thread to check its reply.', 'Sent to thread.'],
  ['Delivery is uncertain. Check the T3 thread before sending again.', 'Send not confirmed.'],
  ['Message was not sent. Check the T3 connection and its permissions.', 'Not sent.'],
  ['Speech was not sent. Tap and speak your own message.', 'Not sent.'],
]) {
  test(`thread delivery feedback: ${text.split('.')[0]}`, async t => {
    const h = await harness(t, true, false, false, { target: chosenThread })
    await h.click()
    await h.message('assistant_thinking')
    assert.equal(h.pages.at(-1).footer, 'Sending')
    assert.equal(h.statuses.at(-1), 'Sending')
    await h.message('assistant_response', { text })
    await h.message('listening_stopped')
    assert.ok(h.pages.at(-1).message.body.startsWith(expected))
    assert.equal(h.pages.at(-1).footer, 'Scroll · Hold: Eyes')
    assert.equal(h.audio.filter(Boolean).length, 1)
    assert.equal(h.audio.at(-1), false)
  })
}

test('taps while a manual send is waiting do not start another microphone; Hold returns to Eyes', async t => {
  const h = await harness(t, true, false, false, { target: chosenThread })
  await h.click()
  await h.click()
  assert.equal(h.pages.at(-1).footer, 'Sending')
  await h.click()
  await h.click()
  assert.equal(h.audio.filter(Boolean).length, 1)
  await h.sleep()
  assert.equal(h.selections.length, 1)
  assert.equal(h.selections[0], undefined)
})

test('reconnect after speech shows unconfirmed delivery without resending', async t => {
  const h = await harness(t, true, true, false, { target: chosenThread })
  await h.click()
  await h.message('assistant_thinking')
  await h.disconnect()
  assert.match(h.pages.at(-1).label, /Reconnecting\nHold: Eyes/)
  await h.advance(1_000)
  assert.match(h.pages.at(-1).message.body, /Send not confirmed/)
  assert.equal(h.controls.filter(value => value === 'listening_start').length, 1)
  assert.equal(h.connectionOptions[1].agentTarget.threadId, chosenThread.threadId)
  await h.scroll(1)
  assert.equal(h.threadReads[0].target.threadId, chosenThread.threadId)
})

test('picker loading ignores taps and back cancels a request that resolves late', async t => {
  const late = deferred()
  let signal
  const h = await harness(t, true, false, false, {
    loadConnections: (_token, incoming) => { signal = incoming; return late.promise },
  })
  await h.scroll(1)
  await h.click()
  assert.equal(h.selections.length, 0)
  await h.sleep()
  assert.equal(signal.aborted, true)
  assert.match(h.pages.at(-1).label, /TAP TO TALK/)
  const count = h.pages.length
  late.resolve([threadConnection()])
  await h.advance(0)
  assert.equal(h.pages.length, count)
  assert.equal(h.selections.length, 0)
  assert.equal(h.audio.at(-1), true)
})

test('picker survives reconnect without opening ambient capture or losing its selected row', async t => {
  const h = await harness(t, true, true, false, { connections: [threadConnection()] })
  await h.scroll(1)
  const row = h.pages.at(-1).message.body
  await h.disconnect()
  await h.advance(1_000)
  assert.equal(h.pages.at(-1).message.body, row)
  assert.equal(h.audio.at(-1), false)
  assert.equal(h.controls.filter(value => value === 'ambient_start').length, 1)
  await h.message('conversation_started')
  await h.message('assistant_response', { text: 'Stale Eyes response' })
  assert.equal(h.pages.at(-1).message.body, row)
  await h.click()
  assert.equal(h.selections[0].threadId, chosenThread.threadId)
})

test('a tap queued with a scroll never selects a row the user has not seen', async t => {
  const h = await harness(t, true, false, false, { connections: [threadConnection([
    { id: 'first', title: 'Same title', status: 'running' },
    { id: 'second', title: 'Same title', status: 'running' },
  ])] })
  await h.scroll(1)
  await Promise.all([h.scroll(1), h.click()])
  assert.equal(h.selections.length, 0)
  await h.click()
  assert.equal(h.selections[0].threadId, 'second')
  await h.click()
  assert.equal(h.selections.length, 1)
  assert.equal(h.controls.filter(value => value === 'listening_start').length, 0)
})

test('Eyes row and picker back restore speech without changing targets', async t => {
  const h = await harness(t, true, false, false, { connections: [threadConnection()] })
  await h.scroll(1)
  await h.scroll(-1)
  assert.match(h.pages.at(-1).message.body, /^Eyes/)
  await h.click()
  assert.match(h.pages.at(-1).label, /TAP TO TALK/)
  assert.equal(h.selections.length, 0)
  assert.equal(h.audio.at(-1), true)
  await h.scroll(1)
  await h.sleep()
  await h.click() // Release click is suppressed.
  assert.equal(h.controls.includes('listening_start'), false)
  await h.advance(750)
  await h.click()
  assert.equal(h.controls.includes('listening_start'), true)
})

test('picker failures stay visible and allow scroll to retry', async t => {
  let calls = 0
  const h = await harness(t, true, false, false, {
    loadConnections: async () => {
      if (++calls === 1) throw new Error('Unavailable')
      return [threadConnection()]
    },
  })
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Threads unavailable/)
  await h.click()
  assert.equal(h.audio.at(-1), false)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Fix audio/)
  await h.click()
  assert.equal(h.selections[0].threadId, chosenThread.threadId)
})

test('load more failures retain the last row and allow retry with the same cursor', async t => {
  const requested = []
  let fail = true
  const h = await harness(t, true, false, false, {
    loadConnections: async (_token, _signal, page) => {
      requested.push(page)
      if (!page) return [threadConnection(undefined, { hasMore: true, cursor: 20 })]
      if (fail) { fail = false; throw new Error('Unavailable') }
      return [threadConnection([{ id: 'second', title: 'Second task', status: 'idle' }])]
    },
  })
  await h.scroll(1)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Couldn’t load more/)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Second task/)
  assert.equal(requested[1].cursor, 20)
  assert.equal(requested[2].cursor, 20)
  await h.click()
  assert.equal(h.selections[0].threadId, 'second')
})

test('long titles and thread controls fit one body page and one footer line', () => {
  const ui = loadGlassesUI()
  for (const title of ['Fix audio', 'WWW '.repeat(80), '界👩‍💻'.repeat(100), 'same\n'.repeat(80)]) {
    for (const [detail, action] of [
      ['Tap to speak\nScroll to read reply', 'Hold: Eyes'],
      ['laptop\nScroll: threads · Hold: back', '999/1000 · Tap: select'],
      ['Couldn’t load more.\nScroll to retry.', 'Tap: select · Hold: back'],
      ['Send not confirmed.\nScroll to check the thread.', 'Hold: Eyes'],
    ]) {
      const [body, status] = ui.buildThreadPage(title, detail, action).textObject
      assert.ok(body.content.split('\n').length <= 6, body.content)
      assert.equal(ui.glassesMessagePages(ui.presentGlassesMessage(body.content)).length, 1)
      assert.equal(status.content, action)
      assert.equal(body.isEventCapture, 1)
      if (title.length > 100) assert.match(body.content, /…/)
      if (detail.includes('Hold: back')) assert.match(body.content, /Hold: back/)
    }
  }
  const reply = ui.presentGlassesMessage('W'.repeat(20_000))
  const footer = ui.buildMessageStatus(reply, 'Scroll · Hold: Eyes', 99)
  assert.match(footer, /^100\/\d+ · Scroll · Hold: Eyes$/)
  assert.equal(footer.includes('\n'), false)
  assert.equal(ui.glassesMessagePages(reply).join('').replace(/\s/g, '').length, 20_000)
})

test('installed SDK renders thread controls, sending feedback and a paged reply', async t => {
  const h = await harness(t, true, false, true, { target: chosenThread, replyMessage: 'Reply '.repeat(120) })
  assert.match(h.nativeText(), /Fix audio/)
  assert.match(h.nativeText(), /Scroll to read reply/)
  assert.match(h.nativeText(), /Hold: Eyes/)
  await h.click()
  assert.match(h.nativeText(), /Pause or tap to send/)
  await h.message('user_transcript', { text: 'Please fix the bug' })
  await h.advance(250)
  await h.click()
  assert.match(h.nativeText(), /Sending/)
  await h.message('assistant_response', { text: 'Message queued in your T3 thread. Open the thread to check its reply.' })
  await h.message('listening_stopped')
  assert.match(h.nativeText(), /Sent to thread/)
  await h.scroll(1)
  assert.match(h.nativeText(), /1\/\d+ · Scroll · Hold: Eyes/)
  await h.scroll(1)
  assert.match(h.nativeText(), /2\/\d+ · Scroll · Hold: Eyes/)
  assert.equal(h.audio.filter(Boolean).length, 1)
})

for (const text of ['', 'Please fix it']) {
  test(`silent thread completion clears the listening cue (${text ? 'speech' : 'no speech'})`, async t => {
    const h = await harness(t, true, false, false, { target: chosenThread })
    await h.click()
    if (text) await h.message('user_transcript', { text })
    await h.click()
    await h.message('assistant_done')
    await h.message('listening_stopped')
    assert.match(h.pages.at(-1).message.body, text ? /Send not confirmed/ : /No speech heard/)
    assert.equal(h.pages.at(-1).footer, 'Scroll · Hold: Eyes')
    assert.equal(h.audio.at(-1), false)
  })
}

test('opaque connection and thread IDs cannot collide when loading more rows', async t => {
  const h = await harness(t, true, false, false, {
    loadConnections: async (_token, _signal, page) => page
      ? [threadConnection([{ id: 'b:c', title: 'Exact second thread', status: 'idle' }], { id: 'a' })]
      : [
        threadConnection([{ id: 'c', title: 'First thread', status: 'idle' }], { id: 'a:b' }),
        threadConnection([{ id: 'd', title: 'Other thread', status: 'idle' }], { id: 'a', hasMore: true, cursor: 20 }),
      ],
  })
  await h.scroll(1)
  await h.scroll(1)
  await h.scroll(1)
  assert.match(h.pages.at(-1).message.body, /Exact second thread/)
  await h.click()
  assert.equal(h.selections[0].connectionId, 'a')
  assert.equal(h.selections[0].threadId, 'b:c')
})

test('a stop acknowledgement before thread delivery keeps Sending visible and blocks a second recording', async t => {
  const h = await harness(t, true, false, false, { target: chosenThread })
  await h.click()
  await h.message('user_transcript', { text: 'Fix it please' })
  await h.click()
  await h.message('listening_stopped')
  assert.equal(h.pages.at(-1).footer, 'Sending')
  await h.click()
  assert.equal(h.audio.filter(Boolean).length, 1)
  await h.message('assistant_response', { text: 'Message queued in your T3 thread.' })
  assert.match(h.pages.at(-1).message.body, /Sent to thread/)
})

test('interrupted transcription on a thread prompts a delivery check, not a microphone error', async t => {
  const h = await harness(t, true, true, false, { target: chosenThread })
  await h.click()
  await h.message('listening_stopped', { error: 'Transcription unavailable' })
  assert.match(h.pages.at(-1).message.body, /Speech interrupted.*\nScroll to check the thread/)
  assert.equal(h.pages.at(-1).footer, 'Hold: Eyes')
  await h.disconnect()
  await h.advance(1_000)
  assert.match(h.pages.at(-1).message.body, /Send not confirmed/)
})
