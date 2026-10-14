import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { TaskCallContext } from "./task-call-context.tsx"
import type { Task } from "../../lib/api/generated/types.gen.ts"

const task: Task = {
  id: "task-1",
  practiceId: "practice-1",
  locationId: "location-1",
  locationName: "Downtown",
  phone: "+15551234567",
  title: "Review booked appointment",
  category: "optical",
  callerName: "Alex Example",
  state: "OPEN",
  origin: "APPOINTMENT_REVIEW",
  urgency: "normal",
  createdBy: { kind: "HUMAN", subject: "user-1" },
  createdAt: "2026-08-30T12:00:00Z",
  version: 1,
  updatedAt: "2026-08-30T12:00:00Z",
  relatedInteractionCount: 0,
  interactions: [],
}

function panel(props: Partial<Parameters<typeof TaskCallContext>[0]>) {
  return renderToStaticMarkup(
    <TaskCallContext
      task={task}
      activeCall={undefined}
      view="task"
      canMutate
      historyHint={0}
      taskCallError=""
      onTaskUpdated={() => {}}
      onReturnToCall={() => {}}
      {...props}
    />,
  )
}

function heading(markup: string) {
  return /<h2 class="([^"]*)">([^<]*)<\/h2>/.exec(markup)
}

test("the Task panel heading fits the narrow column and leaves the number to the band", () => {
  const markup = panel({})
  const [, classes, text] = heading(markup) ?? []
  assert.equal(text, "Review booked appointment")
  assert.match(classes ?? "", /\btext-base\b/)
  assert.doesNotMatch(classes ?? "", /\btext-lg\b/)
  assert.doesNotMatch(markup, /\(555\) 123-4567/)
  assert.match(markup, /Alex Example/)
})

test("the group panel keeps its office and caller names without repeating the number", () => {
  const second: Task = { ...task, id: "task-2", title: "Send prescription copy", callerName: "Blair Example" }
  const group: Task = { ...task, groupMembers: [task, second] }
  const markup = panel({ group, taskRows: [group], onSelectTask: () => {} })
  const [, classes, text] = heading(markup) ?? []
  assert.ok(text)
  assert.match(classes ?? "", /\btext-base\b/)
  assert.doesNotMatch(classes ?? "", /\btext-lg\b/)
  assert.doesNotMatch(markup, /\(555\) 123-4567/)
  assert.match(markup, /2 related Tasks · Downtown/)
  assert.match(markup, /Alex Example/)
  assert.match(markup, /Blair Example/)
  assert.match(markup, /Complete all 2 requests/)
})
