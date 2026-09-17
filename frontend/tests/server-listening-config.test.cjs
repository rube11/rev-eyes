const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const vm = require('node:vm')
const ts = require('typescript')

function config(flag) {
  const source = fs.readFileSync('src/shared/config/env.ts', 'utf8').replaceAll('import.meta.env', 'settings')
  const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } }).outputText
  const context = { exports: {}, settings: { VITE_API_BASE_URL: 'https://example.test', VITE_SUPABASE_URL: 'https://example.test', VITE_SUPABASE_PUBLISHABLE_KEY: 'fixture', VITE_SERVER_LISTENING_ENABLED: flag } }
  vm.runInNewContext(code, context)
  return context.exports.env
}
test('server listening is enabled when beta packaging supplies no flag', () => {
  assert.equal(config(undefined).serverListeningEnabled, true)
  assert.equal(config('').serverListeningEnabled, true)
  assert.equal(config('true').serverListeningEnabled, true)
})
test('manual listening requires an explicit false override', () => {
  assert.equal(config(' false ').serverListeningEnabled, false)
  assert.equal(config('FALSE').serverListeningEnabled, false)
})
