import type { ReactNode } from 'react'
import './PageIntro.css'

export function PageIntro({
  title,
  eyebrow,
  lede,
  action,
}: {
  title: string
  eyebrow?: ReactNode
  lede?: ReactNode
  action?: ReactNode
}) {
  return (
    <header className="page-intro">
      <div className="page-intro__copy">
        {eyebrow ? <p className="eyebrow">{eyebrow}</p> : null}
        <h1>{title}</h1>
        {lede ? <p className="page-intro__lede">{lede}</p> : null}
      </div>
      {action ? <div className="page-intro__action">{action}</div> : null}
    </header>
  )
}
