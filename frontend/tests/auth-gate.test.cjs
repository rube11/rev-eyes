const assert = require('node:assert/strict')
const test = require('node:test')
const fs = require('node:fs')
const path = require('node:path')
const ts = require('typescript')
const React = require('react')
const { renderToStaticMarkup } = require('react-dom/server')
const homepageMarker = /aria-label="Discover rev eyes as you scroll"/

// Render the actual app branches without network, native bridge, or effects.
function renderGate(options = {}) {
  const { storageError = '', hash = '', sessionError = '', pathname = '/' } = options
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
      if (name.endsWith('/Homepage') || name.endsWith('/SignIn') || name.endsWith('/ProactiveHero') || name.endsWith('/FeatureDemos') || name.endsWith('/BetaWaitlist')) {
        return load(path.resolve(path.dirname(file), `${name}.tsx`))
      }
      if (name.endsWith('/mockup-projection')) return load(path.resolve(path.dirname(file), `${name}.ts`))
      if (name.endsWith('/connection-session')) return { ConnectionSession: class {} }
      if (name.endsWith('/supabase')) return { sessionStorage: { subscribe() {}, getError() {} } }
      return {}
    }
    new Function('require', 'exports', 'window', code)(localRequire, exports, {
      location: { search: '', hash, pathname },
    })
    return exports
  }
  const { default: App } = load(path.resolve(__dirname, '../src/app/App.tsx'))
  return renderToStaticMarkup(React.createElement(App))
}

test('visitors without a saved session see the public homepage', () => {
  assert.match(renderGate(), homepageMarker)
})
test('unavailable saved storage shows the homepage instead of a blocking error', () => {
  const html = renderGate({ storageError: 'Saved sign-in storage is unavailable. Reopen the app to retry.' })
  assert.match(html, homepageMarker)
  assert.doesNotMatch(html, /Reload app|Saved sign-in storage/)
})
test('a restored session proceeds to the workspace', () => {
  const html = renderGate({ session: { access_token: 'test', user: { id: 'test-user' } } })
  assert.match(html, /Loading…/)
  assert.doesNotMatch(html, homepageMarker)
})
test('restoration remains pending until the session check finishes', () => {
  const html = renderGate({ session: undefined })
  assert.match(html, /Checking your account/)
  assert.doesNotMatch(html, homepageMarker)
})
test('sign in remains available from the homepage', () => {
  assert.match(renderGate({ hash: '#sign-in' }), /type="password"/)
})
test('the website deploys the browser build', () => {
  assert.equal(JSON.parse(fs.readFileSync(path.resolve(__dirname, '../vercel.json'))).buildCommand, 'pnpm build')
})

test('a failed restoration returns to the homepage', () => {
  assert.match(renderGate({ sessionError: 'Could not restore your sign-in.' }), homepageMarker)
})
test('storage failure takes precedence over an in-memory session', () => {
  assert.match(renderGate({ storageError: 'Unavailable', session: { user: { id: 'test' } } }), homepageMarker)
})

test('the homepage presents the response in the glasses, not a detached card', () => {
  const html = renderGate()
  assert.match(html, /src="\/glasses\/even-g2-mockup.webp"/)
  assert.match(html, /class="re-lens-interface"/)
  assert.match(html, /data-lens="right"/)
  assert.match(html, /class="re-device-crop"/)
  const lensPath = html.match(/<clipPath[^>]*><path d="([^"]+)"/)?.[1]
  assert.ok(lensPath, 'The mockup must clip its display to the lens')
  const lensXs = [...lensPath.matchAll(/(?:M|L)\s+(-?[\d.]+)\s+/g)].map((point) => Number(point[1]))
  assert.ok(lensXs.length > 3 && lensXs.every((x) => x > 500), 'The display must use the right lens of the full 1000-unit render')
  assert.match(html, /aria-live="polite"/)
  assert.equal((html.match(/aria-label="Show /g) || []).length, 3)
  assert.doesNotMatch(html, /re-agent-response|re-response-stack/)
})

test('the lens lettering is live vector text outside the masked hardware layer', () => {
  const html = renderGate()
  assert.ok(/alt="Right lens and graphite frame detail of the glasses"\/><\/div><\/div><div class="re-display-layer"><svg/.test(html), 'The vector display must be a sibling of the cropped hardware, not its masked child')
  assert.match(html, /<text class="re-hud-heading"[^>]*>- Reminder<\/text>/)
  assert.doesNotMatch(html, /re-hud-time|re-hud-brand|<filter|<feGaussianBlur|<radialGradient/)
})

test('concise landing copy keeps feature, demo, and illustration context', () => {
  const html = renderGate()
  for (const feature of ['Memory', 'Reminders', 'Watches']) {
    assert.match(html, new RegExp(`role="tab"[^>]*>${feature}</button>`))
  }
  assert.match(html, /href="\/join">Join the beta/)
  assert.match(html, /href="#sign-in">Sign in/)
  assert.match(html, /PRIVATE BETA/)
  assert.match(html, /Illustrative display/)
  const topNavigation = html.match(/<header class="re-site-nav">([\s\S]*?)<\/header>/)?.[1]
  assert.ok(topNavigation)
  assert.match(topNavigation, /aria-label="reveyes home">rev<span>eyes<\/span><\/a>/)
  assert.doesNotMatch(topNavigation, /re-wordmark/)
  assert.doesNotMatch(html, /re-next-moment|re-device-caption|re-story-count/)
})

test('feature demos show remembered context and confirmation before later alerts', () => {
  const html = renderGate()
  assert.match(html, /Illustrative conversations/)
  const tabs = [...html.matchAll(/<button[^>]*role="tab"[^>]*>/g)].map((match) => match[0])
  const panels = [...html.matchAll(/<div[^>]*role="tabpanel"[^>]*>/g)].map((match) => match[0])
  assert.equal(tabs.length, 3)
  assert.equal(tabs.filter((tab) => tab.includes('aria-selected="true"')).length, 1)
  assert.equal(panels.length, 3)
  assert.equal(panels.filter((panel) => panel.includes('hidden')).length, 2)
  for (const tab of tabs) {
    const tabId = tab.match(/id="([^"]+)"/)[1]
    const panelId = tab.match(/aria-controls="([^"]+)"/)[1]
    assert.ok(panels.some((panel) => panel.includes(`id="${panelId}"`) && panel.includes(`aria-labelledby="${tabId}"`)))
  }
  assert.match(html, /Luke’s in town/)
  assert.match(html, /Saved context/)
  assert.match(html, /Luke prefers quiet restaurants/)
  assert.match(html, /draft an invite\?/)
  assert.equal((html.match(/After you confirm/g) || []).length, 2)
  assert.match(html, /Bring Luke’s book/)
  assert.match(html, /Found a \$460 nonstop to Rome/)
})


test('the waitlist page is accessible without an account or restored storage', () => {
  const html = renderGate({ pathname: '/join', session: undefined })
  assert.match(html, /Join the private beta/)
  assert.match(html, /type="email"/)
  assert.match(html, /Request access/)
  assert.doesNotMatch(html, /Checking your account|type="password"/)
})
