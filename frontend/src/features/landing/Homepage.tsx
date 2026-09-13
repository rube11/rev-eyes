export function Homepage() {
  return (
    <main className="landing">
      <header className="landing__header">
        <a className="landing__brand" href="#">rev/eyes</a>
        <a className="landing__sign-in" href="#sign-in">Sign in <span aria-hidden="true">↗</span></a>
      </header>
      <section className="landing__hero">
        <p className="landing__eyebrow">For Even G2 glasses and the web · Private beta</p>
        <h1>Your assistant, in sight.</h1>
        <p className="landing__intro">Keep the conversation going. rev/eyes helps you remember what matters, find answers, and follow up—from your glasses to your browser.</p>
        <a className="landing__cta" href="#sign-in">Sign in to your assistant <span aria-hidden="true">↗</span></a>
      </section>
      <section className="landing__features" aria-label="What rev/eyes does">
        <article><span>01 / Converse</span><h2>Ask as you go.</h2><p>Speak through your glasses or type in your workspace. Keep your conversations together.</p></article>
        <article><span>02 / Remember</span><h2>Pick up where you left off.</h2><p>Save useful details and bring them into future conversations. Review, correct, or forget them when you choose.</p></article>
        <article><span>03 / Follow up</span><h2>Keep the next step in sight.</h2><p>Confirm reminders and ongoing watches, then manage them alongside your memories and conversations.</p></article>
      </section>
    </main>
  )
}
