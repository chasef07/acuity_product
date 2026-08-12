import assert from "node:assert/strict"
import test from "node:test"

import { createWorkspaceRequestBudget } from "./workspace-request-budget.ts"

test("generic revisions do not restart the AI outcome cadence", async () => {
  const clock = new ManualClock()
  let aiRequests = 0
  const budget = createWorkspaceRequestBudget({ clock })

  budget.setAIRefresh("practice-1:all", async () => {
    aiRequests += 1
  })
  await clock.advance(0)
  assert.equal(aiRequests, 1)

  for (let revision = 1; revision <= 20; revision += 1) {
    budget.setAIRefresh("practice-1:all", async () => {
      aiRequests += 1
    })
  }
  await clock.advance(29_999)
  assert.equal(aiRequests, 1)
  await clock.advance(1)
  assert.equal(aiRequests, 2)
  budget.stop()
})

test("generic revision bursts spend one selected-detail request budget", async () => {
  const clock = new ManualClock()
  let detailRefreshes = 0
  const budget = createWorkspaceRequestBudget({
    clock,
    refreshDetails: () => {
      detailRefreshes += 1
    },
  })

  for (let revision = 1; revision <= 20; revision += 1) {
    budget.signalDetailRefresh()
  }
  await clock.advance(499)
  assert.equal(detailRefreshes, 0)
  await clock.advance(1)
  assert.equal(detailRefreshes, 1)
  budget.stop()
})

test("hidden AI polling defers until one visible refresh", async () => {
  const clock = new ManualClock()
  let hidden = true
  let aiRequests = 0
  const budget = createWorkspaceRequestBudget({
    clock,
    isHidden: () => hidden,
  })

  budget.setAIRefresh("practice-1:all", async () => {
    aiRequests += 1
  })
  await clock.advance(60_000)
  assert.equal(aiRequests, 0)

  hidden = false
  budget.visibilityChanged()
  await clock.advance(0)
  assert.equal(aiRequests, 1)
  await clock.advance(30_000)
  assert.equal(aiRequests, 2)
  budget.stop()
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
