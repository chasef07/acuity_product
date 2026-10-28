import type { StoryArt as StoryArtKind } from "./stories"

const palettes: Record<StoryArtKind, { background: string; color: string }> = {
  wave: { background: "#30332f", color: "#f4f2ed" },
  calendar: { background: "#e4e2db", color: "#080808" },
  network: { background: "#6d716a", color: "#f4f2ed" },
  rings: { background: "#1d1e1c", color: "#f4f2ed" },
  bars: { background: "#1d1e1c", color: "#f4f2ed" },
  slots: { background: "#dcd6cb", color: "#080808" },
}

function wavePath(row: number) {
  const base = 150 + row * 19
  const amplitude = 26 + row * 3.4
  const points = Array.from({ length: 61 }, (_, i) => {
    const x = i * 10
    const y = base + amplitude * Math.sin(x * 0.011 + row * 0.32) * Math.cos(x * 0.004 - row * 0.1)
    return `${x},${y.toFixed(1)}`
  })
  return `M${points.join(" L")}`
}

const booked = new Set([2, 5, 8, 9, 13, 16, 18, 22, 23, 27, 30])
const speakingBars = Array.from({ length: 22 }, (_, i) =>
  Math.round(24 + 150 * Math.abs(Math.sin(i * 1.7) * Math.cos(i * 0.45))),
)
const callerBars = Array.from({ length: 7 }, (_, i) =>
  Math.round(20 + 90 * Math.abs(Math.sin(i * 2.3 + 1) * Math.cos(i * 0.9))),
)

function Art({ kind }: { kind: StoryArtKind }) {
  if (kind === "wave") {
    return Array.from({ length: 22 }, (_, row) => (
      <path
        d={wavePath(row)}
        fill="none"
        stroke="currentColor"
        strokeDasharray="0 10"
        strokeLinecap="round"
        strokeOpacity={0.18 + row * 0.035}
        strokeWidth="3.4"
        key={row}
      />
    ))
  }

  if (kind === "calendar") {
    return Array.from({ length: 35 }, (_, cell) => {
      const x = 96 + (cell % 7) * 62
      const y = 212 + Math.floor(cell / 7) * 62
      return booked.has(cell) ? (
        <rect x={x} y={y} width="48" height="48" rx="6" fill="currentColor" key={cell} />
      ) : (
        <rect
          x={x}
          y={y}
          width="48"
          height="48"
          rx="6"
          fill="none"
          stroke="currentColor"
          strokeOpacity="0.24"
          strokeWidth="1.5"
          key={cell}
        />
      )
    })
  }

  if (kind === "network") {
    const hub = [372, 392]
    const nodes = [
      [180, 250],
      [270, 520],
      [470, 210],
      [530, 470],
      [140, 420],
      [400, 560],
    ]
    return (
      <>
        <path
          d={Array.from({ length: 30 }, (_, row) => `M5,${10 + row * 20} H600`).join(" ")}
          stroke="currentColor"
          strokeDasharray="0 20"
          strokeLinecap="round"
          strokeOpacity="0.28"
          strokeWidth="3.2"
        />
        {nodes.map(([x, y]) => (
          <line
            x1={hub[0]}
            y1={hub[1]}
            x2={x}
            y2={y}
            stroke="currentColor"
            strokeOpacity="0.55"
            strokeWidth="1.5"
            key={`line-${x}`}
          />
        ))}
        {nodes.map(([x, y]) => (
          <circle cx={x} cy={y} r="8" fill="currentColor" key={`node-${x}`} />
        ))}
        <circle cx={hub[0]} cy={hub[1]} r="16" fill="currentColor" />
      </>
    )
  }

  if (kind === "bars") {
    return (
      <>
        {speakingBars.map((height, i) => (
          <rect
            x={40 + i * 10}
            y={330 - height / 2}
            width="5"
            height={height}
            rx="2.5"
            fill="currentColor"
            fillOpacity={0.35 + (i / speakingBars.length) * 0.6}
            key={`speak-${i}`}
          />
        ))}
        <line
          x1="270"
          x2="470"
          y1="330"
          y2="330"
          stroke="currentColor"
          strokeDasharray="0 9"
          strokeLinecap="round"
          strokeOpacity="0.55"
          strokeWidth="3"
        />
        {callerBars.map((height, i) => (
          <rect
            x={490 + i * 10}
            y={330 - height / 2}
            width="5"
            height={height}
            rx="2.5"
            fill="currentColor"
            key={`caller-${i}`}
          />
        ))}
      </>
    )
  }

  if (kind === "slots") {
    return Array.from({ length: 6 }, (_, row) =>
      Array.from({ length: 4 }, (_, col) => {
        const chosen = row === 3 && col === 1
        return (
          <rect
            x={84 + col * 112}
            y={180 + row * 52}
            width="96"
            height="34"
            rx="17"
            fill={chosen ? "currentColor" : "none"}
            stroke="currentColor"
            strokeOpacity={chosen ? 1 : 0.26}
            strokeWidth="1.5"
            key={`${row}-${col}`}
          />
        )
      }),
    )
  }

  return (
    <>
      {Array.from({ length: 11 }, (_, ring) => (
        <circle
          cx="390"
          cy="400"
          r={36 + ring * 30}
          fill="none"
          stroke="currentColor"
          strokeDasharray="0 11"
          strokeLinecap="round"
          strokeOpacity={0.85 - ring * 0.065}
          strokeWidth="3.2"
          key={ring}
        />
      ))}
      <circle cx="390" cy="400" r="16" fill="currentColor" />
    </>
  )
}

export function StoryArt({ kind, bare = false }: { kind: StoryArtKind; bare?: boolean }) {
  return (
    <svg
      viewBox="0 0 600 600"
      preserveAspectRatio="xMidYMid slice"
      aria-hidden="true"
      focusable="false"
      style={bare ? undefined : palettes[kind]}
    >
      <Art kind={kind} />
    </svg>
  )
}
