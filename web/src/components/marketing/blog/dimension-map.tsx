import styles from "./blog.module.css"

type Group = "caller" | "sealed" | "live"

type Field = {
  name: string
  group: Group
}

type Row = {
  title: string
  when: string
  shown: number[] | "all" | "none" | "mixed"
  reference: string
  checks: string
  current?: boolean
}

const fields: Field[] = [
  { name: "startDatetime", group: "caller" },
  { name: "provider", group: "caller" },
  { name: "officeId", group: "sealed" },
  { name: "routing", group: "sealed" },
  { name: "columnId", group: "sealed" },
  { name: "profileId", group: "sealed" },
  { name: "duration", group: "sealed" },
  { name: "dob", group: "sealed" },
  { name: "appointmentTypeIds", group: "sealed" },
  { name: "v", group: "sealed" },
  { name: "sameStartBooked", group: "live" },
  { name: "sameStartCapacity", group: "live" },
  { name: "requiresForce", group: "live" },
  { name: "iat", group: "live" },
  { name: "exp", group: "live" },
]

const rows: Row[] = [
  { title: "The slot, s", when: "15 fields, signed", shown: "all", reference: "15 dimensions", checks: "held by code" },
  { title: "Raw EMR args", when: "until May", shown: [0, 3, 4, 5, 6, 8], reference: "6 EMR arguments", checks: "caller: day, time" },
  { title: "Counter", when: "June to September", shown: "none", reference: "S1", checks: "caller: nothing" },
  { title: "Hash", when: "September", shown: "mixed", reference: "ST_9F3A1C", checks: "caller: nothing" },
  {
    title: "Projection",
    when: "October",
    shown: [0, 1],
    reference: "SEP15-0900-REYES",
    checks: "caller: day, time, doctor",
    current: true,
  },
]

const ink = "#151715"
const quiet = "#61655f"
const edge = "#c3c6be"
const faint = "#efeee9"
const faintEdge = "#e2e1db"

const colors: Record<Group, string> = {
  caller: ink,
  sealed: "#9da19a",
  live: "#d9733f",
}

const legend: { group: Group; label: string }[] = [
  { group: "caller", label: "caller can check" },
  { group: "sealed", label: "signed into the token" },
  { group: "live", label: "re-checked live" },
]

const cellWidth = 24
const cellStep = 27
const cellHeight = 26
const stripX = 168
const stripEnd = stripX + cellStep * fields.length - (cellStep - cellWidth)
const refX = 600
const refWidth = 152
const headerBase = 132
const firstRow = 178
const rowStep = 66

function cellX(index: number) {
  return stripX + index * cellStep
}

function cellFill(row: Row, index: number) {
  if (row.shown === "all") return colors[fields[index].group]
  if (row.shown === "mixed") return "url(#slot-hash-fill)"
  if (row.shown === "none") return faint
  return row.shown.includes(index) ? colors[fields[index].group] : faint
}

export function DimensionMap() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          What the model handles
          <span>One slot, four designs</span>
        </div>
        <div className={styles.diagram}>
          <svg
            viewBox="0 0 760 508"
            role="img"
            aria-label="A booking slot has 15 fields. Raw EMR arguments made the model handle six. Counters and hashes stand for all fifteen with one unreadable symbol. The readable reference projects the slot onto the day, time, and doctor, the only fields a caller can check."
            fontFamily="var(--font-acuity-sans), ui-sans-serif, system-ui, sans-serif"
          >
            <defs>
              <pattern id="slot-hash-fill" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <rect width="6" height="6" fill={faint} />
                <line x1="0" y1="0" x2="0" y2="6" stroke={edge} strokeWidth="2" />
              </pattern>
              <marker
                id="slot-map-arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="6"
                markerHeight="6"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill={edge} />
              </marker>
            </defs>

            {legend.map((item, index) => (
              <g key={item.group}>
                <rect x="0" y={64 + index * 22} width="10" height="10" rx="2" fill={colors[item.group]} />
                <text x="18" y={73 + index * 22} fontSize="12" fill={quiet}>
                  {item.label}
                </text>
              </g>
            ))}

            {fields.map((field, index) => (
              <text
                key={field.name}
                x={cellX(index) + cellWidth / 2 - 3}
                y={headerBase}
                fontSize="10.5"
                fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
                fill={field.group === "caller" ? ink : quiet}
                fontWeight={field.group === "caller" ? 600 : 400}
                transform={`rotate(-55 ${cellX(index) + cellWidth / 2 - 3} ${headerBase})`}
              >
                {field.name}
              </text>
            ))}

            {fields.map((field, index) => (
              <rect
                key={field.name}
                x={cellX(index)}
                y={headerBase + 10}
                width={cellWidth}
                height="4"
                rx="2"
                fill={colors[field.group]}
              />
            ))}

            {rows.map((row, rowIndex) => {
              const top = firstRow + rowIndex * rowStep
              const mid = top + 8 + cellHeight / 2
              return (
                <g key={row.title}>
                  {rowIndex > 0 ? <line x1="0" y1={top - 8} x2="752" y2={top - 8} stroke={faintEdge} /> : null}
                  <text x="0" y={top + 20} fontSize="13.5" fontWeight="600" fill={ink}>
                    {row.title}
                  </text>
                  <text x="0" y={top + 37} fontSize="11.5" fill={quiet}>
                    {row.when}
                  </text>
                  {fields.map((field, index) => (
                    <rect
                      key={field.name}
                      x={cellX(index)}
                      y={top + 8}
                      width={cellWidth}
                      height={cellHeight}
                      rx="5"
                      fill={cellFill(row, index)}
                      stroke={cellFill(row, index) === faint ? faintEdge : "none"}
                    />
                  ))}
                  <path d={`M${stripEnd + 8} ${mid}H${refX - 6}`} stroke={edge} strokeWidth="1.5" markerEnd="url(#slot-map-arrow)" />
                  <rect
                    x={refX}
                    y={top + 2}
                    width={refWidth}
                    height="40"
                    rx="10"
                    fill={row.current ? ink : "#ffffff"}
                    stroke={row.current ? ink : edge}
                    strokeWidth="1.25"
                  />
                  <text
                    x={refX + 12}
                    y={top + 19}
                    fontSize="12.5"
                    fontWeight="600"
                    fontFamily="ui-monospace, SFMono-Regular, Menlo, monospace"
                    fill={row.current ? "#f4f2ed" : ink}
                  >
                    {row.reference}
                  </text>
                  <text x={refX + 12} y={top + 34} fontSize="10.5" fill={row.current ? "#d0d3cc" : quiet}>
                    {row.checks}
                  </text>
                </g>
              )
            })}
          </svg>
        </div>
      </div>
      <figcaption className={styles.figureCaption}>
        A slot is a point with 15 coordinates. Counters and hashes stand for the whole point, so a wrong one looks
        valid. The readable reference projects it onto the day, time, and doctor. Code keeps the rest, signed.
      </figcaption>
    </figure>
  )
}
