"use client"

import Link from "next/link"
import { ArrowRight } from "lucide-react"
import { useState, type CSSProperties, type KeyboardEvent } from "react"

import styles from "./enterprise-site.module.css"

export type CaseStudy = {
  tab: string
  eyebrow: string
  title: string
  body: string
  metrics?: Array<{ value: string; label: string }>
  link: { href: string; label: string; external?: boolean }
}

const storyDurationMs = 7000

export function CaseStudies({ stories }: { stories: CaseStudy[] }) {
  const [active, setActive] = useState(0)
  const [rotating, setRotating] = useState(true)
  const [paused, setPaused] = useState(false)

  const select = (index: number) => {
    setRotating(false)
    setActive((index + stories.length) % stories.length)
    document.getElementById(`story-tab-${(index + stories.length) % stories.length}`)?.focus()
  }

  const onTabKeyDown = (event: KeyboardEvent) => {
    if (event.key === "ArrowRight") select(active + 1)
    if (event.key === "ArrowLeft") select(active - 1)
  }

  return (
    <section
      className={styles.caseStudies}
      aria-labelledby="case-studies-title"
      onPointerEnter={() => setPaused(true)}
      onPointerLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      style={{ "--story-duration": `${storyDurationMs}ms` } as CSSProperties}
    >
      <header className={styles.caseStudiesHeader}>
        <p className={styles.eyebrow}>Case studies & partners</p>
        <h2 id="case-studies-title">Proven in the operation.</h2>
      </header>

      <div className={styles.caseStudyPanels}>
        {stories.map((item, index) => (
          <div
            className={styles.caseStudyPanel}
            role="tabpanel"
            id={`story-panel-${index}`}
            aria-labelledby={`story-tab-${index}`}
            data-active={index === active ? "" : undefined}
            inert={index !== active}
            key={item.tab}
          >
            <div className={styles.caseStudyCopy}>
              <p className={styles.eyebrow}>{item.eyebrow}</p>
              <h3>{item.title}</h3>
              <p>{item.body}</p>
              {item.link.external ? (
                <a className={styles.inlineArrowLink} href={item.link.href} target="_blank" rel="noopener">
                  {item.link.label} <ArrowRight size={16} aria-hidden="true" />
                </a>
              ) : (
                <Link className={styles.inlineArrowLink} href={item.link.href}>
                  {item.link.label} <ArrowRight size={16} aria-hidden="true" />
                </Link>
              )}
            </div>
            {item.metrics ? (
              <dl className={styles.caseStudyMetrics}>
                {item.metrics.map((metric) => (
                  <div key={metric.label}>
                    <dt>{metric.label}</dt>
                    <dd>{metric.value}</dd>
                  </div>
                ))}
              </dl>
            ) : (
              <p className={styles.caseStudyWordmark} aria-hidden="true">{item.tab}</p>
            )}
          </div>
        ))}
      </div>

      <div className={styles.caseStudyTabs} role="tablist" aria-label="Stories">
        {stories.map((item, index) => (
          <button
            type="button"
            role="tab"
            id={`story-tab-${index}`}
            aria-selected={index === active}
            aria-controls={`story-panel-${index}`}
            tabIndex={index === active ? 0 : -1}
            className={styles.caseStudyTab}
            key={item.tab}
            onClick={() => select(index)}
            onKeyDown={onTabKeyDown}
          >
            <span
              className={styles.caseStudyProgress}
              data-state={index !== active ? "idle" : rotating ? "running" : "done"}
              data-paused={paused ? "" : undefined}
              onAnimationEnd={() => setActive((current) => (current + 1) % stories.length)}
              key={index === active && rotating ? `run-${active}` : "static"}
            />
            {item.tab}
          </button>
        ))}
      </div>
    </section>
  )
}
