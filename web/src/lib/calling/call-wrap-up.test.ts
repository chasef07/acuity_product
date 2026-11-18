import assert from "node:assert/strict"
import test from "node:test"

import {
  wrapUpCounts,
  wrapUpDuration,
  wrapUpOffersFollowUp,
  wrapUpRequest,
  wrapUpShortfall,
  type WrapUpCall,
  type WrapUpReason,
  type WrapUpRequest,
} from "./call-wrap-up.ts"

const inbound: WrapUpCall = { direction: "INBOUND", entryPoint: "STANDALONE" }
const handoff: WrapUpCall = { direction: "INBOUND", entryPoint: "AI_HANDOFF" }
const direct: WrapUpCall = { direction: "OUTBOUND", entryPoint: "STANDALONE" }
const fromTask: WrapUpCall = { direction: "OUTBOUND", entryPoint: "TASK", taskId: "task-call" }

function ticks(...entries: [string, WrapUpReason][]) {
  return new Map(entries.map(([taskID, reason]) => [taskID, { reason, expectedVersion: 3 }]))
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
        closures: item.ticks.map(([taskId, reason]) => ({ taskId, reason, expectedVersion: 3 })),
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

test("a save that closed or found closed every ticked Task leaves nothing to say", () => {
  const request: WrapUpRequest = {
    outcome: "FOLLOW_UP_REQUIRED",
    closures: [
      { taskId: "task-a", reason: "DONE", expectedVersion: 1 },
      { taskId: "task-b", reason: "NOT_NEEDED", expectedVersion: 2 },
    ],
  }
  assert.equal(
    wrapUpShortfall(request, {
      closedTaskIds: ["task-a"],
      alreadyClosedTaskIds: ["task-b"],
      changedTaskIds: [],
      recordedOutcome: "FOLLOW_UP_REQUIRED",
    }),
    undefined,
  )
  assert.equal(wrapUpShortfall({ outcome: "RESOLVED", closures: [] }, {}), undefined)
})

test("a save names the ticked Tasks it left open and a follow-up it did not create", () => {
  const request: WrapUpRequest = {
    outcome: "CREATE_TASK",
    closures: [
      { taskId: "task-a", reason: "DONE", expectedVersion: 1 },
      { taskId: "task-b", reason: "DONE", expectedVersion: 1 },
      { taskId: "task-c", reason: "DONE", expectedVersion: 1 },
    ],
  }
  assert.deepEqual(
    wrapUpShortfall(request, {
      closedTaskIds: ["task-a"],
      alreadyClosedTaskIds: [],
      changedTaskIds: ["task-b"],
      recordedOutcome: "RESOLVED",
    }),
    { changedTaskIds: ["task-b"], unclosedTaskIds: ["task-c"], followUpNotCreated: true },
  )
  assert.deepEqual(
    wrapUpShortfall({ outcome: "COMPLETE_TASK", closures: [] }, { recordedOutcome: "RESOLVED" }),
    undefined,
    "only a follow-up that was asked for can be missing",
  )
  assert.deepEqual(
    wrapUpShortfall({ outcome: "FOLLOW_UP_REQUIRED", closures: [] }, { recordedOutcome: "RESOLVED" }),
    { changedTaskIds: [], unclosedTaskIds: [], followUpNotCreated: true },
  )
})

test("a Call's length reads in minutes and seconds", () => {
  assert.equal(wrapUpDuration(222), "3m 42s")
  assert.equal(wrapUpDuration(185.9), "3m 05s")
  assert.equal(wrapUpDuration(42), "42s")
  assert.equal(wrapUpDuration(3_725), "1h 02m")
  assert.equal(wrapUpDuration(-4), "0s")
})

test("the close button and the count line say how many of the patient's Tasks stay open", () => {
  const cases: Array<{
    name: string
    openTaskCount: number | undefined
    closedInSummary?: number
    listed: number
    ticked: number
    open: number
    closeLabel: string
  }> = [
    { name: "three open, two ticked", openTaskCount: 3, listed: 3, ticked: 2, open: 3, closeLabel: "Close 2 · keep 1 open" },
    { name: "a capped list counts the Tasks it does not show", openTaskCount: 55, listed: 50, ticked: 2, open: 55, closeLabel: "Close 2 · keep 53 open" },
    { name: "every open Task ticked", openTaskCount: 2, listed: 2, ticked: 2, open: 2, closeLabel: "Close 2" },
    { name: "one ticked, one kept", openTaskCount: 2, listed: 2, ticked: 1, open: 2, closeLabel: "Close 1 · keep 1 open" },
    { name: "only the call's own Task, ticked", openTaskCount: 1, listed: 1, ticked: 1, open: 1, closeLabel: "Close 1" },
    { name: "the call's own Task listed while the summary failed to load", openTaskCount: undefined, listed: 1, ticked: 1, open: 1, closeLabel: "Close 1" },
    { name: "the call's own Task listed before the summary counted it", openTaskCount: 0, listed: 1, ticked: 1, open: 1, closeLabel: "Close 1" },
    { name: "the call's own Task already closed but still in the summary", openTaskCount: 3, closedInSummary: 1, listed: 2, ticked: 1, open: 2, closeLabel: "Close 1 · keep 1 open" },
    { name: "review Tasks the dialog does not list stay open", openTaskCount: 5, listed: 3, ticked: 3, open: 5, closeLabel: "Close 3 · keep 2 open" },
    { name: "the call's own Task beside an unlisted review Task", openTaskCount: 2, listed: 1, ticked: 1, open: 2, closeLabel: "Close 1 · keep 1 open" },
  ]
  for (const item of cases) {
    assert.deepEqual(
      wrapUpCounts({
        openTaskCount: item.openTaskCount,
        closedInSummary: item.closedInSummary ?? 0,
        listed: item.listed,
        ticked: item.ticked,
      }),
      { open: item.open, closeLabel: item.closeLabel },
      item.name,
    )
  }
})
