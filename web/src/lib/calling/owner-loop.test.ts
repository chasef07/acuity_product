import assert from "node:assert/strict"
import test from "node:test"

import { createCallingOwnerLoop } from "./owner-loop.ts"

test("a visible idle owner stays within its 30-second request budget", async () => {
  const clock = new ManualClock()
  const leasePosts = 1
  let readinessHeartbeats = 0
  let stateReads = 0
  const loop = createCallingOwnerLoop({
    ensureMediaConnected: async () => {},
    heartbeat: async () => {
      readinessHeartbeats += 1
      return "owner"
    },
    refresh: async () => {
      stateReads += 1
    },
    onOwnershipLost: async () => {},
    isHidden: () => false,
    random: () => 0.5,
    clock,
  })

  loop.start()
  await clock.advance(30_000)

  assert.equal(leasePosts, 1)
  assert.equal(readinessHeartbeats, 4)
  assert.equal(stateReads, 6)
  loop.stop()
})

test("an owner heartbeat recovers media without reacquiring its lease", async () => {
  const clock = new ManualClock()
  const leasePosts = 1
  let mediaConnectionAttempts = 1
  let mediaConnected = false
  let readinessHeartbeats = 0
  const loop = createCallingOwnerLoop({
    ensureMediaConnected: async () => {
      mediaConnectionAttempts += 1
      mediaConnected = true
    },
    heartbeat: async () => {
      if (mediaConnected) readinessHeartbeats += 1
      return "owner"
    },
    refresh: async () => {},
    onOwnershipLost: async () => {},
    isHidden: () => false,
    random: () => 0.5,
    clock,
  })

  loop.start()
  await clock.advance(6_500)

  assert.equal(leasePosts, 1)
  assert.equal(mediaConnectionAttempts, 2)
  assert.equal(readinessHeartbeats, 1)
  loop.stop()
})

test("a lost owner fails closed and stops owner work", async () => {
  const clock = new ManualClock()
  let owner = true
  let mediaConnected = true
  let readinessHeartbeats = 0
  let stateReads = 0
  const loop = createCallingOwnerLoop({
    ensureMediaConnected: async () => {},
    heartbeat: async () => {
      readinessHeartbeats += 1
      return "lost"
    },
    refresh: async () => {
      stateReads += 1
    },
    onOwnershipLost: async () => {
      owner = false
      mediaConnected = false
    },
    isHidden: () => false,
    random: () => 0.5,
    clock,
  })

  loop.start()
  await clock.advance(30_000)

  assert.equal(owner, false)
  assert.equal(mediaConnected, false)
  assert.equal(readinessHeartbeats, 1)
  assert.equal(stateReads, 1)
})

test("overlapping incoming media signals cause one immediate authoritative refresh", async () => {
  const clock = new ManualClock()
  const releaseRefresh = deferred()
  let stateReads = 0
  const loop = createCallingOwnerLoop({
    ensureMediaConnected: async () => {},
    heartbeat: async () => "owner",
    refresh: async () => {
      stateReads += 1
      await releaseRefresh.promise
    },
    onOwnershipLost: async () => {},
    isHidden: () => false,
    random: () => 0.5,
    clock,
  })

  loop.start()
  const first = loop.incomingMedia()
  const overlapping = loop.incomingMedia()
  await Promise.resolve()
  assert.equal(stateReads, 1)

  releaseRefresh.resolve()
  await Promise.all([first, overlapping])
  await clock.advance(4_999)
  assert.equal(stateReads, 1)
  loop.stop()
})

test("fallback polling discovers state within six seconds without signals", async () => {
  const clock = new ManualClock()
  let stateReads = 0
  const loop = createCallingOwnerLoop({
    ensureMediaConnected: async () => {},
    heartbeat: async () => "owner",
    refresh: async () => {
      stateReads += 1
    },
    onOwnershipLost: async () => {},
    isHidden: () => false,
    random: () => 1,
    clock,
  })

  loop.start()
  await clock.advance(5_999)
  assert.equal(stateReads, 0)
  await clock.advance(1)
  assert.equal(stateReads, 1)
  loop.stop()
})

type Timer = {
  id: number
  deadline: number
  callback: () => void
}

class ManualClock {
  now = 0
  private nextID = 1
  private timers: Timer[] = []

  setTimeout = (callback: () => void, milliseconds: number) => {
    const id = this.nextID++
    this.timers.push({ id, deadline: this.now + milliseconds, callback })
    return id
  }

  clearTimeout = (id: number) => {
    this.timers = this.timers.filter((timer) => timer.id !== id)
  }

  async advance(milliseconds: number) {
    const target = this.now + milliseconds
    while (true) {
      this.timers.sort((left, right) => left.deadline - right.deadline)
      const timer = this.timers[0]
      if (!timer || timer.deadline > target) break
      this.timers.shift()
      this.now = timer.deadline
      timer.callback()
      await new Promise((resolve) => setTimeout(resolve, 0))
    }
    this.now = target
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

function deferred() {
  let resolve = () => {}
  const promise = new Promise<void>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}
