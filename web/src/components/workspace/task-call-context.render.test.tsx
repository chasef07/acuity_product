import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { TaskCallContext } from "./task-call-context.tsx"
import { DataProvider } from "../../lib/data/provider.tsx"
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
    <DataProvider>
      <TaskCallContext
        task={task}
        activeCall={undefined}
        view="task"
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        {...props}
      />
    </DataProvider>,
  )
}

function heading(markup: string) {
  return /<h2 class="([^"]*)">([^<]*)<\/h2>/.exec(markup)
}

test("the Task panel heading fits the narrow column and leaves the number to the conversation header", () => {
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

test("the Task panel names urgency with the rail's words", () => {
  for (const [urgency, label] of [
    ["high_priority", "High priority"],
    ["normal", "Normal priority"],
    ["non_urgent", "Non-urgent"],
  ] as const) {
    const markup = panel({ task: { ...task, urgency } })
    assert.equal(markup.match(/>Urgency<\/span><span[^>]*>([^<]*)</)?.[1], label)
  }
})

test("a completed Task names why it closed and who closed it", () => {
  const completed = {
    ...task,
    origin: "ABITA_AI",
    state: "COMPLETED",
    completedAt: "2026-08-30T13:00:00Z",
  } as const
  const staff = { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" } as const
  for (const [closeReason, summary] of [
    ["DONE", "Done, by ines@abita.test"],
    ["NOT_NEEDED", "Not needed, by ines@abita.test"],
    ["UNREACHABLE", "Couldn&#x27;t reach, by ines@abita.test"],
  ] as const) {
    const markup = panel({ task: { ...completed, completedBy: staff, closeReason } })
    assert.ok(markup.includes(summary), `${closeReason} reads "${summary}"`)
    assert.doesNotMatch(markup, /Close as/)
  }
  const expired = panel({
    task: { ...completed, completedBy: { kind: "SERVICE", subject: "task-expiry" }, closeReason: "EXPIRED" },
  })
  assert.ok(expired.includes("Expired, automatically"))
  const anonymous = { kind: "HUMAN", subject: "user-1" } as const
  assert.ok(panel({ task: { ...completed, completedBy: anonymous, closeReason: "NOT_NEEDED" } }).includes("Not needed, by the team"))
  const withoutReason = panel({ task: { ...completed, completedBy: staff } })
  assert.ok(withoutReason.includes("Completed by ines@abita.test"), "a Task closed before reasons were recorded reads as it did")
  assert.doesNotMatch(withoutReason, /undefined/)
  assert.ok(panel({ task: { ...completed, completedBy: anonymous } }).includes("Completed by the team"))
})

test("an open Task completes as done from the button and offers the other reasons beside it", () => {
  const markup = panel({ task: { ...task, origin: "ABITA_AI" } })
  assert.match(markup, /Complete &amp; next/)
  const menu = /<button[^>]*aria-label="Close as"[^>]*>\s*<svg[^>]*>/.exec(markup)?.[0] ?? ""
  assert.match(menu, /aria-haspopup="menu"/)
  assert.match(menu, /lucide-ellipsis/)
})
