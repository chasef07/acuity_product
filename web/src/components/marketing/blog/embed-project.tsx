import styles from "./blog.module.css"

const ink = "#151715"
const quiet = "#61655f"
const edge = "#c3c6be"
const faint = "#efeee9"
const green = "#287257"
const mono = "ui-monospace, SFMono-Regular, Menlo, monospace"
const serif = "ui-serif, Georgia, Cambria, 'Times New Roman', serif"

const front = { x: 70, y: 96, w: 190, h: 140 }
const depth = { x: 56, y: -42 }

const cloud = [
  [118, 150],
  [142, 196],
  [228, 128],
  [246, 186],
  [196, 214],
  [104, 206],
  [262, 112],
  [154, 120],
  [214, 98],
  [282, 150],
]

const slot = { x: 176, y: 154 }
const twin = { x: 230, y: 180 }
const image = { x: 190, y: 322 }

export function EmbedProject() {
  const back = { x: front.x + depth.x, y: front.y + depth.y }
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Embed up, project down
          <span>The math</span>
        </div>
        <div className={styles.diagram}>
          <svg
            viewBox="0 0 760 400"
            role="img"
            aria-label="A slot is a point in a 15-dimensional space. Code embeds it into a signed token with an injective map E. The model only sees its projection onto the plane of day, time, and doctor. The caller's read-back is a point in that same plane, so a correct booking satisfies pi of s equals rho. Two schedule columns that project to the same point are collapsed into one choice."
            fontFamily="var(--font-acuity-sans), ui-sans-serif, system-ui, sans-serif"
          >
            <defs>
              <marker
                id="embed-project-arrow"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="6"
                markerHeight="6"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill={ink} />
              </marker>
              <marker
                id="embed-project-check"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="6"
                markerHeight="6"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill={green} />
              </marker>
            </defs>

            <text x={front.x} y="26" fontSize="12.5" fontWeight="600" fill={ink}>
              Slot space
            </text>
            <text x={front.x} y="44" fontSize="13" fontFamily={serif} fontStyle="italic" fill={quiet}>
              S = D₁ × D₂ × ⋯ × D₁₅
            </text>

            <g fill="none" stroke={edge} strokeWidth="1.25">
              <rect x={back.x} y={back.y} width={front.w} height={front.h} />
              <rect x={front.x} y={front.y} width={front.w} height={front.h} fill="#ffffff" fillOpacity="0.55" />
              <path
                d={`M${front.x} ${front.y}L${back.x} ${back.y}M${front.x + front.w} ${front.y}L${back.x + front.w} ${back.y}M${front.x} ${front.y + front.h}L${back.x} ${back.y + front.h}M${front.x + front.w} ${front.y + front.h}L${back.x + front.w} ${back.y + front.h}`}
              />
            </g>

            {cloud.map(([x, y]) => (
              <circle key={`${x}-${y}`} cx={x} cy={y} r="3.2" fill={edge} />
            ))}

            <path
              d="M40 352L250 352L330 294L120 294Z"
              fill={faint}
              stroke={edge}
              strokeWidth="1.25"
            />
            <text x="44" y="374" fontSize="12.5" fontWeight="600" fill={ink}>
              What a caller can check
            </text>
            <text x="44" y="392" fontSize="13" fontFamily={serif} fontStyle="italic" fill={quiet}>
              V = day × time × doctor
            </text>

            <path d={`M${slot.x} ${slot.y}L${image.x} ${image.y}`} stroke={ink} strokeDasharray="4 4" />
            <path d={`M${twin.x} ${twin.y}L${image.x} ${image.y}`} stroke={quiet} strokeDasharray="4 4" />
            <text x={slot.x - 26} y="262" fontSize="15" fontFamily={serif} fontStyle="italic" fill={ink}>
              π
            </text>

            <circle cx={twin.x} cy={twin.y} r="5" fill="#ffffff" stroke={quiet} strokeWidth="1.5" />
            <circle cx={slot.x} cy={slot.y} r="6" fill={ink} />
            <text x={slot.x - 16} y={slot.y + 4} textAnchor="end" fontSize="15" fontFamily={serif} fontStyle="italic" fill={ink}>
              s
            </text>
            <circle cx={image.x} cy={image.y} r="6" fill={ink} />

            <text x="344" y="358" fontSize="12" fontFamily={mono} fontWeight="600" fill={ink}>
              π(s) = SEP15-0900-REYES
            </text>
            <text x="344" y="376" fontSize="11.5" fill={quiet}>
              the only part the model handles
            </text>

            <path
              d={`M${slot.x + 8} ${slot.y - 6}C 290 60 400 70 470 92`}
              stroke={ink}
              strokeWidth="1.4"
              fill="none"
              markerEnd="url(#embed-project-arrow)"
            />
            <text x="330" y="58" fontSize="15" fontFamily={serif} fontStyle="italic" fill={ink}>
              E
            </text>
            <text x="346" y="58" fontSize="11.5" fill={quiet}>
              injective, signed
            </text>

            <rect x="474" y="54" width="272" height="104" rx="12" fill={ink} />
            <text x="490" y="78" fontSize="12" fontWeight="600" fill="#f4f2ed">
              Signed token, held by code
            </text>
            <text x="490" y="102" fontSize="11.5" fontFamily={mono} fill="#d0d3cc">
              eyJ2IjoxLCJvZmZpY2VJZCI6…
            </text>
            <text x="490" y="120" fontSize="11.5" fontFamily={mono} fill="#d0d3cc">
              .kM3fQx9…
            </text>
            <text x="490" y="144" fontSize="13" fontFamily={serif} fontStyle="italic" fill="#f2b48f">
              E(s) = enc(s) ‖ HMAC(enc(s))
            </text>

            <rect x="474" y="214" width="272" height="66" rx="12" fill="#ffffff" stroke={edge} />
            <text x="490" y="238" fontSize="12" fontWeight="600" fill={ink}>
              Caller’s read-back
            </text>
            <text x="490" y="260" fontSize="13" fontFamily={serif} fontStyle="italic" fill={quiet}>
              ρ = (Tue Sep 15, 9:00, Dr. Reyes)
            </text>
            <path
              d={`M560 280C560 304 330 300 ${image.x + 10} ${image.y - 4}`}
              stroke={green}
              strokeWidth="1.4"
              fill="none"
              markerEnd="url(#embed-project-check)"
            />
            <text x="586" y="304" fontSize="13" fontFamily={serif} fontStyle="italic" fontWeight="600" fill={green}>
              check: π(s) = ρ
            </text>

            <path d={`M${twin.x + 8} ${twin.y}L 334 ${twin.y + 26}`} stroke={edge} />
            {["a second column,", "same day, time,", "and doctor:", "one fiber, one choice"].map((line, index) => (
              <text key={line} x="338" y={twin.y + 22 + index * 15} fontSize="11.5" fill={quiet}>
                {line}
              </text>
            ))}
          </svg>
        </div>
      </div>
      <figcaption className={styles.figureCaption}>
        Code embeds the whole slot into a signed token. The model only handles the slot’s projection onto what a
        caller can check, which is also where the read-back lives.
      </figcaption>
    </figure>
  )
}
