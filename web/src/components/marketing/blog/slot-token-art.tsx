import styles from "./blog.module.css"

const retired = ["columnId · profileId · startDatetime", "A · B · C", "S1 · S2", "ST_9F3A1C"]

const signal = "#d9733f"

export function SlotTokenArt() {
  return (
    <figure
      className={styles.wave}
      aria-label="Retired slot references struck through above the current one, SEP15-0900-REYES"
    >
      <svg viewBox="0 0 900 600" aria-hidden="true">
        {retired.map((label, index) => {
          const y = 150 + index * 52
          const width = label.length * 16.8
          return (
            <g key={label} opacity={0.18 + index * 0.08}>
              <text
                x="450"
                y={y}
                fill="#f4f2ed"
                fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
                fontSize="28"
                textAnchor="middle"
              >
                {label}
              </text>
              <line
                x1={450 - width / 2}
                x2={450 + width / 2}
                y1={y - 9}
                y2={y - 9}
                stroke="#f4f2ed"
                strokeWidth="2"
              />
            </g>
          )
        })}
        <text
          x="450"
          y="420"
          fill="#f4f2ed"
          fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
          fontSize="62"
          letterSpacing="2"
          textAnchor="middle"
        >
          SEP15-0900-REYES
        </text>
        <line
          x1="160"
          x2="740"
          y1="448"
          y2="448"
          stroke={signal}
          strokeWidth="3"
          strokeLinecap="round"
        />
      </svg>
    </figure>
  )
}
