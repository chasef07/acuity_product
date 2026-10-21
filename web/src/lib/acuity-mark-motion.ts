type AcuityMarkDot = { x: number; y: number; r: number; opacity: number }

type Motion = { name: string; seconds: number; frame: (progress: number) => AcuityMarkDot[] }

export const acuityMarkDots: ReadonlyArray<readonly [number, number]> = [
  [16, 7], [24, 11.5], [24, 20.5], [16, 25], [8, 20.5], [8, 11.5], [16, 16],
]

const radius = 2.75
const center = 6
const ring = acuityMarkDots.slice(0, center)

const clamp = (value: number) => Math.max(0, Math.min(1, value))
const span = (progress: number, from: number, to: number) => clamp((progress - from) / (to - from))
const lerp = (from: number, to: number, amount: number) => from + (to - from) * amount
const wave = (progress: number) => 0.5 - 0.5 * Math.cos(2 * Math.PI * progress)
const easeOut = (t: number) => 1 - (1 - t) ** 3
const easeIn = (t: number) => t ** 3
const easeInOut = (t: number) => (t < 0.5 ? 4 * t ** 3 : 1 - (-2 * t + 2) ** 3 / 2)
const overshoot = (t: number) => 1 + 2.70158 * (t - 1) ** 3 + 1.70158 * (t - 1) ** 2
const springy = (t: number) =>
  t <= 0 ? 0 : t >= 1 ? 1 : 2 ** (-10 * t) * Math.sin(((t * 10 - 0.75) * 2 * Math.PI) / 3) + 1
const bounce = (t: number) => {
  if (t < 1 / 2.75) return 7.5625 * t * t
  if (t < 2 / 2.75) return 7.5625 * (t -= 1.5 / 2.75) * t + 0.75
  if (t < 2.5 / 2.75) return 7.5625 * (t -= 2.25 / 2.75) * t + 0.9375
  return 7.5625 * (t -= 2.625 / 2.75) * t + 0.984375
}

const dot = (x: number, y: number, r = radius, opacity = 1): AcuityMarkDot => ({ x, y, r, opacity })
const outward = ([x, y]: readonly [number, number], index: number) => {
  if (index === center) return [0.37, -0.93] as const
  const length = Math.hypot(x - 16, y - 16)
  return [(x - 16) / length, (y - 16) / length] as const
}
const polar = ([x, y]: readonly [number, number]) => ({ angle: Math.atan2(y - 16, x - 16), distance: Math.hypot(x - 16, y - 16) })

export const acuityMarkMotions: Motion[] = [
  {
    name: "breathe",
    seconds: 2.8,
    frame: (p) => acuityMarkDots.map(([x, y], i) => {
      const depth = wave(p - (i === center ? 0 : 0.125))
      return dot(x, y, radius * (1 - 0.26 * depth), 1 - 0.45 * depth)
    }),
  },
  {
    name: "float",
    seconds: 3.2,
    frame: (p) => acuityMarkDots.map(([x, y]) => dot(x, y - 1.6 * wave(p))),
  },
  {
    name: "think",
    seconds: 1.2,
    frame: (p) => acuityMarkDots.map(([x, y], i) => {
      if (i === center) return dot(x, y, radius * (1 + 0.15 * wave(p)))
      const phase = (((p - i / ring.length) % 1) + 1) % 1
      const lit = phase < 0.35 ? Math.sin((Math.PI * phase) / 0.35) : 0
      return dot(x, y, radius * (0.8 + 0.3 * lit), 0.3 + 0.7 * lit)
    }),
  },
  {
    name: "talk",
    seconds: 3,
    frame: (p) => {
      const turn = 2 * Math.PI * p
      const volume = Math.max(0, 0.5 * Math.sin(7 * turn) + 0.35 * Math.sin(11 * turn) + 0.4 * Math.sin(2 * turn))
      return acuityMarkDots.map(([x, y], i) => dot(
        16 + (x - 16) * (1 + 0.18 * volume),
        16 + (y - 16) * (1 + 0.18 * volume),
        radius * (i === center ? 1 + 0.4 * volume : 1 + 0.3 * volume),
      ))
    },
  },
  {
    name: "scatter",
    seconds: 3.2,
    frame: (p) => acuityMarkDots.map((home, i) => {
      const [dx, dy] = outward(home, i)
      const distance = 8 + ((i * 5) % 4) * 2.5
      const q = p - i * 0.015
      const out = q < 0.42 ? easeOut(span(q, 0, 0.2)) : 1 - springy(span(q, 0.42, 0.95))
      return dot(home[0] + dx * distance * out, home[1] + dy * distance * out)
    }),
  },
  {
    name: "gather",
    seconds: 3,
    frame: (p) => {
      const spread = p < 0.4 ? 1 - easeInOut(span(p, 0.02, 0.3)) : overshoot(span(p, 0.4, 0.68))
      const merged = p > 0.25 && p < 0.45 ? Math.sin(Math.PI * span(p, 0.25, 0.45)) : 0
      return acuityMarkDots.map(([x, y]) => dot(16 + (x - 16) * spread, 16 + (y - 16) * spread, radius * (1 + 0.6 * merged)))
    },
  },
  {
    name: "stack",
    seconds: 5,
    frame: (p) => {
      const pile = [[13.5, 23], [18.5, 23], [26, 28.5], [21, 28.5], [16, 28.5], [11, 28.5], [6, 28.5]]
      return acuityMarkDots.map(([x, y], i) => {
        const tower = [16, 1 + i * 5]
        if (p < 0.4) {
          const t = easeInOut(span(p, i * 0.025, 0.22 + i * 0.025))
          const sway = Math.sin(span(p, 0.24, 0.4) * Math.PI * 3) * span(p, 0.24, 0.4) * (29 - tower[1]) * 0.12
          return dot(lerp(x, tower[0], t) + sway, lerp(y, tower[1], t))
        }
        if (p < 0.72) {
          const t = span(p, 0.4 + (6 - i) * 0.02, 0.62 + (6 - i) * 0.01)
          return dot(lerp(tower[0], pile[i][0], easeOut(t)), lerp(tower[1], pile[i][1], bounce(t)))
        }
        const t = overshoot(span(p, 0.72 + i * 0.02, 0.92 + i * 0.01))
        return dot(lerp(pile[i][0], x, t), lerp(pile[i][1], y, t))
      })
    },
  },
  {
    name: "spin",
    seconds: 4.8,
    frame: (p) => {
      const step = Math.min(ring.length - 1, Math.floor(p * ring.length))
      const within = p * ring.length - step
      const turn = overshoot(span(within, 0, 0.4))
      return acuityMarkDots.map(([x, y], i) => {
        if (i === center) return dot(x, y, radius * (1 + 0.3 * Math.sin(Math.PI * span(within, 0, 0.3))))
        const from = polar(ring[(i + step) % ring.length])
        const to = polar(ring[(i + step + 1) % ring.length])
        const toAngle = to.angle < from.angle ? to.angle + 2 * Math.PI : to.angle
        const angle = lerp(from.angle, toAngle, turn)
        const distance = lerp(from.distance, to.distance, turn)
        return dot(16 + distance * Math.cos(angle), 16 + distance * Math.sin(angle))
      })
    },
  },
  {
    name: "hop",
    seconds: 2.2,
    frame: (p) => acuityMarkDots.map(([x, y]) => {
      const squash = Math.sin(Math.PI * span(p, 0, 0.16)) + 0.8 * Math.sin(Math.PI * span(p, 0.62, 0.82))
      const lift = 11 * Math.sin(Math.PI * span(p - ((25 - y) / 18) * 0.06, 0.16, 0.62))
      return dot(16 + (x - 16) * (1 + 0.16 * squash), 28 - (28 - y) * (1 - 0.22 * squash) - lift)
    }),
  },
  {
    name: "rain",
    seconds: 4.4,
    frame: (p) => {
      const order = [1, 3, 5, 2, 4, 6, 0]
      return acuityMarkDots.map(([x, y], i) => {
        const turn = order[i]
        if (p < 0.6) {
          const t = span(p, turn * 0.05, turn * 0.05 + 0.28)
          return dot(x, lerp(-14, y, bounce(t)), radius, t > 0 ? 1 : 0)
        }
        const t = easeIn(span(p, 0.62 + turn * 0.03, 0.78 + turn * 0.03))
        return dot(x, y + 24 * t, radius, 1 - t)
      })
    },
  },
]

export function dailyAcuityMarkMotion(today: Date): Motion {
  const day = Math.floor(Date.UTC(today.getFullYear(), today.getMonth(), today.getDate()) / 86_400_000)
  return acuityMarkMotions[day % acuityMarkMotions.length]
}
