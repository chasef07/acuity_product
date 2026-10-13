import { cn } from "@/lib/utils"

import styles from "./blog.module.css"

const keys = Array.from({ length: 40 }, (_, index) => `S${index + 1}`)

export function SimilarKeys() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Version 3: one character apart
          <span>Illustration</span>
        </div>
        <div className={styles.keysBody}>
          <div>
            <p className={styles.shiftTitle}>Keys in the model’s context</p>
            <ul className={styles.keyGrid}>
              {keys.map((key) => (
                <li
                  key={key}
                  className={cn(
                    styles.keyChip,
                    key === "S6" && styles.keyAgreed,
                    key === "S7" && styles.keySent,
                  )}
                >
                  {key}
                </li>
              ))}
            </ul>
          </div>
          <div className={styles.keyTrace}>
            <p className={styles.shiftTitle}>The trace</p>
            <p className={styles.keyLine}>
              <span>Agent says</span>
              “Tuesday, September 15 at 9:00 with Dr. Reyes?”
            </p>
            <p className={styles.keyLine}>
              <span>Caller says</span>
              “Yes.”
            </p>
            <p className={styles.keyLine}>
              <span>Tool call</span>
              <code>book_appointment(&quot;S7&quot;)</code>
            </p>
            <p className={styles.keyVerdict}>
              <code>S6</code> was Tuesday. <code>S7</code> was Wednesday. Both are valid, and nothing in the trace
              connects the words to the key.
            </p>
          </div>
        </div>
      </div>
      <figcaption className={styles.figureCaption}>
        Counters stayed stable, but every key looked like its neighbors. A one-character slip still named a real slot.
      </figcaption>
    </figure>
  )
}
