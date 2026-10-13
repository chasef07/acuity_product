import styles from "./blog.module.css"

const ink = "#151715"
const quiet = "#61655f"
const edge = "#c3c6be"
const faint = "#efeee9"
const signal = "#d9733f"
const mono = "ui-monospace, SFMono-Regular, Menlo, monospace"

type Block = {
  label: string
  width: number
  tone: "plain" | "search" | "old" | "call"
}

const blocks: Block[] = [
  { label: "prompt", width: 64, tone: "plain" },
  { label: "caller", width: 52, tone: "plain" },
  { label: "search result", width: 132, tone: "search" },
  { label: "agent", width: 52, tone: "plain" },
  { label: "caller", width: 52, tone: "plain" },
  { label: "search result", width: 104, tone: "old" },
  { label: "agent", width: 52, tone: "plain" },
  { label: "caller", width: 52, tone: "plain" },
  { label: "book_appt(…)", width: 126, tone: "call" },
]

const rows = [
  { column: "412", profile: "87", start: "2026-09-15T09:00", duration: "15", mark: "agreed" },
  { column: "412", profile: "87", start: "2026-09-16T09:00", duration: "15", mark: "copied" },
  { column: "415", profile: "91", start: "2026-09-15T09:00", duration: "20", mark: "" },
  { column: "412", profile: "87", start: "2026-09-15T09:30", duration: "15", mark: "" },
  { column: "415", profile: "91", start: "2026-09-16T09:00", duration: "20", mark: "" },
  { column: "412", profile: "87", start: "2026-09-16T09:30", duration: "15", mark: "" },
]

const gap = 6
const stripX = 12
const stripY = 52
const stripHeight = 34
const tableX = 12
const tableY = 150
const rowHeight = 26
const callX = 476
const callY = 168

function blockX(index: number) {
  return stripX + blocks.slice(0, index).reduce((sum, block) => sum + block.width + gap, 0)
}

function blockFill(tone: Block["tone"]) {
  if (tone === "search") return "#ffffff"
  if (tone === "call") return ink
  if (tone === "old") return "url(#context-copy-hatch)"
  return faint
}

export function ContextCopy() {
  const searchX = blockX(2)
  const searchWidth = blocks[2].width
  const tableWidth = 348
  const copiedY = tableY + 34 + rowHeight
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Version 1: what the model had to do
          <span>Illustration</span>
        </div>
        <div className={styles.diagram}>
          <svg
            viewBox="0 0 760 360"
            role="img"
            aria-label="The call's context grows every turn. To book, the model has to reach back to a search result several turns earlier and copy four values from one row among near-identical rows. Copying from the next row books a valid slot on the wrong day."
            fontFamily="var(--font-acuity-sans), ui-sans-serif, system-ui, sans-serif"
          >
            <defs>
              <pattern id="context-copy-hatch" width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
                <rect width="6" height="6" fill={faint} />
                <line x1="0" y1="0" x2="0" y2="6" stroke={edge} strokeWidth="2" />
              </pattern>
              <marker
                id="context-copy-arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="6"
                markerHeight="6"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill={signal} />
              </marker>
              <marker
                id="context-copy-grow"
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

            <text x={stripX} y="22" fontSize="12" fill={quiet}>
              The call’s context, oldest to newest
            </text>
            <path d={`M${stripX} 34H748`} stroke={edge} strokeWidth="1.25" markerEnd="url(#context-copy-grow)" />
            <text x="748" y="28" textAnchor="end" fontSize="11" fill={quiet}>
              grows every turn
            </text>

            {blocks.map((block, index) => (
              <g key={`${block.label}-${index}`}>
                <rect
                  x={blockX(index)}
                  y={stripY}
                  width={block.width}
                  height={stripHeight}
                  rx="7"
                  fill={blockFill(block.tone)}
                  stroke={block.tone === "search" ? ink : block.tone === "call" ? ink : "none"}
                  strokeWidth="1.25"
                />
                <text
                  x={blockX(index) + block.width / 2}
                  y={stripY + 21}
                  textAnchor="middle"
                  fontSize="11.5"
                  fontFamily={block.tone === "call" ? mono : undefined}
                  fontWeight={block.tone === "search" || block.tone === "call" ? 600 : 400}
                  fill={block.tone === "call" ? "#f4f2ed" : block.tone === "search" ? ink : quiet}
                >
                  {block.label}
                </text>
              </g>
            ))}

            <path
              d={`M${searchX} ${stripY + stripHeight}L${tableX} ${tableY}M${searchX + searchWidth} ${stripY + stripHeight}L${tableX + tableWidth} ${tableY}`}
              stroke={edge}
              strokeDasharray="4 4"
              fill="none"
            />

            <rect x={tableX} y={tableY} width={tableWidth} height={34 + rows.length * rowHeight + 8} rx="12" fill="#ffffff" stroke={edge} />
            {["columnId", "profileId", "startDatetime", "duration"].map((heading, index) => (
              <text
                key={heading}
                x={tableX + 16 + [0, 66, 134, 268][index]}
                y={tableY + 22}
                fontSize="10.5"
                fontFamily={mono}
                fill={quiet}
              >
                {heading}
              </text>
            ))}
            {rows.map((row, index) => {
              const y = tableY + 34 + index * rowHeight
              const highlighted = row.mark === "agreed" || row.mark === "copied"
              return (
                <g key={`${row.start}-${row.column}`}>
                  {highlighted ? (
                    <rect
                      x={tableX + 6}
                      y={y}
                      width={tableWidth - 12}
                      height={rowHeight - 4}
                      rx="6"
                      fill={row.mark === "agreed" ? "#e7efe9" : "#fbe9df"}
                    />
                  ) : null}
                  {[row.column, row.profile, row.start, row.duration].map((value, column) => (
                    <text
                      key={column}
                      x={tableX + 16 + [0, 66, 134, 268][column]}
                      y={y + 15}
                      fontSize="12"
                      fontFamily={mono}
                      fill={highlighted ? ink : quiet}
                      fontWeight={highlighted ? 600 : 400}
                    >
                      {value}
                    </text>
                  ))}
                </g>
              )
            })}
            <text x={tableX + tableWidth + 8} y={tableY + 34 + 15} fontSize="11" fill="#287257" fontWeight="600">
              caller said yes
            </text>
            <text x={tableX + tableWidth + 8} y={copiedY + 15} fontSize="11" fill={signal} fontWeight="600">
              one row down
            </text>

            <path
              d={`M${blockX(8) + blocks[8].width / 2} ${stripY + stripHeight}V${callY - 8}`}
              stroke={edge}
              strokeDasharray="4 4"
            />
            <rect x={callX} y={callY} width="276" height="124" rx="12" fill={ink} />
            <text x={callX + 16} y={callY + 24} fontSize="12" fontFamily={mono} fill="#f4f2ed" fontWeight="600">
              book_appt(
            </text>
            {[
              "columnId: 412,",
              "profileId: 87,",
              "startDatetime: \"2026-09-16T09:00\",",
              "duration: 15 )",
            ].map((line, index) => (
              <text
                key={line}
                x={callX + 28}
                y={callY + 46 + index * 18}
                fontSize="11"
                fontFamily={mono}
                fill={index === 2 ? "#f2b48f" : "#d0d3cc"}
              >
                {line}
              </text>
            ))}
            <path
              d={`M${tableX + tableWidth + 92} ${copiedY + 11}C${callX - 14} ${copiedY + 11} ${callX - 20} ${callY + 70} ${callX - 4} ${callY + 74}`}
              stroke={signal}
              strokeWidth="1.5"
              fill="none"
              markerEnd="url(#context-copy-arrow)"
            />
            <text x={callX} y={callY + 146} fontSize="11.5" fill={quiet}>
              Valid booking, wrong day. Nothing errors.
            </text>
          </svg>
        </div>
      </div>
      <figcaption className={styles.figureCaption}>
        To book, the model reaches back several turns and copies four values from one row among near-identical
        rows. Copying the row below books a real slot the caller never chose.
      </figcaption>
    </figure>
  )
}
