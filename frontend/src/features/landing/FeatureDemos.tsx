import { useId, useRef, useState } from 'react'
import type { KeyboardEvent } from 'react'

// Illustrative conversations, not live recommendations or scheduled actions.
const demos = [
  {
    id: 'memory',
    label: 'Memory',
    prompt: 'Hey, Luke’s in town. Find a nice Italian restaurant near my house where I can invite him.',
    context: ['Your home neighborhood', 'Luke prefers quiet restaurants'],
    response: 'Luca, an Italian spot eight minutes from home, has a quiet patio Luke would like. Want me to draft an invite?',
  },
  {
    id: 'reminders',
    label: 'Reminders',
    prompt: 'Dinner with Luke is tomorrow at 7 PM. Remind me to bring his book.',
    response: 'Remind you at 6:30 PM tomorrow, so you have the book before dinner?',
    followup: {
      when: 'Tomorrow, 6:30 PM',
      message: 'Bring Luke’s book. Dinner is at 7.',
    },
  },
  {
    id: 'watches',
    label: 'Watches',
    prompt: 'Watch nonstop flights from New York to Rome. Let me know if one drops below $500.',
    response: 'Check those fares every six hours for two weeks and alert you below $500?',
    followup: {
      when: 'Two days later',
      message: 'Found a $460 nonstop to Rome. Under your $500 limit.',
    },
  },
]

export function FeatureDemos() {
  const [active, setActive] = useState(0)
  const id = useId()
  const tabs = useRef<Array<HTMLButtonElement | null>>([])

  function navigateTabs(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const next = event.key === 'ArrowRight' ? (index + 1) % demos.length
      : event.key === 'ArrowLeft' ? (index + demos.length - 1) % demos.length
        : event.key === 'Home' ? 0 : event.key === 'End' ? demos.length - 1 : undefined
    if (next === undefined) return
    event.preventDefault()
    setActive(next)
    tabs.current[next]?.focus()
  }

  return (
    <div className="re-feature-demos">
      <div className="re-feature-tabs" role="tablist" aria-label="Feature demos">
        {demos.map((demo, index) => (
          <button key={demo.id} id={`${id}-${demo.id}-tab`} type="button" role="tab"
            aria-selected={active === index} aria-controls={`${id}-${demo.id}-panel`}
            tabIndex={active === index ? 0 : -1}
            ref={(element) => { tabs.current[index] = element }}
            onClick={() => setActive(index)} onKeyDown={(event) => navigateTabs(event, index)}>
            {demo.label}
          </button>
        ))}
      </div>
      {demos.map((demo, index) => (
        <div key={demo.id} className="re-feature-panel" id={`${id}-${demo.id}-panel`}
          role="tabpanel" aria-labelledby={`${id}-${demo.id}-tab`} tabIndex={0} hidden={active !== index}>
          <div className="re-demo-prompt">
            <p className="re-demo-speaker">You</p>
            <blockquote>{demo.prompt}</blockquote>
            {demo.context ? <div className="re-demo-context">
              <p>Saved context</p>
              <ul>{demo.context.map((item) => <li key={item}>{item}</li>)}</ul>
            </div> : null}
          </div>
          <div className="re-demo-response">
            <p className="re-demo-speaker">rev<span>eyes</span></p>
            <p className="re-demo-answer">{demo.response}</p>
            {demo.followup ? <div className="re-demo-followup">
              <p className="re-demo-timing">After you confirm <span>· {demo.followup.when}</span></p>
              <p className="re-demo-notification">{demo.followup.message}</p>
            </div> : null}
          </div>
        </div>
      ))}
    </div>
  )
}
