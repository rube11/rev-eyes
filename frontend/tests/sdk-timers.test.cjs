const assert = require('node:assert/strict')
const fs = require('node:fs')
const test = require('node:test')
const vm = require('node:vm')

// Load the real SDK as a browser module: window must exist before import so
// its native-background shadow timers are installed, unlike display mocks.
function browserTimers() {
  let sequence = 0
  const native = new Map()
  const schedule = (callback, delay, repeat) => {
    const id = ++sequence
    native.set(id, { callback, delay, repeat })
    return id
  }
  const browser = {
    exports: {},
    console: { log() {}, warn() {}, error() {} },
    setTimeout: (callback, delay) => schedule(callback, delay, false),
    setInterval: (callback, delay) => schedule(callback, delay, true),
    clearTimeout: id => native.delete(id),
    clearInterval: id => native.delete(id),
    addEventListener() {},
    document: { addEventListener() {} },
    flutter_inappwebview: { callHandler: async () => true },
  }
  browser.window = browser
  vm.createContext(browser)
  const sdkFile = process.env.EVEN_SDK_TEST_MODULE || require.resolve('@evenrealities/even_hub_sdk')
  vm.runInContext(fs.readFileSync(sdkFile, 'utf8'), browser, { filename: sdkFile })
  return {
    browser,
    resumeNative() {
      for (const [id, timer] of [...native]) {
        if (!native.has(id)) continue
        if (!timer.repeat) native.delete(id)
        timer.callback()
      }
    },
  }
}

test('Even SDK does not rerun an elapsed background timeout when native timers resume', () => {
  const { browser, resumeNative } = browserTimers()
  let calls = 0
  browser.setTimeout(() => { calls += 1 }, 250)
  browser.__tickShadowTimers(250)
  assert.equal(calls, 1)
  resumeNative()
  assert.equal(calls, 1, 'one-shot transcript timers must remain one-shot after app backgrounding')
})

test('Even SDK does not execute newly scheduled timers within the same background tick', () => {
  const { browser } = browserTimers()
  let calls = 0
  function next() {
    calls += 1
    // Keep the buggy SDK finite; the app has continuously recurring timers.
    if (calls < 5) browser.setTimeout(next, 800)
  }
  browser.setTimeout(next, 800)
  browser.__tickShadowTimers(800)
  assert.equal(calls, 1, 'an 800ms tick must not consume future recurring timer periods')
})
