"use client"

import { useState, type ReactNode } from "react"

import styles from "./platform.module.css"

export function FeatureAccordion({
  items,
}: {
  items: Array<{ title: string; body: string; visual: ReactNode }>
}) {
  const [open, setOpen] = useState(0)

  return (
    <div className={styles.accordion}>
      <ul className={styles.accordionList}>
        {items.map((item, index) => (
          <li key={item.title}>
            <h3>
              <button
                type="button"
                className={styles.accordionTrigger}
                aria-expanded={index === open}
                aria-controls={`feature-${index}`}
                onClick={() => setOpen(index)}
              >
                {item.title}
              </button>
            </h3>
            <p className={styles.accordionBody} id={`feature-${index}`} hidden={index !== open}>
              {item.body}
            </p>
          </li>
        ))}
      </ul>
      <div key={open}>{items[open].visual}</div>
    </div>
  )
}

export function PhaseTabs({
  phases,
}: {
  phases: Array<{ label: string; title: string; body: string; tags: string[] }>
}) {
  const [active, setActive] = useState(0)
  const phase = phases[active]

  const select = (index: number) => {
    const next = (index + phases.length) % phases.length
    setActive(next)
    document.getElementById(`phase-tab-${next}`)?.focus()
  }

  return (
    <div>
      <div className={styles.phaseTabs} role="tablist" aria-label="Phases">
        {phases.map((item, index) => (
          <button
            type="button"
            role="tab"
            id={`phase-tab-${index}`}
            aria-selected={index === active}
            aria-controls="phase-panel"
            tabIndex={index === active ? 0 : -1}
            className={styles.phaseTab}
            key={item.label}
            onClick={() => setActive(index)}
            onKeyDown={(event) => {
              if (event.key === "ArrowRight") select(active + 1)
              if (event.key === "ArrowLeft") select(active - 1)
              if (event.key === "Home") select(0)
              if (event.key === "End") select(phases.length - 1)
            }}
          >
            {item.label}
          </button>
        ))}
      </div>
      <div
        className={styles.phasePanel}
        role="tabpanel"
        id="phase-panel"
        aria-labelledby={`phase-tab-${active}`}
        key={phase.label}
      >
        <h3>{phase.title}</h3>
        <div className={styles.phaseProgress}>
          <span>
            <span style={{ width: `${((active + 1) / phases.length) * 100}%` }} />
          </span>
          <strong>
            {active + 1}/{phases.length}
          </strong>
        </div>
        <p>{phase.body}</p>
        <ul className={styles.phaseTags}>
          {phase.tags.map((tag) => (
            <li key={tag}>{tag}</li>
          ))}
        </ul>
      </div>
    </div>
  )
}
