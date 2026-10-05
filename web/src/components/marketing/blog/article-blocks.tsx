import type { ReactNode } from "react"

import { cn } from "@/lib/utils"

import styles from "./blog.module.css"

type Approach = {
  name: string
  change: string
  stalls: number
  runs: number
  shipped?: boolean
  baseline?: boolean
}

const approaches: Approach[] = [
  { name: "Base", change: "Nothing", stalls: 3, runs: 33, baseline: true },
  { name: "A", change: "Added “saying you’ll check is not checking”", stalls: 2, runs: 18 },
  { name: "C1", change: "“Hand off in the same turn” plus two triggers", stalls: 0, runs: 17 },
  { name: "C2", change: "C1, with another rule trimmed", stalls: 0, runs: 18, shipped: true },
  { name: "D", change: "“Only the backend can check” plus a narrower trigger", stalls: 1, runs: 35 },
  { name: "E", change: "Original prompt plus only the “caller answered” trigger", stalls: 0, runs: 18 },
  { name: "F", change: "D, broadened to “do anything”", stalls: 2, runs: 17 },
]

const highestRate = Math.max(...approaches.map((approach) => approach.stalls / approach.runs))

export function ApproachChart() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Stalls by approach
          <span>Stall suite, 3 runs</span>
        </div>
        <ul className={styles.approachList}>
          {approaches.map((approach) => {
            const rate = approach.stalls / approach.runs
            return (
              <li
                key={approach.name}
                className={cn(styles.approachRow, approach.shipped && styles.approachShipped)}
              >
                <span className={cn(styles.approachName, approach.baseline && styles.approachBaseline)}>
                  {approach.name}
                </span>
                <span className={styles.approachChange}>
                  {approach.change}
                  {approach.shipped ? <span className={styles.shippedBadge}>Shipped</span> : null}
                </span>
                <span className={styles.approachRate}>
                  <span className={styles.bar}>
                    <span className={styles.barFill} style={{ width: `${(rate / highestRate) * 100}%` }} />
                  </span>
                  <span className={cn(styles.rate, rate === 0 && styles.rateZero)}>
                    {approach.stalls}/{approach.runs}
                  </span>
                </span>
              </li>
            )
          })}
        </ul>
      </div>
      <figcaption className={styles.figureCaption}>
        Audio simulations, 12 to 47 valid runs per approach. The differences between C, D and E are noise.
      </figcaption>
    </figure>
  )
}

export function Stats({ children }: { children: ReactNode }) {
  return <div className={styles.stats}>{children}</div>
}

export function Stat({
  label,
  before,
  after,
  note,
  tone = "simulation",
}: {
  label: string
  before: string
  after: string
  note: string
  tone?: "simulation" | "production"
}) {
  return (
    <div className={cn(styles.stat, tone === "production" && styles.statDark)}>
      <p className={styles.statLabel}>{label}</p>
      <p className={styles.statNumbers}>
        <span className={styles.statBefore}>{before}</span>
        <span className={styles.statArrow} aria-hidden="true">
          →
        </span>
        <span className={styles.statAfter}>{after}</span>
      </p>
      <p className={styles.statNote}>{note}</p>
    </div>
  )
}

export function PullQuote({ children, cite }: { children: ReactNode; cite?: string }) {
  return (
    <figure className={styles.pullQuote}>
      <blockquote>{children}</blockquote>
      {cite ? <figcaption>{cite}</figcaption> : null}
    </figure>
  )
}

export function Steps({ children }: { children: ReactNode }) {
  return <div className={styles.steps}>{children}</div>
}

export function Step({
  n,
  children,
  alert = false,
  last = false,
}: {
  n: number
  children: ReactNode
  alert?: boolean
  last?: boolean
}) {
  return (
    <div className={cn(styles.step, alert && styles.stepAlert)}>
      <div className={styles.stepRail}>
        <span className={styles.stepNumber}>{n}</span>
        {last ? null : <span className={styles.stepLine} />}
      </div>
      <div className={styles.stepBody}>{children}</div>
    </div>
  )
}
