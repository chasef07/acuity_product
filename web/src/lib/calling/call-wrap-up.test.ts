import assert from "node:assert/strict"
import test from "node:test"

import {
  wrapUpDuration,
  wrapUpOffersFollowUp,
  wrapUpRequest,
  type WrapUpCall,
  type WrapUpReason,
} from "./call-wrap-up.ts"

const inbound: WrapUpCall = { direction: "INBOUND", entryPoint: "STANDALONE" }
const handoff: WrapUpCall = { direction: "INBOUND", entryPoint: "AI_HANDOFF" }
const direct: WrapUpCall = { direction: "OUTBOUND", entryPoint: "STANDALONE" }
const fromTask: WrapUpCall = { direction: "OUTBOUND", entryPoint: "TASK", taskId: "task-call" }

function ticks(...entries: [string, WrapUpReason][]) {
  return new Map(entries)
}

test("every wrap-up combination sends an outcome the server accepts for that Call", () => {
  const other = ["task-other", "DONE"] as const
  const own = ["task-call", "NOT_NEEDED"] as const
  const cases: Array<{
    name: string
    call: WrapUpCall
    ticks: [string, WrapUpReason][]
    followUp: boolean
    outcome: string
  }> = [
    { name: "inbound, nothing chosen", call: inbound, ticks: [], followUp: false, outcome: "RESOLVED" },
    { name: "inbound, Task ticked", call: inbound, ticks: [[...other]], followUp: false, outcome: "RESOLVED" },
    { name: "inbound, follow-up", call: inbound, ticks: [], followUp: true, outcome: "FOLLOW_UP_REQUIRED" },
    { name: "inbound, Task ticked and follow-up", call: inbound, ticks: [[...other]], followUp: true, outcome: "FOLLOW_UP_REQUIRED" },
    { name: "AI handoff, nothing chosen", call: handoff, ticks: [], followUp: false, outcome: "RESOLVED" },
    { name: "AI handoff, follow-up", call: handoff, ticks: [], followUp: true, outcome: "FOLLOW_UP_REQUIRED" },
    { name: "dialed number, nothing chosen", call: direct, ticks: [], followUp: false, outcome: "RESOLVED" },
    { name: "dialed number, Task ticked", call: direct, ticks: [[...other]], followUp: false, outcome: "RESOLVED" },
    { name: "dialed number, follow-up", call: direct, ticks: [], followUp: true, outcome: "CREATE_TASK" },
    { name: "dialed number, Task ticked and follow-up", call: direct, ticks: [[...other]], followUp: true, outcome: "CREATE_TASK" },
    { name: "from a Task, nothing chosen", call: fromTask, ticks: [], followUp: false, outcome: "KEEP_OPEN" },
    { name: "from a Task, another Task ticked", call: fromTask, ticks: [[...other]], followUp: false, outcome: "KEEP_OPEN" },
    { name: "from a Task, its Task ticked", call: fromTask, ticks: [[...own]], followUp: false, outcome: "COMPLETE_TASK" },
    { name: "from a Task, both ticked", call: fromTask, ticks: [[...own], [...other]], followUp: false, outcome: "COMPLETE_TASK" },
    { name: "from a Task, follow-up is not offered and is ignored", call: fromTask, ticks: [], followUp: true, outcome: "KEEP_OPEN" },
    { name: "from a Task, its Task ticked, follow-up ignored", call: fromTask, ticks: [[...own]], followUp: true, outcome: "COMPLETE_TASK" },
  ]
  for (const item of cases) {
    assert.deepEqual(
      wrapUpRequest(item.call, { ticks: ticks(...item.ticks), followUp: item.followUp }),
      {
        outcome: item.outcome,
        closures: item.ticks.map(([taskId, reason]) => ({ taskId, reason })),
      },
      item.name,
    )
  }
})

test("an untouched wrap-up closes nothing", () => {
  for (const call of [inbound, handoff, direct, fromTask]) {
    assert.deepEqual(wrapUpRequest(call, { ticks: new Map(), followUp: false }).closures, [])
  }
})

test("a follow-up is offered unless the Call was placed from a Task, which stays open instead", () => {
  assert.equal(wrapUpOffersFollowUp(inbound), true)
  assert.equal(wrapUpOffersFollowUp(handoff), true)
  assert.equal(wrapUpOffersFollowUp(direct), true)
  assert.equal(wrapUpOffersFollowUp(fromTask), false)
})

test("a Call's length reads in minutes and seconds", () => {
  assert.equal(wrapUpDuration(222), "3m 42s")
  assert.equal(wrapUpDuration(185.9), "3m 05s")
  assert.equal(wrapUpDuration(42), "42s")
  assert.equal(wrapUpDuration(3_725), "1h 02m")
  assert.equal(wrapUpDuration(-4), "0s")
})
