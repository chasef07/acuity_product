import { cn } from "@/lib/utils"

import styles from "./blog.module.css"

const ink = "#151715"
const quiet = "#61655f"
const edge = "#c3c6be"
const faint = "#efeee9"
const mono = "ui-monospace, SFMono-Regular, Menlo, monospace"

const actors = [
  { x: 92, name: "Model", note: "speaks, picks a letter" },
  { x: 288, name: "Agent code", note: "keeps the token table" },
  { x: 484, name: "Go middleware", note: "translates, signs, checks" },
  { x: 676, name: "EMR API", note: "IDs and schedules" },
]

type Message = {
  from: number
  to: number
  y: number
  lines: string[]
  reply?: boolean
  strong?: boolean
}

const messages: Message[] = [
  { from: 0, to: 1, y: 128, lines: ["get_availability(", "date: \"2026-09-15\")"] },
  { from: 1, to: 2, y: 164, lines: ["POST /api/scheduler/availability"] },
  { from: 2, to: 3, y: 208, lines: ["GET appointments", "GET block holds"] },
  { from: 3, to: 2, y: 240, lines: ["schedule rows"], reply: true },
  { from: 2, to: 1, y: 332, lines: ["3 slots + 3 signed tokens"], reply: true },
  { from: 1, to: 0, y: 440, lines: ["A 9:00 · B 9:30 · C 10:00"], reply: true, strong: true },
  { from: 0, to: 1, y: 516, lines: ["book_appt(slotId: \"B\")"], strong: true },
  { from: 1, to: 2, y: 622, lines: ["POST /api/appointment/book", "{ bookingToken: B’s token }"] },
  { from: 2, to: 3, y: 730, lines: ["POST /scheduler/Appointments", "columnid 412, profileid 87, …"] },
  { from: 3, to: 2, y: 762, lines: ["appointment id"], reply: true },
  { from: 2, to: 1, y: 796, lines: ["booking receipt"], reply: true },
  { from: 1, to: 0, y: 830, lines: ["booked: 9:30 AM"], reply: true },
]

const notes = [
  { actor: 2, y: 256, lines: ["find open slots", "sign one token per slot"] },
  { actor: 1, y: 346, lines: ["A → token…", "B → token…", "C → token…"], code: true },
  { actor: 1, y: 530, lines: ["look up B", "→ B’s token"] },
  { actor: 2, y: 638, lines: ["check signature, expiry", "translate to EMR fields"] },
]

const phases = [
  { y: 82, label: "1 · Search" },
  { y: 474, label: "2 · Book" },
]

export function TokenFlow() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          Version 2: one search, one booking
          <span>Request and response</span>
        </div>
        <div className={styles.diagram}>
          <svg
            viewBox="0 0 760 900"
            role="img"
            aria-label="Search: the model calls get_availability, the agent's code calls the Go middleware, and the middleware reads appointments and block holds from the EMR API. The middleware finds open slots and signs a token for each. The agent's code stores the tokens by letter and gives the model only A 9:00, B 9:30, C 10:00. Book: the model calls book_appt with slot B. The agent's code looks up B's token and sends it to the middleware, which checks the signature and expiry, translates it into EMR fields, and posts the appointment. The appointment id comes back as a booking receipt, and the model is told it is booked at 9:30."
            fontFamily="var(--font-acuity-sans), ui-sans-serif, system-ui, sans-serif"
          >
            <defs>
              <marker
                id="token-flow-arrow"
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
                id="token-flow-reply"
                viewBox="0 0 10 10"
                refX="9"
                refY="5"
                markerWidth="6"
                markerHeight="6"
                orient="auto-start-reverse"
              >
                <path d="M0 0L10 5L0 10z" fill={quiet} />
              </marker>
            </defs>

            <rect x="14" y="66" width="156" height="820" rx="12" fill={faint} />

            {actors.map((actor) => (
              <g key={actor.name}>
                <rect x={actor.x - 76} y="10" width="152" height="46" rx="10" fill="#ffffff" stroke={edge} />
                <text x={actor.x} y="30" textAnchor="middle" fontSize="13.5" fontWeight="600" fill={ink}>
                  {actor.name}
                </text>
                <text x={actor.x} y="47" textAnchor="middle" fontSize="11" fill={quiet}>
                  {actor.note}
                </text>
                <line x1={actor.x} y1="56" x2={actor.x} y2="856" stroke={edge} strokeDasharray="4 4" />
              </g>
            ))}

            {phases.map((phase) => (
              <g key={phase.label}>
                <line x1="14" y1={phase.y} x2="746" y2={phase.y} stroke={edge} />
                <rect x="584" y={phase.y - 11} width="96" height="22" rx="11" fill={ink} />
                <text x="632" y={phase.y + 4} textAnchor="middle" fontSize="11.5" fontWeight="600" fill="#f4f2ed">
                  {phase.label}
                </text>
              </g>
            ))}

            {notes.map((note) => {
              const x = actors[note.actor].x
              return (
                <g key={`${note.actor}-${note.y}`}>
                  <rect
                    x={x - 84}
                    y={note.y}
                    width="168"
                    height={note.lines.length * 17 + 12}
                    rx="8"
                    fill="#ffffff"
                    stroke={ink}
                    strokeWidth="1.1"
                  />
                  {note.lines.map((line, index) => (
                    <text
                      key={line}
                      x={x}
                      y={note.y + 20 + index * 17}
                      textAnchor="middle"
                      fontSize="11.5"
                      fontFamily={note.code ? mono : undefined}
                      fill={ink}
                    >
                      {line}
                    </text>
                  ))}
                </g>
              )
            })}

            {messages.map((message) => {
              const from = actors[message.from].x
              const to = actors[message.to].x
              const direction = to > from ? 1 : -1
              return (
                <g key={`${message.y}-${message.lines[0]}`}>
                  <path
                    d={`M${from + direction * 4} ${message.y}H${to - direction * 6}`}
                    stroke={message.reply ? quiet : ink}
                    strokeWidth="1.4"
                    strokeDasharray={message.reply ? "5 4" : undefined}
                    markerEnd={message.reply ? "url(#token-flow-reply)" : "url(#token-flow-arrow)"}
                  />
                  {message.lines.map((line, index) => (
                    <text
                      key={line}
                      x={(from + to) / 2}
                      y={message.y - 8 - (message.lines.length - 1 - index) * 15}
                      textAnchor="middle"
                      fontSize="11"
                      fontFamily={mono}
                      fontWeight={message.strong ? 700 : 400}
                      fill={message.reply ? quiet : ink}
                    >
                      {line}
                    </text>
                  ))}
                </g>
              )
            })}

            <text x="92" y="874" textAnchor="middle" fontSize="11" fill={quiet}>
              never sees a token or EMR ID
            </text>
          </svg>
        </div>
      </div>
      <figcaption className={styles.figureCaption}>
        Solid arrows are requests and dashed arrows are responses. The model sends a letter. The Go middleware turns
        the letter’s token into EMR arguments.
      </figcaption>
    </figure>
  )
}

type Option = {
  letter: string
  time: string
}

const morning: Option[] = [
  { letter: "A", time: "9:00 AM" },
  { letter: "B", time: "9:30 AM" },
  { letter: "C", time: "10:00 AM" },
]

const afternoon: Option[] = [
  { letter: "A", time: "2:00 PM" },
  { letter: "B", time: "2:30 PM" },
  { letter: "C", time: "3:00 PM" },
]

function Search({
  title,
  turn,
  options,
  hit,
}: {
  title: string
  turn: string
  options: Option[]
  hit: "agreed" | "booked"
}) {
  return (
    <div className={styles.shiftColumn}>
      <p className={styles.shiftTitle}>{title}</p>
      <p className={styles.shiftTurn}>{turn}</p>
      <ul className={styles.shiftList}>
        {options.map((option) => (
          <li
            key={option.letter}
            className={cn(
              styles.shiftRow,
              option.letter === "B" && (hit === "agreed" ? styles.shiftAgreed : styles.shiftBooked),
            )}
          >
            <code>{option.letter}</code>
            <span>
              {option.time} · Dr. Reyes
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function LetterShift() {
  return (
    <figure className={styles.figure}>
      <div className={styles.panel}>
        <div className={styles.panelHeader}>
          How letters broke
          <span>Illustration</span>
        </div>
        <div className={styles.shiftGrid}>
          <Search title="Search 1" turn="Caller: “9:30 works.”" options={morning} hit="agreed" />
          <Search title="Search 2" turn="Caller: “Anything in the afternoon?”" options={afternoon} hit="booked" />
        </div>
        <p className={styles.shiftNote}>
          Caller: “No, keep the 9:30.” The model sends <code>book B</code>, and the agent books 2:30 PM.
        </p>
      </div>
      <figcaption className={styles.figureCaption}>
        Every search lettered its results from the top again, so the same letter could point at a different slot.
      </figcaption>
    </figure>
  )
}
