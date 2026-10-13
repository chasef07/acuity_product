import { cn } from "@/lib/utils"

import styles from "./blog.module.css"

type Generation = {
  when: string
  sees: string
  why: string
  current?: boolean
}

const generations: Generation[] = [
  {
    when: "Until May",
    sees: "columnId, profileId, startDatetime, duration, appointmentTypeId, routing",
    why: "Any slip in a copied or chosen value was a wrong or rejected booking.",
  },
  {
    when: "May",
    sees: "A, B, C …",
    why: "Letters came from each slot’s position in the list. A new search reassigned them, so “A” changed meaning.",
  },
  {
    when: "June",
    sees: "S1, S2 …",
    why: "Stable across refreshes, but meaningless. A wrong number looks as valid as the right one.",
  },
  {
    when: "September",
    sees: "ST_9F3A1C",
    why: "Bound to one search, so a stale reference can’t book. Still nothing a person can read.",
  },
  {
    when: "October",
    sees: "SEP15-0900-REYES",
    why: "Repeats the read-back, so a reference for the wrong day says so.",
    current: true,
  },
]

export function RefGenerations() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Five generations of slot references
          <span>What the model sends</span>
        </div>
        <ul className={styles.approachList}>
          {generations.map((generation) => (
            <li
              key={generation.when}
              className={cn(styles.generationRow, generation.current && styles.approachShipped)}
            >
              <span className={styles.generationWhen}>{generation.when}</span>
              <span className={styles.generationSees}>
                <code>{generation.sees}</code>
                {generation.current ? <span className={styles.shippedBadge}>Current</span> : null}
              </span>
              <span className={styles.approachChange}>{generation.why}</span>
            </li>
          ))}
        </ul>
      </div>
      <figcaption className={styles.figureCaption}>
        Each change came from a way the previous reference let the model book the wrong slot.
      </figcaption>
    </figure>
  )
}
