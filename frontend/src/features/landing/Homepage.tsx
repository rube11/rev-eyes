import { ProactiveHero } from './ProactiveHero'
import { FeatureDemos } from './FeatureDemos'
import './homepage.css'

export function Homepage() {
  return (
    <main className="re-site" id="top">
      <header className="re-site-nav">
        <a className="re-nav-wordmark" href="#top" aria-label="reveyes home">rev<span>eyes</span></a>
        <nav aria-label="Main navigation">
          <a href="#experience">Overview</a>
          <a href="#everyday">Features</a>
        </nav>
        <a className="re-sign-in" href="#sign-in">Sign in <span aria-hidden="true">↗</span></a>
      </header>

      <ProactiveHero />

      <section className="re-everyday" id="everyday">
        <h2>What it does</h2>
        <p className="re-demo-disclaimer">Illustrative conversations</p>
        <FeatureDemos />
        <div className="re-final-invitation">
          <p>Even G2 + web.</p>
          <span>PRIVATE BETA</span>
        </div>
      </section>
      <footer className="re-site-footer"><a className="re-wordmark" href="#top">rev<span>/</span>eyes</a><a href="#top">Back to top ↑</a></footer>
    </main>
  )
}
