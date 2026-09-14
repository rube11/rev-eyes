// Exercises the actual transcriber/capture orchestration with browser hardware mocked.
// Does not emulate iOS suspension or execute the neural model.
const assert = require('node:assert/strict')
const { readFileSync } = require('node:fs')
const path = require('node:path')
const vm = require('node:vm')
const test = require('node:test')
const ts = require('typescript')

function fixture() {
  let failLoad = false
  let creates = 0
  const events = []
  const adapter = {
    stream: {}, state: 'running',
    contextState() { return this.state },
    trackState: () => 'live/audible',
    resume: async () => { adapter.state = 'running' },
    push() {}, clear() {}, dispose: async () => {},
  }
  let transcriber
  const sharedModel = {
    generate: async () => 'test one',
    isModelLoading: false,
    loaded: false,
    loadPromise: undefined,
    isLoaded() { return this.loaded },
  }
  class Transcriber {
    constructor() {
      transcriber = this
      this.isActive = false
      this.starts = 0
      this.sttModel = sharedModel
      this.audioContext = { state: 'running', resume: async () => { this.audioContext.state = 'running' }, close: async () => {} }
    }
    attachStream() {}
    async load() {
      sharedModel.loadPromise ??= (async () => {
        sharedModel.isModelLoading = true
        if (failLoad) { failLoad = false; throw new Error('temporary download error') }
        sharedModel.loaded = true
        sharedModel.isModelLoading = false
      })()
      await sharedModel.loadPromise
    }
    async start() { this.isActive = true; this.starts += 1 }
    stop() { this.isActive = false }
  }
  const snapshot = () => ({ startSampleOffset: 0, endSampleOffset: 0, retainedSamples: 0 })
  class CandidateAudioWindow {
    startRun() {} snapshot() { return snapshot() } clear() { return snapshot() }
    finalizePending() { return false } push() {} discardPending() {}
  }
  class Diagnostics {
    lifecycle(name) { events.push(name) }
    transcript() {} runSummary() {}
  }
  const cache = new Map()
  const mocks = {
    '@moonshine-ai/moonshine-js': { Transcriber },
    './g2-pcm-media-stream': { G2PcmMediaStream: { create: async () => { creates += 1; return adapter } } },
    './candidate-audio-window': { CandidateAudioWindow },
    './moonshine-diagnostics': { MoonshineDiagnostics: Diagnostics },
  }
  function load(name) {
    if (mocks[name]) return mocks[name]
    name = name.replace(/\.js$/, '')
    if (cache.has(name)) return cache.get(name)
    const filename = path.join(__dirname, '../src/even', `${name}.ts`)
    const code = ts.transpileModule(readFileSync(filename, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
    }).outputText
    const exports = {}
    cache.set(name, exports)
    vm.runInNewContext(code, { exports, require: load, console: { info() {}, warn() {} }, setTimeout, clearTimeout }, { filename })
    return exports
  }
  const { MoonshineShadowTranscriber } = load('./moonshine-shadow')
  const shadow = new MoonshineShadowTranscriber({ debugTranscripts: true })
  return { shadow, adapter, events, load, failNextLoad: () => { failLoad = true }, creates: () => creates, transcriber: () => transcriber }
}

test('failed initialization can be retried without rebuilding the app instance', async () => {
  const f = fixture()
  f.failNextLoad()
  await f.shadow.prepare()
  assert.equal(f.shadow.isReady(), false)
  await f.shadow.prepare()
  assert.equal(f.shadow.isReady(), true)
  assert.equal(f.creates(), 2)
  f.shadow.dispose()
})

test('start checks both audio contexts instead of trusting cached running state', async () => {
  const f = fixture()
  assert.equal(await f.shadow.start(), true)
  f.adapter.state = 'suspended'
  f.transcriber().audioContext.state = 'suspended'
  assert.equal(await f.shadow.start(), true)
  assert.equal(f.adapter.state, 'running')
  assert.equal(f.transcriber().audioContext.state, 'running')
  assert.equal(f.transcriber().starts, 2)
  assert.ok(f.events.includes('audio context interrupted; restarting'))
  f.shadow.dispose()
})

test('local capture starts without a backend socket', async () => {
  const f = fixture()
  const { CandidateAudioClient } = f.load('./candidate-audio-client')
  const client = new CandidateAudioClient({
    candidateAudioEnabled: true, moonshineEnabled: true,
    debugTranscripts: false, forwardDiagnostics: false,
    getSocket: () => undefined,
    device: { start: async () => true, stop: async () => true },
  })
  assert.equal(await client.prepare(), true)
  assert.equal(await client.startCapture(), true)
  client.push(new Uint8Array([0, 0]))
  assert.equal(client.snapshot().pcmFrames, 1)
  client.dispose()
})
