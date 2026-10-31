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
  const result = parseAgentConnections(payload([{ threadId: 'a', title: '<script>bad</script>', status: 'running' }], { nextCursor: 20 }))[0]
  assert.equal(result.hasMore, true)
  assert.equal(result.threads[0].status, 'running')
})

test('visible states distinguish read-only, control-enabled and disconnected accounts', () => {
  for (const phase of ['loading', 'error', 'ready']) {
    const html = render({ phase, connections: [] })
    assert.match(html, /Dots isn’t connected yet/)
    assert.doesNotMatch(html.replace(/<[^>]*>/g, ''), /Connection research|inventory|provider|Not configured/)
    assert.doesNotMatch(html, /<button[^>]*>\s*(Send|Start|Interrupt|Connect|Pair)/)
    assert.match(html, phase === 'loading' ? /Loading threads/ : phase === 'error' ? /Couldn’t load threads/ : /T3 isn’t connected to this account/)
  }
  const empty = render({ phase: 'ready', connections: parseAgentConnections(payload()) })
  assert.match(empty, /No threads on this page/)
  const full = render({ phase: 'ready', connections: parseAgentConnections(payload([{ title: '<img src=x>', status: 'running', threadId: 'id' }], { nextCursor: 20 })) })
  assert.match(full, /&lt;img src=x&gt;/)
  assert.match(full, /More threads/)
  assert.match(full, /View only/)
  assert.match(full, /disabled/)
  const offline = payload(); offline.connections[0].error = 'unavailable'
  assert.match(render({ phase: 'ready', connections: parseAgentConnections(offline) }), /Couldn’t reach T3/)
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
  assert.match(html,/Working/)
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


test('pagination retains existing threads, deduplicates IDs and uses the provider cursor', async () => {
  const requests = []
  const store = new AgentStatusStore(async (_signal, page) => {
    requests.push(page)
    return page ? payload([{threadId:'b',title:'Next'},{threadId:'a',title:'Updated'}], {nextCursor:null}) : payload([{threadId:'a',title:'First'}], {nextCursor:20})
  })
  await store.refresh(); await store.loadMore('t3')
  assert.deepEqual(requests, [undefined, {connectionId:'t3',cursor:20}])
  assert.deepEqual(store.getSnapshot().connections[0].threads.map(row => row.title), ['Updated','Next'])
  assert.equal(store.getSnapshot().connections[0].hasMore,false)
  store.dispose()
})

test('control cards preserve opaque T3 IDs and expose only implemented capabilities', () => {
  const data = payload([{threadId:'mcp:launch:one',title:'Fix audio',status:'running'}])
  data.connections[0].capabilities.send = true
  data.connections[0].capabilities.open = true
  const connections = parseAgentConnections(data)
  assert.equal(connections[0].threads[0].id,'mcp:launch:one')
  const html = renderToStaticMarkup(React.createElement(AgentStatusPanel,{snapshot:{phase:'ready',connections},onRefresh(){},onSelect(){}}))
  assert.match(html,/Connected/)
  assert.doesNotMatch(html,/<button[^>]*disabled/)
  assert.equal(parseAgentConnections(payload([{threadId:'https://evil'}]))[0].unavailable,true)
})

test('agent voice socket uses the selected target and a one-use ticket, never a provider credential', async () => {
  const previousFetch = global.fetch
  const previousSocket = global.WebSocket
  const requests = []
  const sockets = []
  global.fetch = async (url, options) => { requests.push({url,options}); return {ok:true,json:async () => ({ticket:'single-use-ticket'})} }
  global.WebSocket = class {
    constructor(url) { this.url=url; this.listeners=new Map(); sockets.push(this); queueMicrotask(() => this.listeners.get('open')?.()) }
    addEventListener(type, listener) { this.listeners.set(type,listener) }
    removeEventListener(type, listener) { if (this.listeners.get(type) === listener) this.listeners.delete(type) }
    close() {}
  }
  try {
    const { connectRealtimeSocket } = load('shared/api/client.ts')
    await connectRealtimeSocket('eyes-session-token',{agentTarget:{connectionId:'laptop',threadId:'mcp:launch:chosen'}})
    assert.equal(sockets[0].url.pathname,'/agents/voice')
    assert.equal(sockets[0].url.searchParams.get('thread_id'),'mcp:launch:chosen')
    assert.equal(sockets[0].url.searchParams.get('connection_id'),'laptop')
    assert.equal(sockets[0].url.searchParams.get('ticket'),'single-use-ticket')
    assert.doesNotMatch(sockets[0].url.toString(),/eyes-session-token/)
    assert.equal(requests[0].options.headers.Authorization,'Bearer eyes-session-token')
    await connectRealtimeSocket('eyes-session-token')
    assert.equal(sockets[1].url.pathname,'/ws')
  } finally { global.fetch=previousFetch;global.WebSocket=previousSocket }
})

test('typed agent messages preserve target and retry key; uncertain delivery stays uncertain', async () => {
  const previousFetch = global.fetch
  const requests = []
  global.fetch = async (url, options) => { requests.push({url,options}); return {ok:true,json:async () => ({threadId:'chosen',delivery:'queued'})} }
  try {
    const {sendAgentMessage,openAgentThread} = load('features/workspace/agentClient.ts')
    const target={connectionId:'laptop',threadId:'chosen',title:'Fix it'}
    await sendAgentMessage('session',target,'Do the work','stable-key')
    assert.deepEqual(JSON.parse(requests[0].options.body),{connection_id:'laptop',thread_id:'chosen',text:'Do the work',request_id:'stable-key'})
    global.fetch=async () => ({ok:false,json:async () => ({error:'delivery_unknown'})})
    await assert.rejects(sendAgentMessage('session',target,'Do the work','stable-key'),/Couldn’t confirm delivery/)
    global.fetch=async () => ({ok:true,json:async () => ({thread:{threadId:'wrong'},items:[]})})
    await assert.rejects(openAgentThread('session',target),/Couldn’t read this thread/)
  } finally {global.fetch=previousFetch}
})


test('uncertain sends retain their key after edits and thread changes', () => {
  const {AgentSendKeys}=load('features/workspace/agentSendKeys.ts')
  let next=0
  const keys=new AgentSendKeys(()=>`key-${++next}`)
  const first={connectionId:'one',threadId:'task',title:'Task'}
  const second={connectionId:'two',threadId:'task',title:'Other task'}
  const attempt=keys.prepare(first,'Fix audio')
  assert.notEqual(keys.prepare(first,'Fix display').key,attempt.key)
  assert.notEqual(keys.prepare(second,'Fix audio').key,attempt.key)
  assert.equal(keys.prepare(first,'Fix audio').key,attempt.key)
  keys.complete(attempt.signature)
  assert.notEqual(keys.prepare(first,'Fix audio').key,attempt.key)
})

test('thread reads distinguish a recent refresh from an explicit history request', async () => {
  const previousFetch=global.fetch
  const bodies=[]
  global.fetch=async (_url,options)=>{
    bodies.push(JSON.parse(options.body))
    return {ok:true,json:async()=>({thread:{threadId:'chosen'},items:[],hasEarlier:true,nextPosition:99})}
  }
  try {
    const {openAgentThread}=load('features/workspace/agentClient.ts')
    const target={connectionId:'laptop',threadId:'chosen',title:'Task'}
    const recent=await openAgentThread('session',target)
    await openAgentThread('session',target,undefined,undefined,true)
    assert.equal(recent.hasEarlier,true)
    assert.equal(bodies[0].after_position,undefined)
    assert.equal(bodies[1].from_start,true)
  } finally {global.fetch=previousFetch}
})
