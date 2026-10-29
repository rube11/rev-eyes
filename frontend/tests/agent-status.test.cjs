const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const ts = require('typescript')
const React = require('react')
const { renderToStaticMarkup } = require('react-dom/server')
function load(relative) {
  const file = path.resolve(__dirname, '../src', relative)
  const code = ts.transpileModule(fs.readFileSync(file, 'utf8'), { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }).outputText
  const exports = {}
  const localRequire = name => {
    if (name.endsWith('.css')) return {}
    if (name.endsWith('/env')) return { env: { apiBaseUrl: 'https://example.invalid' } }
    if (name.startsWith('.')) {
      const base = path.resolve(path.dirname(file), name)
      return load(path.relative(path.resolve(__dirname, '../src'), base + (fs.existsSync(base + '.tsx') ? '.tsx' : '.ts')))
    }
    return require(name)
  }
  new Function('require', 'exports', code)(localRequire, exports)
  return exports
}
const { AgentStatusStore, parseAgentConnections } = load('features/workspace/agentStatus.ts')
const { AgentStatusPanel } = load('features/workspace/views/AgentsView.tsx')
const payload = (threads = [], extra = {}) => ({ limit: 20, connections: [{ connection_id: 't3', provider: 'T3', capabilities: { list: true, send: false, start: false, interrupt: false, read: false }, untrusted_provider_data: { threads, ...extra } }] })
const render = snapshot => renderToStaticMarkup(React.createElement(AgentStatusPanel, { snapshot, onRefresh() {} }))
const tick = () => new Promise(resolve => setImmediate(resolve))

test('defensive provider parsing caps pages and treats malformed/offline status as unavailable', () => {
  assert.deepEqual(parseAgentConnections({ limit: 20, connections: [] }), [])
  assert.equal(parseAgentConnections(payload(Array.from({ length: 21 }, () => ({}))))[0].unavailable, true)
  const offline = payload(); offline.connections[0].error = 'unavailable'
  assert.equal(parseAgentConnections(offline)[0].unavailable, true)
  assert.throws(() => parseAgentConnections({ connections: [] }))
  const result = parseAgentConnections(payload([{ threadId: 'a', title: '<script>bad</script>', status: 'working' }], { nextCursor: 20 }))[0]
  assert.equal(result.hasMore, true)
  assert.equal(result.threads[0].status, 'working')
})

test('all visible states are truthful; no unsupported action controls', () => {
  for (const phase of ['loading', 'error', 'ready']) {
    const html = render({ phase, connections: [] })
    assert.match(html, /Read-only preview/)
    assert.match(html, /Not supported/)
    assert.match(html, /This placeholder is not connected/)
    assert.doesNotMatch(html, /<button[^>]*>\s*(Send|Start|Interrupt|Connect|Pair)/)
    assert.match(html, phase === 'loading' ? /Checking your configured connection/ : phase === 'error' ? /Couldn’t load agent status/ : /Not configured for this account/)
  }
  const empty = render({ phase: 'ready', connections: parseAgentConnections(payload()) })
  assert.match(empty, /No threads returned on this page/)
  assert.match(empty, /not a complete inventory/)
  const full = render({ phase: 'ready', connections: parseAgentConnections(payload([{ title: '<img src=x>', status: 'working', threadId: 'id' }], { nextCursor: 20 })) })
  assert.match(full, /&lt;img src=x&gt;/)
  assert.match(full, /More threads are available/)
  assert.match(full, /Pagination is not available/)
  const offline = payload(); offline.connections[0].error = 'unavailable'
  assert.match(render({ phase: 'ready', connections: parseAgentConnections(offline) }), /provider may be offline/)
})

test('refresh is single-flight, clears previous status, and handles failure', async () => {
  let resolve, calls = 0
  const store = new AgentStatusStore(() => { calls++; return new Promise(done => { resolve = done }) })
  const first = store.refresh(); void store.refresh()
  assert.equal(calls, 1)
  assert.equal(store.getSnapshot().phase, 'loading')
  resolve(payload()); await first
  assert.equal(store.getSnapshot().phase, 'ready')
  const second = store.refresh()
  assert.deepEqual(store.getSnapshot().connections, [])
  resolve({ bad: 'shape' }); await second
  assert.equal(store.getSnapshot().phase, 'error')
  store.dispose()
})

test('navigation/disposal aborts requests and ignores late results; effect restart is safe', async () => {
  const requests = []
  const store = new AgentStatusStore(signal => new Promise(resolve => requests.push({ signal, resolve })))
  store.start(); store.dispose()
  assert.equal(requests[0].signal.aborted, true)
  store.start()
  requests[0].resolve(payload([{ threadId: 'old-owner' }]))
  await tick()
  assert.equal(store.getSnapshot().phase, 'loading')
  requests[1].resolve({ limit: 20, connections: [] })
  await tick()
  assert.equal(store.getSnapshot().phase, 'ready')
  assert.deepEqual(store.getSnapshot().connections, [])
  store.dispose()
  await store.refresh()
  assert.equal(requests.length, 2)
})

// Representative list-item fields from official orchestratorMcp.ts ThreadListItem;
// extra metadata must not become links, actions, or inferred permissions.
test('upstream T3 list fixture displays top-level status and ignores authority-like metadata', () => {
  const fixture = payload([{threadId:'thread-1',projectId:'project-1',title:'Review UI',status:'running',latestRunId:'run-1',providerInstanceId:'provider-1',model:'model',runtimeMode:'full-access',interactionMode:'autonomous',linkedPullRequest:null,parentThreadId:null,relationshipToParent:null,itemCount:4,settled:false,settledAt:null,snoozed:false,snoozedUntil:null,createdAt:'2026-10-29T00:00:00Z',updatedAt:'2026-10-29T00:01:00Z',link:'javascript:alert(1)'}], {nextCursor:20,total:30})
  const html = render({phase:'ready',connections:parseAgentConnections(fixture)})
  assert.match(html,/running/)
  assert.doesNotMatch(html,/javascript:|full-access|autonomous/)
})

test('timeout aborts the browser request and clears loading', async () => {
  let signal
  const store = new AgentStatusStore(nextSignal => { signal = nextSignal; return new Promise((_resolve,reject) => { nextSignal.addEventListener('abort',()=>reject(new Error('aborted'))) }) }, 1)
  await store.refresh()
  assert.equal(signal.aborted,true)
  assert.equal(store.getSnapshot().phase,'error')
  store.dispose()
})

test('a replacement token/owner store cannot display the previous request result', async () => {
  let resolveOld
  const oldStore = new AgentStatusStore(() => new Promise(resolve => {resolveOld = resolve}))
  const pending = oldStore.refresh()
  oldStore.dispose()
  const newStore = new AgentStatusStore(async () => ({limit:20,connections:[]}))
  await newStore.refresh()
  resolveOld(payload([{threadId:'private-old-thread',title:'Previous owner'}])); await pending
  assert.deepEqual(newStore.getSnapshot().connections,[])
  assert.equal(oldStore.getSnapshot().phase,'loading')
  newStore.dispose()
})
