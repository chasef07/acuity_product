import styles from "./blog.module.css"

export const agentBars = Array.from({ length: 44 }, (_, i) =>
  Math.round(10 + 46 * Math.abs(Math.sin(i * 1.7) * Math.cos(i * 0.45))),
)

export const callerBars = Array.from({ length: 16 }, (_, i) =>
  Math.round(8 + 34 * Math.abs(Math.sin(i * 2.3 + 1) * Math.cos(i * 0.9))),
)

const middle = 70
const signal = "#d9733f"

export function DeadAirWaveform({ compact = false }: { compact?: boolean }) {
  return (
    <figure
      className={styles.wave}
      aria-label="The agent speaks, then the line goes silent until the caller says hello"
    >
      <svg viewBox="0 0 1000 140" aria-hidden="true">
        <defs>
          <linearGradient id="dead-air-agent" x1="0" x2="1" y1="0" y2="0">
            <stop offset="0" stopColor="#f4f2ed" stopOpacity="0.25" />
            <stop offset="1" stopColor="#f4f2ed" stopOpacity="0.85" />
          </linearGradient>
          <linearGradient id="dead-air-band" x1="0" x2="1" y1="0" y2="0">
            <stop offset="0" stopColor={signal} stopOpacity="0" />
            <stop offset="0.15" stopColor={signal} stopOpacity="0.1" />
            <stop offset="0.85" stopColor={signal} stopOpacity="0.1" />
            <stop offset="1" stopColor={signal} stopOpacity="0" />
          </linearGradient>
        </defs>
        {agentBars.map((height, index) => (
          <rect
            key={`agent-${index}`}
            x={index * 8}
            y={middle - height / 2}
            width="4"
            height={height}
            rx="2"
            fill="url(#dead-air-agent)"
          />
        ))}
        <rect x="368" y="20" width="440" height="100" fill="url(#dead-air-band)" />
        <line
          x1="364"
          x2="812"
          y1={middle}
          y2={middle}
          stroke={signal}
          strokeWidth="2"
          strokeLinecap="round"
        />
        <line
          className={styles.playhead}
          x1="370"
          x2="370"
          y1="34"
          y2="106"
          stroke={signal}
          strokeWidth="2"
        />
        {callerBars.map((height, index) => (
          <rect
            key={`caller-${index}`}
            x={832 + index * 10}
            y={middle - height / 2}
            width="5"
            height={height}
            rx="2.5"
            fill="#f4f2ed"
          />
        ))}
      </svg>
      {compact ? null : (
        <figcaption className={styles.waveLegend}>
          <span>
            Agent
            <span>&ldquo;One moment while I look you up in the system.&rdquo;</span>
          </span>
          <span>
            Dead air
            <span>nothing is running</span>
          </span>
          <span>
            Caller
            <span>&ldquo;hello? hello?&rdquo;</span>
          </span>
        </figcaption>
      )}
    </figure>
  )
}
