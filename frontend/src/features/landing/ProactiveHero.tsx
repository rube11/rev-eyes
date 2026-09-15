import { useEffect, useId, useRef, useState } from 'react'
import { mockupProjection } from './mockup-projection'
import './proactive-hero.css'

const chapters = [
  {
    label: 'A little nudge',
    header: 'Reminder',
    title: 'Focus on',
    continuation: 'what matters.',
    message: ['Pick up groceries', 'on your way home.', 'You’re low on oat milk.'],
    prompt: 'Add it to the list?',
  },
  {
    label: 'Make it personal',
    header: 'Memory',
    title: 'Small details.',
    continuation: 'Big meaning.',
    message: ['Joe likes an oat flat white.', 'He takes it without sugar.'],
    prompt: 'Remind you before lunch?',
  },
  {
    label: 'Don’t miss it',
    header: 'Watch update',
    title: 'More living.',
    continuation: 'Less checking.',
    message: ['The jazz show you asked', 'me to watch is on sale.', 'Friday, 7 pm.'],
    prompt: 'Remind you to buy tonight?',
  },
]

export function ProactiveHero() {
  const [active, setActive] = useState(0)
  const track = useRef<HTMLElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const clipId = useId()
  const chapter = chapters[active]

  useEffect(() => {
    let frame = 0
    function measure() {
      frame = 0
      if (!track.current) return
      const bounds = track.current.getBoundingClientRect()
      const distance = Math.max(1, bounds.height - (stage.current?.offsetHeight ?? window.innerHeight))
      const progress = Math.max(0, Math.min(1, -bounds.top / distance))
      setActive(Math.min(chapters.length - 1, Math.floor(progress * chapters.length)))
    }
    function queueMeasure() {
      if (!frame) frame = window.requestAnimationFrame(measure)
    }
    const observer = new ResizeObserver(queueMeasure)
    if (track.current) observer.observe(track.current)
    queueMeasure()
    window.addEventListener('scroll', queueMeasure, { passive: true })
    window.addEventListener('resize', queueMeasure)
    return () => {
      window.cancelAnimationFrame(frame)
      observer.disconnect()
      window.removeEventListener('scroll', queueMeasure)
      window.removeEventListener('resize', queueMeasure)
    }
  }, [])

  function goToChapter(index: number) {
    if (!track.current) return
    const top = track.current.getBoundingClientRect().top + window.scrollY
    const distance = Math.max(1, track.current.offsetHeight - (stage.current?.offsetHeight ?? window.innerHeight))
    const progress = index === 0 ? 0 : (index + .12) / chapters.length
    window.scrollTo({
      top: top + distance * progress,
      behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth',
    })
  }

  return (
    <section className="re-scroll-story" id="experience" ref={track} aria-label="Discover rev eyes as you scroll">
      <div className="re-cinema-stage" ref={stage} data-chapter={active}>
        <div className="re-atmosphere" aria-hidden="true"><div /><div /></div>
        <div className="re-cinema-layout">
          <div className="re-story-copy" key={'copy-' + active}>
            <h1>{chapter.title}{chapter.continuation ? <><br /><span>{chapter.continuation}</span></> : null}</h1>
            <div className="re-story-actions">
              <a className="re-beta-button" href="/join">Join the beta</a>
            </div>
          </div>

          <figure className="re-device-scene" aria-label="Illustrative close-up of the right lens of the glasses with the assistant response" data-lens="right">
            <div className="re-device-crop">
              <div className="re-hardware">
                <img src="/glasses/even-g2-mockup.webp" width="2400" height="1200" fetchPriority="high" alt="Right lens and graphite frame detail of the glasses" />
              </div>
            </div>
            {/* Keep vector lettering out of the hardware's raster mask and CSS transform. */}
            <div className="re-display-layer">
              <svg className="re-hud" viewBox={mockupProjection.viewBox} aria-hidden="true">
                <defs>
                  <clipPath id={clipId} clipPathUnits="userSpaceOnUse"><path d={mockupProjection.clipPath} /></clipPath>
                </defs>
                <g clipPath={'url(#' + clipId + ')'}>
                  <g transform={mockupProjection.transform}>
                    <g className="re-lens-interface" key={active}>
                      <text className="re-hud-heading" x="12" y="20">- {chapter.header}</text>
                      <text className="re-hud-message">{chapter.message.map((line, index) => <tspan key={line} x="12" y={59 + index * 27}>{line}</tspan>)}</text>
                      <text className="re-hud-prompt" x="12" y="153">{chapter.prompt}</text>
                    </g>
                  </g>
                </g>
              </svg>
            </div>
            <figcaption className="re-mockup-caption">Illustrative display</figcaption>
            <p className="re-screen-reader" aria-live="polite" aria-atomic="true">{chapter.header}: {chapter.message.join(' ')} {chapter.prompt}</p>
          </figure>
        </div>
        <div className="re-story-navigation">
          <span className="re-scroll-cue"><span aria-hidden="true">↓</span> SCROLL</span>
          <div className="re-chapter-buttons" aria-label="Choose a moment">{chapters.map((item, index) => <button key={item.label} type="button" aria-pressed={active === index} aria-label={'Show ' + item.label} onClick={() => goToChapter(index)}><span>0{index + 1}</span><i /><span>{item.label}</span></button>)}</div>
        </div>
      </div>
    </section>
  )
}
