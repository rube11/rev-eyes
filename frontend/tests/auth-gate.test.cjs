const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const ts = require('typescript')
const React = require('react')
const { renderToStaticMarkup } = require('react-dom/server')

// Render the actual app branches without network, native bridge, or effects.
function renderGate(options = {}) {
  const { storageError = '', hash = '', sessionError = '' } = options
  const session = Object.hasOwn(options, 'session') ? options.session : null
  let stateIndex = 0
  const react = {
    ...React,
    useSyncExternalStore: () => storageError,
    useState(initial) {
      const index = stateIndex++
      return React.useState(index === 0 ? sessionError : index === 1 ? session : initial)
    },
  }
  const load = (file) => {
    const code = ts.transpileModule(fs.readFileSync(file, 'utf8'), {
      compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX },
    }).outputText
    const exports = {}
    const localRequire = (name) => {
      if (name === 'react') return react
      if (name === 'react/jsx-runtime') return require(name)
      if (name.endsWith('/Homepage') || name.endsWith('/SignIn')) {
        return load(path.resolve(path.dirname(file), `${name}.tsx`))
      }
      if (name.endsWith('/connection-session')) return { ConnectionSession: class {} }
      if (name.endsWith('/supabase')) return { sessionStorage: { subscribe() {}, getError() {} } }
      return {}
    }
    new Function('require', 'exports', 'window', code)(localRequire, exports, {
      location: { search: '', hash },
    })
    return exports
  }
  const { default: App } = load(path.resolve(__dirname, '../src/app/App.tsx'))
  return renderToStaticMarkup(React.createElement(App))
}

test('visitors without a saved session see the public homepage', () => {
  assert.match(renderGate(), /Your assistant, in sight/)
})
test('unavailable saved storage shows the homepage instead of a blocking error', () => {
  const html = renderGate({ storageError: 'Saved sign-in storage is unavailable. Reopen the app to retry.' })
  assert.match(html, /Your assistant, in sight/)
  assert.doesNotMatch(html, /Reload app|Saved sign-in storage/)
})
test('a restored session proceeds to the workspace', () => {
  const html = renderGate({ session: { access_token: 'test', user: { id: 'test-user' } } })
  assert.match(html, /Opening your assistant/)
  assert.doesNotMatch(html, /Your assistant, in sight/)
})
test('restoration remains pending until the session check finishes', () => {
  const html = renderGate({ session: undefined })
  assert.match(html, /Checking your account/)
  assert.doesNotMatch(html, /Your assistant, in sight/)
})
test('sign in remains available from the homepage', () => {
  assert.match(renderGate({ hash: '#sign-in' }), /type="password"/)
})
test('the website deploys the browser build', () => {
  assert.equal(JSON.parse(fs.readFileSync(path.resolve(__dirname, '../vercel.json'))).buildCommand, 'pnpm build')
})

test('a failed restoration returns to the homepage', () => {
  assert.match(renderGate({ sessionError: 'Could not restore your sign-in.' }), /Your assistant, in sight/)
})
test('storage failure takes precedence over an in-memory session', () => {
  assert.match(renderGate({ storageError: 'Unavailable', session: { user: { id: 'test' } } }), /Your assistant, in sight/)
})
