import styles from "./blog.module.css"

const width = 200
const height = 60
const rowA = 70
const rowB = 200
const left = 120
const center = 380
const right = 640

const edge = "#c3c6be"
const ink = "#151715"
const quiet = "#61655f"

function Box({
  x,
  y,
  step,
  title,
  body,
  main = false,
}: {
  x: number
  y: number
  step: string
  title: string
  body: string
  main?: boolean
}) {
  return (
    <g>
      <rect
        x={x - width / 2}
        y={y - height / 2}
        width={width}
        height={height}
        rx="12"
        fill={main ? ink : "#ffffff"}
        stroke={main ? ink : edge}
        strokeWidth="1.25"
      />
      <text
        x={x - width / 2 + 14}
        y={y - height / 2 + 18}
        fontSize="10"
        letterSpacing="1.2"
        fill={main ? "#b7bbb3" : quiet}
      >
        {step}
      </text>
      <text x={x} y={y + 2} textAnchor="middle" fontSize="15" fontWeight="600" fill={main ? "#f4f2ed" : ink}>
        {title}
      </text>
      <text x={x} y={y + 20} textAnchor="middle" fontSize="12.5" fill={main ? "#d0d3cc" : quiet}>
        {body}
      </text>
    </g>
  )
}

export function LoopDiagram() {
  return (
    <figure className={styles.figure}>
      <div className={`${styles.panel} ${styles.diagram}`}>
        <svg
          viewBox="0 0 760 260"
          role="img"
          aria-label="Every stall the judge finds becomes a permanent test"
          fontFamily="var(--font-acuity-sans), ui-sans-serif, system-ui, sans-serif"
        >
          <defs>
            <marker
              id="blog-loop-arrow"
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
          <g fill="none" stroke={edge} strokeWidth="1.5">
            <path d={`M${left + width / 2} ${rowA}H${center - width / 2 - 2}`} markerEnd="url(#blog-loop-arrow)" />
            <path d={`M${center + width / 2} ${rowA}H${right - width / 2 - 2}`} markerEnd="url(#blog-loop-arrow)" />
            <path d={`M${right} ${rowA + height / 2}V${rowB - height / 2 - 2}`} markerEnd="url(#blog-loop-arrow)" />
            <path d={`M${right - width / 2} ${rowB}H${center + width / 2 + 2}`} markerEnd="url(#blog-loop-arrow)" />
            <path d={`M${center - width / 2} ${rowB}H${left + width / 2 + 2}`} markerEnd="url(#blog-loop-arrow)" />
            <path
              d={`M${left} ${rowB - height / 2}V${rowA + height / 2 + 2}`}
              markerEnd="url(#blog-loop-arrow)"
              strokeDasharray="4 4"
            />
          </g>
          <text x={left + 12} y={(rowA + rowB) / 2 + 4} fontSize="11.5" fill={quiet}>
            back to production
          </text>
          <Box x={left} y={rowA} step="01" title="Production calls" body="every call is judged" />
          <Box x={center} y={rowA} step="02" title="Judge flags a stall" body="caller says “hello? hello?”" />
          <Box x={right} y={rowA} step="03" title="Traces show why" body="answer never handed off" />
          <Box x={right} y={rowB} step="04" title="Failure becomes a test" body="6 audio scenarios, kept" main />
          <Box x={center} y={rowB} step="05" title="Six teams compete" body="fewest stalls, least change" />
          <Box x={left} y={rowB} step="06" title="Ship with a fallback" body="then watch the judge" />
        </svg>
      </div>
      <figcaption className={styles.figureCaption}>
        Every stall the judge finds becomes a permanent test.
      </figcaption>
    </figure>
  )
}
