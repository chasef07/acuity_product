import assert from "node:assert/strict"
import test from "node:test"

import { acuityMarkDots, acuityMarkMotions, dailyAcuityMarkMotion } from "./acuity-mark-motion.ts"

test("the mark plays a different motion each day and every motion within ten days", () => {
  const seen = new Set<string>()
  let previous = ""
  for (let day = 1; day <= 10; day += 1) {
    const motion = dailyAcuityMarkMotion(new Date(2026, 9, day, 23, 59)).name
    assert.notEqual(motion, previous)
    assert.equal(dailyAcuityMarkMotion(new Date(2026, 9, day, 0, 1)).name, motion)
    seen.add(motion)
    previous = motion
  }
  assert.equal(seen.size, acuityMarkMotions.length)
  assert.equal(acuityMarkMotions.length, 10)
})

test("every motion keeps seven dots and loops without a visible jump", () => {
  for (const motion of acuityMarkMotions) {
    const start = visibleDots(motion.frame(0))
    const end = visibleDots(motion.frame(0.99999))
    assert.equal(motion.frame(0.5).length, acuityMarkDots.length, motion.name)
    assert.equal(end.length, start.length, motion.name)
    start.forEach((dot, index) => {
      for (const key of ["x", "y", "r", "opacity"] as const) {
        assert.ok(Math.abs(dot[key] - end[index][key]) < 0.05, `${motion.name} ${key}`)
      }
    })
  }
})

function visibleDots(frame: ReturnType<(typeof acuityMarkMotions)[number]["frame"]>) {
  return frame
    .filter((dot) => dot.opacity > 0.01)
    .sort((a, b) => Math.round(a.x - b.x) || Math.round(a.y - b.y))
}
