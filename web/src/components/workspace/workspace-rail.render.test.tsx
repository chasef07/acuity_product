import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { SidebarProvider } from "../ui/sidebar.tsx"
import type { Task } from "../../lib/api/generated/types.gen.ts"
import { TaskRow } from "./workspace-rail-task-row.tsx"
import { WorkspaceWindowFailure } from "./workspace-window-failure.tsx"

test("workspace window failure exposes one accessible retry control", () => {
  const failure = (
    <WorkspaceWindowFailure
      message="Texts are temporarily unavailable."
      onRetry={() => {}}
    />
  )
  const html = renderToStaticMarkup(failure)

  assert.match(html, /role="alert"/)
  assert.match(html, /Texts are temporarily unavailable\./)
  assert.match(html, /<button type="button"[^>]*>Retry<\/button>/)
})

const railTask: Task = {
  id: "task-1",
  practiceId: "practice-1",
  locationId: "location-1",
  locationName: "Downtown",
  phone: "+15551234567",
  title: "Call back about refill",
  state: "OPEN",
  origin: "ABITA_AI",
  urgency: "normal",
  createdBy: { kind: "SERVICE", subject: "abita" },
  createdAt: "2026-08-30T12:00:00Z",
  version: 1,
  updatedAt: "2026-08-30T12:00:00Z",
  relatedInteractionCount: 0,
  interactions: [],
}

function row(task: Task) {
  return renderToStaticMarkup(
    <SidebarProvider>
      <TaskRow
        task={task}
        active={false}
        onSelect={() => {}}
        completionDisabled={false}
        completionPending={false}
        completionError=""
        onComplete={() => {}}
      />
    </SidebarProvider>,
  )
}

test("an open Task row ticks as done and offers the other reasons in a menu", () => {
  const html = row(railTask)
  assert.match(html, /aria-label="Complete Task: Call back about refill"/)
  const menu = /<button[^>]*aria-label="Close Task as: Call back about refill"[^>]*>/.exec(html)?.[0] ?? ""
  assert.match(menu, /aria-haspopup="menu"/)
})

test("a completed Task row reads why it closed and who closed it", () => {
  const html = row({
    ...railTask,
    state: "COMPLETED",
    completedBy: { kind: "HUMAN", subject: "user-1", email: "ines@abita.test" },
    completedAt: "2026-08-30T13:00:00Z",
    closeReason: "NOT_NEEDED",
    version: 2,
  })
  assert.match(html, /Not needed, by ines@abita\.test/)
  assert.doesNotMatch(html, /Close Task as/)
})
