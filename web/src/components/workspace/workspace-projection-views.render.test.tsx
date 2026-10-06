import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps } from "react"
import { createRoot } from "react-dom/client"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "@/components/ui/sidebar"
import { AIInteractionContext } from "./ai-interaction-context.tsx"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { TaskCallContext } from "./task-call-context.tsx"
import { WorkspaceRail } from "./workspace-rail.tsx"
import { clearAccessToken } from "../../lib/auth-client.ts"
import type {
  AiInteractionDetail,
  ConversationTimelineItem,
  Message,
  Task,
} from "../../lib/api/generated/types.gen.ts"
import type { NumberHistory } from "../../lib/workspace-history.ts"
import type {
  WorkspaceProjectionIntent,
  WorkspaceProjectionState,
} from "../../lib/workspace-projection.ts"

test("Task rail toggles the selected context while preserving the conversation", async () => {
  const task = projectedTask()
  const projection = projectedWorkspace(task)
  const railMarkup = renderToStaticMarkup(
    <SidebarProvider>
      <WorkspaceRail
        projection={projection}


        onIntent={() => {}}
      />
    </SidebarProvider>,
  )
  const canvasMarkup = renderToStaticMarkup(
    <EngagementWorkspaceView
      practiceName="Synthetic Practice"
      engagement={projection.selection.engagement!}
      practiceID={projection.scope.practiceID}
      canMutate
      revision={projection.detailRevision}
      selectedTaskID={projection.selection.task?.id}
      onTaskCreated={() => {}}
      onTaskOpen={() => {}}
      onCallOpen={() => {}}
      onAIInteractionOpen={() => {}}
      calling={{
        callingOccupied: false,
        callingEnabled: false,
        outboundPending: false,
        ownsSoftphone: false,
        startOutbound: async () => undefined,
      }}
    />,
  )

  assert.match(railMarkup, /Projected follow-up/)
  assert.match(canvasMarkup, /\(555\) 123-4567/)
  assert.match(canvasMarkup, /aria-label="Message"/)

  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const intents: WorkspaceProjectionIntent[] = []
  try {
    await act(async () => {
      root.render(
        <SidebarProvider>
          <WorkspaceRail
            projection={projection}


            onIntent={(intent) => intents.push(intent)}
          />
        </SidebarProvider>,
      )
    })
  } catch (error) {
    if (error instanceof AggregateError) {
      throw new Error(error.errors.map(String).join("\n"))
    }
    throw error
  }
  const taskButton = Array.from(
    host.querySelectorAll<HTMLButtonElement>("[data-testid='task-row'] button"),
  ).find((button) => button.textContent?.includes(task.title))
  assert.ok(taskButton)
  await act(async () => taskButton.click())
  assert.deepEqual(intents, [{ type: "close-context" }])
  await act(async () => root.render(
    <SidebarProvider>
      <WorkspaceRail
        projection={{ ...projection, selection: { ...projection.selection, contextPanelOpen: false } }}
        onIntent={(intent) => intents.push(intent)}
      />
    </SidebarProvider>,
  ))
  await act(async () => taskButton.click())
  assert.deepEqual(intents, [{ type: "close-context" }, { type: "select-task", task }])
  await act(async () => root.unmount())
  dom.window.close()
})

test("the sidebar shows Manage agent pages in place of Tasks only while Manage agent is open", async () => {
  const projection = projectedWorkspace(projectedTask())
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const intents: WorkspaceProjectionIntent[] = []
  const render = (state: WorkspaceProjectionState) =>
    act(async () => root.render(
      <SidebarProvider>
        <WorkspaceRail
          projection={state}
          locationControl={<button type="button">Workspace selector</button>}
          callingStatus={<p>Calling status</p>}
          onIntent={(intent) => intents.push(intent)}
        />
      </SidebarProvider>,
    ))
  await render({ ...projection, selection: { ...projection.selection, view: "manage-agent", agentPage: "insurance" } })
  const nav = host.querySelector("nav")
  assert.ok(nav)
  assert.equal(document.getElementById(nav.getAttribute("aria-labelledby") ?? "")?.textContent, "Manage agent")
  assert.deepEqual(Array.from(nav.querySelectorAll("button"), (button) => button.textContent), ["Transcripts", "Knowledge base", "Insurance list"])
  assert.equal(nav.querySelector("[aria-current='page']")?.textContent, "Insurance list")
  assert.match(host.textContent ?? "", /Acuity Health.*Workspace selector/)
  assert.match(host.textContent ?? "", /Calling status/)
  assert.equal(host.querySelector("input[aria-label='Search tasks, names, or phone']"), null)
  assert.equal(host.querySelector("[aria-label='Workspace folders']"), null)
  assert.doesNotMatch(host.textContent ?? "", /Recently completed|Projected follow-up/)
  const knowledge = Array.from(nav.querySelectorAll("button")).find((button) => button.textContent === "Knowledge base")!
  await act(async () => knowledge.click())
  assert.deepEqual(intents, [{ type: "select-manage-agent", page: "knowledge" }])

  await render(projection)
  assert.equal(host.querySelector("nav"), null)
  assert.ok(host.querySelector("input[aria-label='Search tasks, names, or phone']"))
  assert.ok(host.querySelector("[aria-label='Workspace folders']"))
  assert.match(host.textContent ?? "", /Recently completed/)
  await act(async () => root.unmount())
  dom.window.close()
})

test("opening AI appointment evidence never clears shared work", async () => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(<AIInteractionContext detail={projectedAIInteraction()} loading={false} error="" />))
  assert.match(host.textContent ?? "", /Appointment booked/)
  assert.doesNotMatch(host.textContent ?? "", /Review status|Mark.*reviewed/)
  await act(async () => root.unmount())
  dom.window.close()
})

test("conversation clears its unavailable alert after a successful refresh", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  conversation.items = [{
    type: "TASK",
    id: task.id,
    occurredAt: task.createdAt,
    taskActivity: "TASK_CREATED",
    task,
  }]
  await conversation.render(0)
  assert.equal(conversation.timelineRequests, 1)
  assert.match(conversation.host.textContent ?? "", /Projected follow-up/)

  conversation.timelineStatus = 503
  await conversation.render(1)
  assert.equal(conversation.timelineRequests, 2)
  assert.match(conversation.host.textContent ?? "", /Projected follow-up/)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /Conversation unavailable.*The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await conversation.render(2)
  assert.equal(conversation.timelineRequests, 3)
  assert.equal(
    Boolean(conversation.host.querySelector("[role='alert']")),
    false,
    "A successful timeline response must clear the conversation unavailable alert",
  )
})

test("an unavailable conversation can be retried without showing an empty history", async (t) => {
  const conversation = conversationHarness(t)
  conversation.timelineStatus = 503
  await conversation.render(0)
  assert.doesNotMatch(conversation.host.textContent ?? "", /No activity yet/)
  const retry = Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  assert.ok(retry, "A failed initial timeline load must offer a retry")

  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 2)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 3)
  assert.equal(Boolean(conversation.host.querySelector("[role='alert']")), false)
  assert.match(conversation.host.textContent ?? "", /No activity yet/)
})

test("a missing access token leaves a retryable conversation error instead of a loading spinner", async (t) => {
  const conversation = conversationHarness(t)
  conversation.tokenStatus = 401
  await conversation.render(0)
  assert.equal(conversation.timelineRequests, 0)
  assert.equal(
    Boolean(conversation.host.querySelector('[aria-label="Loading conversation"]')),
    false,
  )
  const retry = Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  assert.ok(retry)
  conversation.tokenStatus = 200
  await act(async () => retry.click())
  assert.equal(conversation.timelineRequests, 1)
  assert.equal(Boolean(conversation.host.querySelector("[role='alert']")), false)
})

test("the sender office follows the offices of the selected Task", async (t) => {
  const conversation = conversationHarness(t)
  const engagement = projectedWorkspace(projectedTask()).selection.engagement!
  const officeA = { id: "location-a", name: "Office A" }
  const officeB = { id: "location-b", name: "Office B" }
  const started: string[] = []
  const calling = {
    callingOccupied: false,
    callingEnabled: true,
    outboundPending: false,
    ownsSoftphone: true,
    startOutbound: async (locationID: string) => {
      started.push(locationID)
      return undefined
    },
  }
  await conversation.render(0, { canMutate: true, calling, engagement: { ...engagement, locations: [officeA, officeB] } })
  const office = conversation.host.querySelector<HTMLSelectElement>('select[aria-label="Sender office"]')
  assert.ok(office)
  await act(async () => {
    office.value = officeA.id
    office.dispatchEvent(new window.Event("change", { bubbles: true }))
  })

  await conversation.render(0, { canMutate: true, calling, engagement: { ...engagement, locations: [officeB] } })
  const call = Array.from(conversation.host.querySelectorAll("button")).find((button) => button.textContent?.trim() === "Call")
  assert.ok(call)
  await act(async () => call.click())
  assert.deepEqual(started, [officeB.id])
})

test("linked call history stays brief and opens its existing detail panels", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  const occurredAt = "2026-09-05T12:00:00Z"
  conversation.items = [{
    id: "history", type: "CALL_HISTORY", occurredAt,
    entries: [
      { id: "ai", type: "AI_INTERACTION", occurredAt, aiInteraction: {
        id: "ai", locationId: task.locationId, locationName: "Office", sourceCallId: "source", phone: task.phone,
        startedAt: occurredAt, status: "ESCALATED", appointmentOutcome: "BOOKING",
      } },
      { id: "call", type: "CALL", occurredAt, call: {
        id: "call", type: "CALL", direction: "INBOUND", startedAt: occurredAt,
        durationSeconds: 0, locationId: task.locationId, locationName: "Office",
        answeredByEmail: "", transferReason: "", sourceCallId: "source", outcome: "VOICEMAIL",
      } },
      { id: "created", type: "TASK", occurredAt, taskActivity: "TASK_CREATED", task },
    ],
  }]
  await conversation.render(0)
  const timeline = conversation.host.querySelector('[aria-label="Conversation activity"]')!
  assert.equal(timeline.querySelectorAll("button[aria-expanded]").length, 0)
  const call = timeline.querySelector<HTMLButtonElement>('button[aria-label="View AI call: Appointment booked"]')!
  assert.ok(call)
  assert.match(call.textContent!, /Voicemail received/)
  assert.match(call.textContent!, /Projected follow-up/)
  for (const label of ["View AI call", "View voicemail", "View task"]) {
    const button = [...timeline.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.getAttribute("aria-label")?.startsWith(label))
    assert.ok(button, label)
    await act(async () => button.click())
  }
  assert.deepEqual(conversation.opened, ["ai:ai", "call:call", `task:${task.id}`])

})

test("the thread names the callback caller and marks which Task each entry describes", async (t) => {
  const conversation = conversationHarness(t)
  const { selected, review, history } = callbackFixture()
  conversation.items = history
  const histories: NumberHistory[] = []
  await conversation.render(0, { selectedTaskID: selected.id, onHistoryChange: (value) => histories.push(value) })
  const timeline = conversation.host.querySelector('[aria-label="Conversation activity"]')!
  const text = timeline.textContent ?? ""
  assert.match(text, /Outbound call by caller@example\.test/)
  assert.match(text, /No answer/)
  assert.match(text, /Missed call review completed after caller@example\.test's callback attempt/)
  assert.match(text, /This Task created by AI/)
  assert.doesNotMatch(text, /Task completed after callback attempt/)
  assert.ok(timeline.querySelector(`button[aria-label="View task: ${selected.title}"]`))
  assert.ok(timeline.querySelector(`button[aria-label="View task: ${review.title}"]`))
  assert.equal(histories.at(-1)?.phone, selected.phone)
  assert.equal(histories.at(-1)?.items.length, history.length)
})

test("the Task panel credits the callback caller and flags an open Task only with loaded evidence", () => {
  const { selected, review, history } = callbackFixture()
  const panel = (task: Task, numberHistory?: ConversationTimelineItem[]) => renderToStaticMarkup(
    <TaskCallContext
      task={task}
      activeCall={undefined}
      view="task"
      canMutate
      historyHint={0}
      numberHistory={numberHistory}
      taskCallError=""
      onTaskUpdated={() => {}}
      onReturnToCall={() => {}}
    />,
  )
  const open = panel(selected, history)
  assert.match(open, /caller@example\.test called this number at .+ \(no answer\)\. This Task is still open\./)
  assert.ok(open.indexOf("This Task is still open.") < open.indexOf("Complete &amp; next"))
  assert.doesNotMatch(panel(selected), /still open/)
  assert.doesNotMatch(panel(selected, history.filter((item) => item.type !== "CALL_HISTORY")), /still open/)
  assert.match(panel(review, history), /Missed call review completed after caller@example\.test&#x27;s callback attempt/)
  assert.doesNotMatch(panel(review, history), /Completed by the team/)
  assert.match(panel(review), /Completed by the team/)
})

test("completing a Task keeps Next task in place and holds Reopen until the change settles", async () => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const open: Task = { ...projectedTask(), origin: "ABITA_AI" }
  const next: Task = { ...projectedTask(), id: "task-next", title: "Synthetic next Task" }
  const selected: string[] = []
  const render = (task: Task) => act(async () => root.render(
    <TaskCallContext
      task={task}
      taskRows={[task, next]}
      onSelectTask={(row) => selected.push(row.id)}
      activeCall={undefined}
      view="task"
      canMutate
      historyHint={0}
      taskCallError=""
      onTaskUpdated={() => {}}
      onReturnToCall={() => {}}
    />,
  ))
  const actions = () => [...host.querySelectorAll<HTMLButtonElement>("section[aria-label='Focused Task'] div.flex-col > button")]
  const button = (name: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((candidate) => candidate.textContent?.trim() === name)
  try {
    await render(open)
    assert.equal(actions()[0]?.textContent?.trim(), "Complete & next")
    await render({ ...open, state: "COMPLETED", version: 2, completedAt: "2026-08-30T12:05:00Z", completedBy: { kind: "HUMAN", subject: "user-1", email: "staff@example.test" } })
    assert.equal(actions()[0]?.textContent?.trim(), "Next task", "the primary position keeps a forward action")
    assert.equal(button("Reopen")?.disabled, true, "a Reopen click landing during the flip is ignored")
    await act(async () => button("Reopen")!.click())
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1_100)) })
    assert.equal(button("Reopen")?.disabled, false)
    await act(async () => button("Next task")!.click())
    assert.deepEqual(selected, [next.id])
  } finally {
    await act(async () => root.unmount())
    dom.window.close()
  }
})

test("a text conversation keeps Reopen away from Mark done and holds it until the change settles", async (t) => {
  const conversation = conversationHarness(t)
  const task = { ...projectedTask(), origin: "INBOUND_MESSAGE_REVIEW" as const }
  conversation.textTask = task
  await conversation.render(0, { onNextTask: () => {} })
  const header = () => [...conversation.host.querySelectorAll<HTMLButtonElement>("header button")].map((button) => button.textContent?.trim())
  assert.equal(header().at(-1), "Mark done")
  conversation.textTask = { ...task, state: "COMPLETED", version: 2 }
  await conversation.render(1, { onNextTask: () => {} })
  assert.equal(header().at(-1), "Next task", "Mark done's position becomes a forward action")
  const reopen = () => [...conversation.host.querySelectorAll<HTMLButtonElement>("header button")].find((button) => button.textContent?.trim() === "Reopen")!
  assert.equal(reopen().disabled, true)
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1_100)) })
  assert.equal(reopen().disabled, false)
})

function callbackFixture() {
  const selected: Task = {
    ...projectedTask(), id: "task-ai", origin: "ABITA_AI", title: "Synthetic refill question",
    createdBy: { kind: "SERVICE", subject: "abita-ai" }, createdAt: "2026-09-08T13:00:00Z",
  }
  const review: Task = {
    ...projectedTask(), id: "task-review", origin: "MISSED_CALL_RECOVERY", recoveryOutcome: "MISSED_CALL",
    title: "Return missed call", state: "COMPLETED", createdAt: "2026-09-08T12:50:00Z",
    completedAt: "2026-09-08T13:56:00Z", completedBy: { kind: "SERVICE", subject: "work-recovery-resolution" },
  }
  const history: ConversationTimelineItem[] = [
    { id: "created", type: "TASK", occurredAt: selected.createdAt, taskActivity: "TASK_CREATED", task: selected },
    { id: "callback-history", type: "CALL_HISTORY", occurredAt: "2026-09-08T13:55:00Z", entries: [
      { id: "callback", type: "CALL", occurredAt: "2026-09-08T13:55:00Z", call: {
        id: "callback", type: "CALL", direction: "OUTBOUND", startedAt: "2026-09-08T13:55:00Z",
        durationSeconds: 0, locationId: review.locationId, locationName: review.locationName,
        answeredByEmail: "", placedByEmail: "caller@example.test", transferReason: "", outcome: "UNANSWERED",
      } },
    ] },
    { id: "completion", type: "TASK", occurredAt: "2026-09-08T13:56:00Z", task: review,
      taskActivity: "TASK_AUTO_COMPLETED_CALLBACK_ATTEMPT",
      taskActivityDetails: { callId: "callback", callerSubject: "caller", callerEmail: "caller@example.test" } },
  ]
  return { selected, review, history }
}

test("image download finishing after navigation does not allocate an object URL", async (t) => {
  const view = attachmentHarness(t)
  await view.render(0)
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  assert.equal(view.attachmentRequests, 1)
  await act(async () => view.root.unmount())
  await act(async () => view.release())
  assert.equal(view.createURL.mock.callCount(), 0)
})

test("loaded attachment previews revoke their object URL on navigation", async (t) => {
  const view = attachmentHarness(t)
  await view.render(0)
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)) })
  await act(async () => view.release())
  assert.equal(view.host.querySelector("img")?.getAttribute("src"), "blob:synthetic-image")
  assert.equal(view.createURL.mock.callCount(), 1)
  assert.equal(view.revokeURL.mock.callCount(), 0)
  await act(async () => view.root.unmount())
  assert.deepEqual(view.revokeURL.mock.calls.map((call) => call.arguments), [["blob:synthetic-image"]])
})

test("a refused send keeps its draft attempt and a sent Message reloads after its hold", async (t) => {
  const conversation = conversationHarness(t)
  await conversation.render(0, { canMutate: true })
  assert.equal(conversation.timelineRequests, 1)
  const composer = conversation.host.querySelector<HTMLFormElement>('form[aria-label="Message composer"]')!
  const textarea = composer.querySelector("textarea")!
  await typeInto(textarea, "Synthetic reply")
  const submit = () => act(async () => {
    composer.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 0))
  })

  conversation.sendStatus = 409
  await submit()
  assert.match(
    composer.querySelector("[role='alert']")?.textContent ?? "",
    /This destination cannot be messaged from the selected office\./,
  )
  conversation.sendStatus = 201
  await submit()
  assert.equal(conversation.sends.length, 2)
  assert.equal(conversation.sends[0]!.destination, "+15551234567")
  assert.equal(conversation.sends[1]!.idempotencyKey, conversation.sends[0]!.idempotencyKey, "a retried draft reuses its idempotency key")
  assert.match(conversation.host.querySelector('[aria-label="Conversation activity"]')?.textContent ?? "", /Synthetic reply/)
  assert.equal(conversation.timelineRequests, 1)

  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 800)) })
  assert.equal(conversation.timelineRequests, 2, "the Sending Message reloads once its hold ends")
})

test("a sent Message does not reload its conversation after navigation", async (t) => {
  const conversation = conversationHarness(t)
  await conversation.render(0, { canMutate: true })
  const composer = conversation.host.querySelector<HTMLFormElement>('form[aria-label="Message composer"]')!
  const textarea = composer.querySelector("textarea")!
  await typeInto(textarea, "Synthetic reply")
  await act(async () => {
    composer.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.equal(conversation.sends.length, 1)
  await act(async () => conversation.root.unmount())
  await new Promise((resolve) => setTimeout(resolve, 800))
  assert.equal(conversation.timelineRequests, 1)
})

async function typeInto(textarea: HTMLTextAreaElement, value: string) {
  Object.assign(textarea, { attachEvent() {}, detachEvent() {} })
  await act(async () => {
    textarea.focus()
    Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, "value")!.set!.call(textarea, value)
    textarea.dispatchEvent(new window.KeyboardEvent("keyup", { bubbles: true }))
  })
}

function attachmentHarness(t: TestContext) {
  const view = conversationHarness(t)
  let release!: () => void
  view.attachmentGate = new Promise<void>((resolve) => { release = resolve })
  view.items = [{ type: "MESSAGE", id: message.id, occurredAt: timestamp, message }]
  t.after(release)
  return Object.assign(view, {
    release,
    createURL: t.mock.method(URL, "createObjectURL", () => "blob:synthetic-image"),
    revokeURL: t.mock.method(URL, "revokeObjectURL", () => {}),
  })
}

const timestamp = "2026-09-25T12:00:00Z"
const message: Message = {
  id: "message-1", direction: "INBOUND", body: "Synthetic attachment", sender: "+15551234567", destination: "+15557654321", delivery: "Delivered", version: 1,
  createdAt: timestamp, updatedAt: timestamp,
  thread: { id: "thread-1", practiceId: "practice-1", locationId: "location-1", locationName: "Test location", officePhone: "+15557654321", externalPhone: "+15551234567", outboundBlocked: false, createdAt: timestamp, updatedAt: timestamp },
  attachment: { id: "attachment-1", direction: "INBOUND", state: "Stored", fileName: "synthetic.png", contentType: "image/png", byteSize: 15, createdAt: timestamp, updatedAt: timestamp },
}

function conversationHarness(t: TestContext) {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const conversation = {
    host,
    root,
    render,
    attachmentRequests: 0,
    attachmentGate: undefined as Promise<void> | undefined,
    timelineStatus: 200,
    timelineRequests: 0,
    tokenStatus: 200,
    textTask: undefined as Task | undefined,
    timelineGate: undefined as Promise<void> | undefined,
    completions: [] as number[],
    sends: [] as { idempotencyKey: string; destination?: string; body: string }[],
    sendStatus: 201,
    items: [] as ConversationTimelineItem[],
    opened: [] as string[],
  }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url =
      typeof input === "string"
        ? input
        : input instanceof URL
          ? input.href
          : input.url
    if (url === "/api/auth/token") {
      return Response.json(
        { token: "synthetic-token" },
        { status: conversation.tokenStatus },
      )
    }
    if (url.includes("/attachments/")) {
      conversation.attachmentRequests++
      await conversation.attachmentGate
      return new Response(new Blob(["synthetic image"], { type: "image/png" }), { headers: { "Content-Type": "image/png" } })
    }
    if (url.endsWith("/v1/messages")) {
      const body = await (input as Request).json()
      conversation.sends.push(body)
      return conversation.sendStatus === 201
        ? Response.json({ status: "created", message: { ...message, id: `sent-${conversation.sends.length}`, direction: "OUTBOUND", body: body.body, delivery: "Sending", attachment: undefined } }, { status: 201 })
        : Response.json({ error: { code: "DESTINATION_UNAVAILABLE", message: "Synthetic conflict", correlationId: "correlation-1", retryable: false } }, { status: conversation.sendStatus })
    }
    if (url.includes("/v1/tasks/") && url.endsWith("/complete")) {
      const body = await (input as Request).json()
      conversation.completions.push(body.expectedVersion)
      return Response.json({ ...conversation.textTask, state: "COMPLETED" })
    }
    assert.match(url, /\/v1\/engagements\/%2B15551234567\/timeline\?/)
    conversation.timelineRequests += 1
    await conversation.timelineGate
    return conversation.timelineStatus === 200
      ? Response.json({ items: conversation.items, nextCursor: "" })
      : Response.json(
          { code: "UNAVAILABLE" },
          { status: conversation.timelineStatus },
        )
  })
  const projection = projectedWorkspace(projectedTask())
  async function render(
    revision: number,
    overrides: Partial<ComponentProps<typeof EngagementWorkspaceView>> = {},
  ) {
    await act(async () => {
      root.render(
        <EngagementWorkspaceView
          practiceName="Synthetic Practice"
          engagement={projection.selection.engagement!}
          practiceID={projection.scope.practiceID}
          canMutate={false}
          textTask={conversation.textTask}
          onTextTaskUpdated={() => {}}
          revision={revision}
          onTaskCreated={() => {}}
          onTaskOpen={(task) => conversation.opened.push(`task:${task.id}`)}
          onCallOpen={(id) => conversation.opened.push(`call:${id}`)}
          onAIInteractionOpen={(id) => conversation.opened.push(`ai:${id}`)}
          calling={{
            callingOccupied: false,
            callingEnabled: false,
            outboundPending: false,
            ownsSoftphone: false,
            startOutbound: async () => undefined,
          }}
          {...overrides}
        />,
      )
    })
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
  t.after(async () => {
    await act(async () => root.unmount())
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
    dom.window.close()
  })
  return conversation
}

test("text completion requires a successful conversation reload for the current Task version", async (t) => {
  const conversation = conversationHarness(t)
  const task = { ...projectedTask(), origin: "INBOUND_MESSAGE_REVIEW" as const }
  const done = () => Array.from(conversation.host.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent === "Mark done")!
  conversation.textTask = task
  let release!: () => void
  conversation.timelineGate = new Promise<void>((resolve) => { release = resolve })
  await conversation.render(0)
  assert.equal(done().disabled, true, "initial loading has no reviewed evidence")
  conversation.textTask = { ...task, version: 2 }
  await conversation.render(0)
  assert.equal(done().disabled, true, "a Task change during initial loading stays unreviewed")
  await act(async () => { release(); await conversation.timelineGate })
  assert.equal(done().disabled, false)

  conversation.timelineGate = new Promise<void>((resolve) => { release = resolve })
  conversation.textTask = { ...task, version: 3 }
  await conversation.render(1)
  assert.equal(done().disabled, true, "new Task version is not proof the new messages loaded")
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [])
  conversation.timelineStatus = 503
  await act(async () => { release(); await conversation.timelineGate })
  assert.equal(done().disabled, true, "failed refresh must leave unseen work pending")

  conversation.timelineStatus = 200
  conversation.timelineGate = undefined
  await conversation.render(2)
  assert.equal(done().disabled, false)
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [3], "completion submits the version whose conversation loaded")
})

function installDOM() {
  const dom = new JSDOM("<!doctype html><html><body></body></html>", {
    url: "http://localhost/workspace",
  })
  Object.defineProperties(globalThis, {
    window: { configurable: true, value: dom.window },
    document: { configurable: true, value: dom.window.document },
    navigator: { configurable: true, value: dom.window.navigator },
    HTMLElement: { configurable: true, value: dom.window.HTMLElement },
    Element: { configurable: true, value: dom.window.Element },
    Node: { configurable: true, value: dom.window.Node },
    MouseEvent: { configurable: true, value: dom.window.MouseEvent },
    getComputedStyle: {
      configurable: true,
      value: dom.window.getComputedStyle.bind(dom.window),
    },
    IS_REACT_ACT_ENVIRONMENT: { configurable: true, value: true },
  })
  Object.defineProperty(dom.window, "matchMedia", {
    configurable: true,
    value: () => ({
      addEventListener() {},
      matches: false,
      removeEventListener() {},
    }),
  })
  Object.defineProperties(dom.window, {
    requestAnimationFrame: {
      configurable: true,
      value: (callback: FrameRequestCallback) =>
        globalThis.setTimeout(() => callback(Date.now()), 0),
    },
    cancelAnimationFrame: {
      configurable: true,
      value: (id: number) => globalThis.clearTimeout(id),
    },
  })
  Object.defineProperty(dom.window.HTMLElement.prototype, "scrollTo", {
    configurable: true,
    value() {},
  })
  return dom
}

function projectedWorkspace(task: Task): WorkspaceProjectionState {
  return {
    loadState: "ready",
    connection: "connected",
    discovery: {
      actor: {
        type: "HUMAN",
        subject: "user-1",
        email: "staff@abita.test",
      },
      platformOperator: false,
      practices: [
        {
          id: "practice-1",
          name: "Example Eye Group",
          version: 1,
          callingEnabled: false,
          membership: {
            id: "membership-1",
            role: "STAFF",
            locationScope: "ALL",
          },
          locations: [{ id: "location-1", name: "Downtown" }],
        },
      ],
    },
    workspace: {
      schemaVersion: "2026-07-24",
      version: 1,
      state: "EMPTY",
      actor: {
        type: "HUMAN",
        subject: "user-1",
        email: "staff@abita.test",
      },
      practice: { id: "practice-1", name: "Example Eye Group", version: 1 },
      location: { id: "location-1", name: "Downtown" },
      platformOperator: false,
      navigation: [],
    },
    scope: {
      practiceID: "practice-1",
      locationID: "location-1",
      locationScopeID: "",
    },
    search: { input: "", applied: "", error: "" },
    tasks: {
      items: [task],
      nextCursor: "",
      loading: false,
      error: "",
      counts: {
        tasks: 1,
        categories: {
          billing: 0,
          appointments: 0,
          documentation: 0,
          optical: 0,
          medication: 0,
          referrals: 0,
          other: 1,
        },
      },
    },
    completedTasks: { items: [], nextCursor: "", loading: false, error: "" },
    selection: {
      task,
      taskError: "",
      engagement: {
        phone: task.phone,
        locations: [{ id: "location-1", name: "Downtown" }],
        latestActivity: task.updatedAt,
        openTaskCount: 1,
      },
      aiInteractionID: "",
      aiInteractionLoading: false,
      aiInteractionError: "",
      view: "engagement",
      agentPage: "transcripts",
      contextView: "task",
      contextPanelOpen: true,
    },
    detailRevision: 0,
    completion: { pendingTaskID: "", errorTaskID: "", error: "" },
    rail: {
      expanded: ["tasks"],
      taskCategory: "all",
      scrollTop: 0,
    },
  }
}

function projectedTask(): Task {
  return {
    id: "task-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    phone: "+15551234567",
    title: "Projected follow-up",
    state: "OPEN",
    origin: "STAFF_MESSAGE_FOLLOW_UP",
    urgency: "normal",
    createdBy: { kind: "HUMAN", subject: "user-1" },
    createdAt: "2026-08-30T12:00:00Z",
    version: 1,
    updatedAt: "2026-08-30T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
  }
}

function projectedAIInteraction(): AiInteractionDetail {
  return {
    id: "interaction-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    sourceCallId: "call-1",
    phone: "+15551234567",
    officePhone: "+15557654321",
    startedAt: "2026-08-30T12:00:00Z",
    endedAt: "2026-08-30T12:05:00Z",
    status: "COMPLETED",
    appointmentOutcome: "BOOKING",
    appointment: {},
    createdAt: "2026-08-30T12:05:00Z",
    updatedAt: "2026-08-30T12:05:00Z",
  }
}
