import assert from "node:assert/strict"
import test, { type TestContext } from "node:test"
import { act, type ComponentProps, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"

import { SidebarProvider } from "@/components/ui/sidebar"
import { AIInteractionContext } from "./ai-interaction-context.tsx"
import { EngagementWorkspaceView } from "./engagement-workspace.tsx"
import { DetailsAbovePatientTasks, TaskCallContext } from "./task-call-context.tsx"
import { TaskCallAction } from "./task-call-action.tsx"
import { VoicemailSource } from "./recording-source.tsx"
import { CallingDock } from "./calling-dock.tsx"
import { WorkEmptyState } from "./work-empty-state.tsx"
import { CallingRailControl, RailButton } from "./workspace-rail-button.tsx"
import { WorkspaceRail } from "./workspace-rail.tsx"
import { WorkspaceTopBar } from "./workspace-page-header.tsx"
import { projectedTask, projectedWorkspace } from "./workspace-projection.fixture.ts"
import { clearAccessToken } from "../../lib/auth-client.ts"
import { DataProvider } from "../../lib/data/provider.tsx"
import { createWorkspaceAuthorityAdapter } from "../../lib/workspace-projection-http.ts"
import { useLiveRefresh } from "../../lib/data/queries.ts"
import type {
  AiInteractionDetail,
  ConversationTimelineItem,
  ConversationTimelinePage,
  Message,
  PhoneSummary,
  Task,
} from "../../lib/api/generated/types.gen.ts"
import type { NumberHistory } from "../../lib/workspace-history.ts"
import type {
  WorkspaceProjectionIntent,
  WorkspaceProjectionState,
} from "../../lib/workspace-projection.ts"

test("an accessible practice renders calling navigation and the Task call action", () => {
  const markup = renderToStaticMarkup(
    <DataProvider>
    <SidebarProvider>
      <CallingDock
        actorSubject="calling-member"
        practiceID="practice-1"
        onTaskCallHandled={() => {}}
        onCallScope={() => {}}
        onCallConnected={() => {}}
        onDisposition={() => {}}
      >
        {(_activeCall, occupied) => <>
          <CallingRailControl workActive />
          <TaskCallAction task={projectedTask()} canCall={!occupied} pending={false} onCall={() => {}} />
        </>}
      </CallingDock>
    </SidebarProvider>
    </DataProvider>,
  )
  assert.match(markup, /aria-label="Receive calls"/)
  assert.match(markup, /role="switch"/)
  assert.match(markup, /Checking Call route/)
  assert.match(markup, /<audio/)
})

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
    <DataProvider>
    <EngagementWorkspaceView
      practiceName="Synthetic Practice"
      engagement={projection.selection.engagement!}
      practiceID={projection.scope.practiceID}
      selectedTaskID={projection.selection.task?.id}
      patientTasksOpen={false}
      onPatientTasks={() => {}}
      onTaskCreated={() => {}}
      onTaskOpen={() => {}}
      onCallOpen={() => {}}
      onAIInteractionOpen={() => {}}
      calling={{
        callingOccupied: false,
        outboundPending: false,
        ownsSoftphone: false,
        startOutbound: async () => undefined,
      }}
    />
    </DataProvider>,
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
    host.querySelectorAll<HTMLButtonElement>("[data-testid='patient-row'] button"),
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
  const nav = Array.from(host.querySelectorAll("nav")).find((element) =>
    (element.getAttribute("aria-label") ?? document.getElementById(element.getAttribute("aria-labelledby") ?? "")?.textContent) === "Manage agent")
  assert.ok(nav)
  assert.deepEqual(Array.from(nav.querySelectorAll("button"), (button) => button.textContent), ["Home", "Calls", "Knowledge base", "Insurance list"])
  assert.equal(nav.querySelector("[aria-current='page']")?.textContent, "Insurance list")
  assert.match(host.textContent ?? "", /Acuity Health/)
  assert.match(host.textContent ?? "", /Workspace selector/)
  assert.doesNotMatch(host.textContent ?? "", /Calling status/)
  assert.equal(host.querySelector("input[aria-label='Search tasks, names, or phone']"), null)
  assert.equal(host.querySelector("[aria-label='Patients with open work']"), null)
  assert.doesNotMatch(host.textContent ?? "", /Recently completed|Projected follow-up/)
  const knowledge = Array.from(nav.querySelectorAll("button")).find((button) => button.textContent === "Knowledge base")!
  await act(async () => knowledge.click())
  assert.deepEqual(intents, [{ type: "select-manage-agent", page: "knowledge" }])

  await render(projection)
  assert.equal(host.querySelector("nav"), null)
  assert.ok(host.querySelector("input[aria-label='Search tasks, names, or phone']"))
  assert.ok(host.querySelector("[aria-label='Patients with open work']"))
  assert.match(host.textContent ?? "", /Recently completed/)
  assert.match(host.textContent ?? "", /Calling status/)
  await act(async () => root.unmount())
  dom.window.close()
})

test("analytics sidebars keep the brand and office picker and drop the patient list, search, and calling status", () => {
  const projection = projectedWorkspace(projectedTask())
  for (const view of ["analytics", "operator-analytics"] as const) {
    const html = renderToStaticMarkup(
      <SidebarProvider>
        <WorkspaceRail
          projection={{ ...projection, selection: { ...projection.selection, view } }}
          locationControl={<button type="button">Workspace selector</button>}
          callingStatus={<p>Calling status</p>}
          onIntent={() => {}}
        />
      </SidebarProvider>,
    )
    assert.match(html, /Acuity Health/)
    assert.match(html, /Workspace selector/)
    assert.doesNotMatch(html, /Search tasks, names, or phone|Patients with open work|Recently completed|Projected follow-up|Calling status/)
  }
})

test("Work without a selected task names whether the inbox is clear or waiting for a choice", () => {
  const projection = projectedWorkspace(projectedTask())
  const none = { ...projection.selection, view: "none" as const, task: undefined, engagement: undefined }
  const emptyCounts = { ...projection.patients.counts, all: 0 }
  const waiting = renderToStaticMarkup(<WorkEmptyState projection={{ ...projection, selection: none }} />)
  assert.match(waiting, /Select a patient/)
  assert.doesNotMatch(waiting, /Inbox clear/)
  const clear = renderToStaticMarkup(<WorkEmptyState projection={{ ...projection, selection: none, patients: { ...projection.patients, items: [], counts: emptyCounts } }} />)
  assert.match(clear, /Inbox clear/)
  const loading = renderToStaticMarkup(<WorkEmptyState projection={{ ...projection, selection: none, patients: { ...projection.patients, items: [], loading: true, counts: emptyCounts } }} />)
  assert.doesNotMatch(loading, /Inbox clear/)
})

test("breadcrumbs mark only the current page and only offer navigation when a destination exists", () => {
  for (const props of [
    { section: "Home" },
    { section: "Home", page: "Knowledge base", onSection: () => {} },
    { section: "Work", page: "Review appointment" },
  ]) {
    const html = renderToStaticMarkup(<WorkspaceTopBar {...props} />)
    assert.equal(html.match(/aria-current="page"/g)?.length, 1)
    assert.equal(html.includes("<button"), Boolean(props.onSection))
    assert.match(html, new RegExp(`aria-current="page"[^>]*>${props.page ?? props.section}<`))
  }
})

test("rail buttons are icon-only and keep their accessible names", () => {
  for (const label of ["Home", "Command Center"]) {
    const html = renderToStaticMarkup(
      <RailButton label={label} active={label === "Home"} onClick={() => {}}>
        <svg />
      </RailButton>,
    )
    assert.match(html, new RegExp(`aria-label="${label}"`))
    assert.doesNotMatch(html, /text-\[10px\]/)
    assert.equal(/aria-current="page"/.test(html), label === "Home")
    assert.doesNotMatch(html, /aria-describedby/)
  }
})

test("the calling rail control names its warning dot", () => {
  const html = renderToStaticMarkup(
    <RailButton
      label="Receive calls"
      role="switch"
      aria-checked
      attention="Calling is open elsewhere. Fix it from Work."
      onClick={() => {}}
    >
      <svg />
    </RailButton>,
  )
  const describedBy = html.match(/aria-describedby="([^"]+)"/)?.[1]
  assert.ok(describedBy)
  assert.match(html, new RegExp(`id="${describedBy}"[^>]*>Calling is open elsewhere\\. Fix it from Work\\.<`))
  assert.match(html, /role="switch"/)
  assert.doesNotMatch(html, />Calls</)
  assert.doesNotMatch(html, /aria-current/)
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

test("an AI call keeps its title above the scrolling evidence, and the patient's Tasks below both", () => {
  const page = new JSDOM(renderToStaticMarkup(
    <DetailsAbovePatientTasks patientTasks={<p>patient tasks</p>}>
      <AIInteractionContext detail={projectedAIInteraction()} loading={false} error="" />
    </DetailsAbovePatientTasks>,
  )).window.document
  const title = page.querySelector("h2")
  const header = title?.closest("header")
  const scroller = page.querySelector('[class~="overflow-y-auto"]')
  const tasks = [...page.querySelectorAll("p")].find((element) => element.textContent === "patient tasks")
  assert.equal(title?.textContent, "Appointment booked")
  assert.ok(header && scroller && tasks)
  assert.ok(header.classList.contains("shrink-0") && header.classList.contains("min-h-11") && header.classList.contains("pr-12"))
  assert.ok(!scroller.contains(title), "the title stays put")
  assert.ok(scroller.querySelector("details"), "the evidence scrolls")
  assert.ok(header.compareDocumentPosition(scroller) & page.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING)
  assert.ok(!scroller.contains(tasks), "the list is its own region")
  assert.ok(scroller.compareDocumentPosition(tasks) & page.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING)
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
  await waitFor(() => /Projected follow-up/.test(conversation.host.textContent ?? ""))
  assert.equal(conversation.timelineRequests, 1)

  conversation.timelineStatus = 503
  await conversation.render(1)
  await waitFor(() => Boolean(conversation.host.querySelector("[role='alert']")))
  assert.equal(conversation.timelineRequests, 2)
  assert.match(conversation.host.textContent ?? "", /Projected follow-up/)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /Conversation unavailable.*The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await conversation.render(2)
  await waitFor(() => !conversation.host.querySelector("[role='alert']"))
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
  const retry = () => Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  await waitFor(() => retry() !== undefined)
  assert.doesNotMatch(conversation.host.textContent ?? "", /No activity yet/)

  await act(async () => retry()!.click())
  await waitFor(() => conversation.timelineRequests === 2 && retry()?.disabled === false)
  assert.match(
    conversation.host.querySelector("[role='alert']")?.textContent ?? "",
    /The conversation could not be loaded\./,
  )

  conversation.timelineStatus = 200
  await act(async () => retry()!.click())
  await waitFor(() => !conversation.host.querySelector("[role='alert']"))
  assert.equal(conversation.timelineRequests, 3)
  assert.match(conversation.host.textContent ?? "", /No activity yet/)
})

test("a missing access token leaves a retryable conversation error instead of a loading spinner", async (t) => {
  const conversation = conversationHarness(t)
  conversation.tokenStatus = 401
  await conversation.render(0)
  const tryAgain = () => Array.from(conversation.host.querySelectorAll("button")).find(
    (button) => button.textContent === "Try again",
  )
  await waitFor(() => tryAgain() !== undefined)
  assert.equal(conversation.timelineRequests, 0)
  assert.equal(
    Boolean(conversation.host.querySelector('[aria-label="Loading conversation"]')),
    false,
  )
  conversation.tokenStatus = 200
  await act(async () => tryAgain()!.click())
  await waitFor(() => !conversation.host.querySelector("[role='alert']"))
  assert.equal(conversation.timelineRequests, 1)
})

test("the sender office follows the offices of the selected Task", async (t) => {
  const conversation = conversationHarness(t)
  const engagement = projectedWorkspace(projectedTask()).selection.engagement!
  const officeA = { id: "location-a", name: "Office A" }
  const officeB = { id: "location-b", name: "Office B" }
  const started: string[] = []
  const calling = {
    callingOccupied: false,
    outboundPending: false,
    ownsSoftphone: true,
    startOutbound: async (locationID: string) => {
      started.push(locationID)
      return undefined
    },
  }
  await conversation.render(0, { calling, engagement: { ...engagement, locations: [officeA, officeB] } })
  const office = conversation.host.querySelector<HTMLSelectElement>('select[aria-label="Sender office"]')
  assert.ok(office)
  await act(async () => {
    office.value = officeA.id
    office.dispatchEvent(new window.Event("change", { bubbles: true }))
  })

  await conversation.render(0, { calling, engagement: { ...engagement, locations: [officeB] } })
  const call = Array.from(conversation.host.querySelectorAll("button")).find((button) => button.textContent?.trim() === "Call")
  assert.ok(call)
  await act(async () => call.click())
  assert.deepEqual(started, [officeB.id])
})

function callLineFixture(
  call: Partial<NonNullable<ConversationTimelineItem["call"]>>,
  { ai = true, task }: { ai?: boolean; task?: Task } = {},
): ConversationTimelineItem {
  const location = projectedTask()
  const occurredAt = "2026-09-05T12:00:00Z"
  const id = call.id ?? "call"
  return {
    id: `history-${id}`, type: "CALL_HISTORY", occurredAt,
    entries: [
      ...(ai ? [{ id: `ai-${id}`, type: "AI_INTERACTION" as const, occurredAt, aiInteraction: {
        id: `ai-${id}`, locationId: location.locationId, locationName: "Office", sourceCallId: "source", phone: location.phone,
        startedAt: occurredAt, status: "ESCALATED" as const, appointmentOutcome: "BOOKING" as const,
        summary: "Caller booked a visit and asked about insurance.", resolved: false,
      } }] : []),
      { id, type: "CALL", occurredAt, call: {
        id, type: "CALL", direction: "INBOUND", startedAt: occurredAt,
        durationSeconds: 0, locationId: location.locationId, locationName: "Office",
        answeredByEmail: "", transferReason: "", sourceCallId: "source", outcome: "MISSED", ...call,
      } },
      ...(task ? [{ id: `created-${task.id}`, type: "TASK" as const, occurredAt, taskActivity: "TASK_CREATED" as const, task }] : []),
    ],
  }
}

function callLines(host: HTMLElement) {
  const timeline = host.querySelector('[aria-label="Conversation activity"]')!
  const lines = () => [...timeline.querySelectorAll<HTMLButtonElement>("button[aria-expanded]")]
  const outcome = (line: HTMLButtonElement) => line.lastElementChild!
  const button = (name: string) => [...timeline.querySelectorAll<HTMLButtonElement>("button")]
    .find((element) => (element.getAttribute("aria-label") ?? element.textContent?.trim()) === name)
  return { timeline, lines, outcome, button }
}

test("a call reads as one line with its outcome and expands in place to its steps, Tasks and detail panels", async (t) => {
  const conversation = conversationHarness(t)
  const task: Task = {
    ...projectedTask(), state: "COMPLETED", closeReason: "UNREACHABLE", completedAt: "2026-09-06T12:00:00Z",
    completedBy: { kind: "HUMAN", subject: "staff", email: "staff@example.test" },
  }
  conversation.items = [callLineFixture({
    outcome: "RESOLVED", answeredByEmail: "dana@example.test", durationSeconds: 362, transferReason: "Insurance question",
  }, { task })]
  await conversation.render(0)
  const { timeline, lines, outcome, button } = callLines(conversation.host)
  assert.equal(lines().length, 1)
  const line = lines()[0]!
  assert.equal(line.getAttribute("aria-expanded"), "false")
  const summary = line.querySelector('[data-slot="call-line-summary"]')!
  const time = line.querySelector("time")!
  assert.equal(summary.textContent, "Called in · Insurance question")
  assert.match(time.textContent!, /^\d+:\d+\s[AP]M$/)
  assert.ok(!summary.contains(time), "the time is not part of the text that truncates")
  assert.equal(time.closest('[class*="line-clamp"], [class~="truncate"]'), null, "nothing that clamps holds the time")
  assert.match(summary.className, /line-clamp-2/)
  assert.match(time.className, /whitespace-nowrap/)
  assert.match(line.getAttribute("aria-label")!, /^Called in, Insurance question, \d+:\d+\s[AP]M, dana@example\.test answered$/)
  assert.equal(outcome(line).textContent, "dana@example.test answered")
  assert.match(outcome(line).className, /text-success/)
  assert.doesNotMatch(timeline.textContent!, /AI answered|Projected follow-up|View AI call/)

  await act(async () => line.click())
  assert.equal(lines()[0]!.getAttribute("aria-expanded"), "true")
  const expanded = timeline.textContent!
  assert.match(expanded, /AI answered and transferred: Insurance question/)
  assert.match(expanded, /Appointment booked/)
  assert.match(expanded, /Caller booked a visit and asked about insurance\./)
  assert.match(expanded, /dana@example\.test answered, 6m 2s/)
  assert.match(expanded, /Resolved on call/)
  assert.match(expanded, /Projected follow-up · Couldn't reach, by staff@example\.test/)
  for (const name of ["View AI call", "View call", `View task: ${task.title}`]) {
    const action = button(name)
    assert.ok(action, name)
    await act(async () => action.click())
  }
  assert.deepEqual(conversation.opened, ["ai:ai-call", "call:call", `task:${task.id}`])
})

test("a line with a very long Task title or summary still shows its time outside the clamped text", async (t) => {
  const conversation = conversationHarness(t)
  const title = "Call the patient back about the progressive lens remake, the frame warranty claim and the insurance resubmission for the second pair"
  conversation.items = [
    callLineFixture({ id: "callback", direction: "OUTBOUND", outcome: "UNANSWERED", placedByEmail: "caller@example.test", taskId: "task-callback", taskTitle: title }, { ai: false }),
    {
      id: "ai-long", type: "AI_INTERACTION", occurredAt: "2026-09-05T13:00:00Z", aiInteraction: {
        id: "ai-long", locationId: projectedTask().locationId, locationName: "Office", sourceCallId: "source-long", phone: projectedTask().phone,
        startedAt: "2026-09-05T13:00:00Z", status: "COMPLETED", appointmentOutcome: "INDETERMINATE", resolved: true,
        summary: "Caller asked whether the office can move the annual exam to a later week and also whether the fitting can happen at the same visit",
      },
    },
  ]
  await conversation.render(0)
  const { lines } = callLines(conversation.host)
  assert.equal(lines().length, 2)
  for (const line of lines()) {
    const summary = line.querySelector('[data-slot="call-line-summary"]')!
    const time = line.querySelector("time")!
    assert.match(time.textContent!, /^\d+:\d+\s[AP]M$/)
    assert.ok(!summary.contains(time))
    assert.equal(time.closest('[class*="line-clamp"], [class~="truncate"]'), null)
  }
  const [callback, long] = lines().map((line) => line.querySelector('[data-slot="call-line-summary"]')!.textContent!)
  assert.equal(callback, `caller@example.test called · about "${title}"`)
  assert.equal(long, "Called in · Caller asked whether the office can move the annual exam to a later week and…")
  assert.ok(long!.length - "Called in · ".length <= 81)
})

test("a missed transfer reads Missed, a voicemail offers its recording, and a callback names its Task", async (t) => {
  const conversation = conversationHarness(t)
  conversation.items = [
    callLineFixture({ id: "missed", outcome: "MISSED" }),
    callLineFixture({ id: "voicemail", outcome: "VOICEMAIL", voicemailDurationSeconds: 22 }),
    callLineFixture({
      id: "callback", direction: "OUTBOUND", outcome: "UNANSWERED", placedByEmail: "caller@example.test",
      taskId: "task-callback", taskTitle: "Reschedule post-op visit",
    }, { ai: false }),
    callLineFixture({ id: "reached", direction: "OUTBOUND", outcome: "NEEDS_DISPOSITION", durationSeconds: 192 }, { ai: false }),
    callLineFixture({ id: "elsewhere", outcome: "MISSED", locationId: "location-other", locationName: "Other office" }, { ai: false }),
  ]
  await conversation.render(0, { selectedCallID: "voicemail" })
  const { lines, outcome, button } = callLines(conversation.host)
  const read = (text: string) => {
    const line = lines().find((element) => element.textContent!.includes(text))
    assert.ok(line, text)
    return { tone: outcome(line).className.match(/text-(success|warning|destructive|muted-foreground)/)?.[1], label: outcome(line).textContent, line }
  }
  assert.equal(lines().length, 5)
  const outcomes = lines().map((line) => outcome(line).textContent).sort()
  assert.deepEqual(outcomes, ["Answered · 3m 12s", "Missed", "Missed", "No answer", "Voicemail"])
  const missed = lines().find((line) => outcome(line).textContent === "Missed")!
  assert.doesNotMatch(missed.textContent!, /Office/)
  assert.equal(read("· Other office").label, "Missed")
  assert.match(outcome(missed).className, /text-destructive/)
  const voicemail = lines().find((line) => outcome(line).textContent === "Voicemail")!
  assert.match(outcome(voicemail).className, /text-warning/)
  assert.equal(voicemail.getAttribute("aria-current"), "true")
  assert.equal(missed.getAttribute("aria-current"), null)
  const callback = read('caller@example.test called · about "Reschedule post-op visit"')
  assert.deepEqual([callback.label, callback.tone], ["No answer", "warning"])
  const reached = read("Outbound call")
  assert.deepEqual([reached.label, reached.tone], ["Answered · 3m 12s", "success"])

  assert.match(read("· Other office").line.getAttribute("aria-label")!, /^Called in, \d+:\d+\s[AP]M, Other office, Missed$/)

  const play = () => button("Play voicemail 0:22")
  const recording = () => conversation.host.querySelector<HTMLAudioElement>('audio[aria-label="Voicemail recording"]')
  assert.ok(play())
  assert.equal(play()!.textContent, "Voicemail 0:22")
  conversation.callStatus = 503
  await act(async () => play()!.click())
  await waitFor(() => /The voicemail could not be loaded/.test(conversation.host.textContent!))
  assert.equal(recording(), null)
  conversation.callStatus = 200
  await act(async () => play()!.click())
  await waitFor(() => recording() !== null)
  assert.equal(recording()!.src, "http://portal.test/v1/calling/voicemail-playback/synthetic-playback")
  assert.doesNotMatch(conversation.host.textContent!, /The voicemail could not be loaded/)
  assert.equal(play(), undefined, "the player replaces the control once it is open")
  assert.deepEqual(conversation.callRequests, [
    "/v1/calling/calls/voicemail", "/v1/calling/calls/voicemail", "/v1/calling/calls/voicemail/voicemail-playback",
  ])
  assert.deepEqual(conversation.opened, [], "playing a voicemail does not leave the conversation")

  await act(async () => voicemail.click())
  assert.match(conversation.host.textContent!, /Nobody answered, caller left a voicemail, 0:22/)
  await act(async () => button("View voicemail")!.click())
  assert.deepEqual(conversation.opened, ["call:voicemail"])
})

test("a call with several staff legs names who and when on each View call", async (t) => {
  const conversation = conversationHarness(t)
  const history = callLineFixture({ id: "answered", outcome: "RESOLVED", answeredByEmail: "maria@example.test", startedAt: "2026-09-05T12:01:00Z" })
  history.entries!.push({ id: "later", type: "CALL", occurredAt: "2026-09-05T12:09:00Z", call: {
    ...history.entries!.at(-1)!.call!, id: "later", outcome: "MISSED", answeredByEmail: "", startedAt: "2026-09-05T12:09:00Z",
  } })
  conversation.items = [history]
  await conversation.render(0)
  const { lines, outcome, timeline } = callLines(conversation.host)
  assert.equal(outcome(lines()[0]!).textContent, "maria@example.test answered")
  await act(async () => lines()[0]!.click())
  const actions = [...timeline.querySelectorAll("button")].map((element) => element.textContent!).filter((text) => text.startsWith("View call"))
  assert.equal(actions.length, 2)
  assert.match(actions[0]!, /^View call · maria@example\.test · \d+:01\s[AP]M$/)
  assert.match(actions[1]!, /^View call · \d+:09\s[AP]M$/)
  assert.match(timeline.textContent!, /:01\s[AP]M · maria@example\.test answered.*:09\s[AP]M · Nobody answered, no voicemail/)
})

test("an AI call that only ended reads Handled by AI, a stored resolved label reads Resolved by AI, and a message taken names its Task", async (t) => {
  const conversation = conversationHarness(t)
  const ai = (id: string, minute: number, aiInteraction: Partial<NonNullable<ConversationTimelineItem["aiInteraction"]>>): ConversationTimelineItem => {
    const occurredAt = `2026-09-05T12:0${minute}:00Z`
    return { id, type: "AI_INTERACTION", occurredAt, aiInteraction: {
      id, locationId: projectedTask().locationId, locationName: "Office", sourceCallId: id, phone: projectedTask().phone,
      startedAt: occurredAt, status: "COMPLETED", appointmentOutcome: "INDETERMINATE", resolved: false, summary: `Synthetic ${id}.`, ...aiInteraction,
    } }
  }
  const message = ai("message", 4, { status: "ESCALATED" })
  conversation.items = [
    ai("ended", 1, {}),
    ai("resolved", 2, { resolved: true }),
    ai("booked", 3, { appointmentOutcome: "BOOKING" }),
    { id: "history-message", type: "CALL_HISTORY", occurredAt: message.occurredAt, entries: [
      message,
      { id: "created", type: "TASK", occurredAt: message.occurredAt, taskActivity: "TASK_CREATED", task: { ...projectedTask(), origin: "ABITA_AI", title: "Synthetic refill request" } },
    ] },
  ]
  await conversation.render(0)
  const { lines, outcome, timeline } = callLines(conversation.host)
  const read = (text: string) => {
    const line = lines().find((element) => element.textContent!.includes(text))!
    return [outcome(line).textContent, outcome(line).className.match(/text-(success|warning|destructive|muted-foreground)/)?.[1]]
  }
  assert.deepEqual(read("Synthetic ended"), ["Handled by AI", "muted-foreground"])
  assert.deepEqual(read("Synthetic resolved"), ["Resolved by AI", "success"])
  assert.deepEqual(read("Synthetic booked"), ["Booked by AI", "success"])
  assert.deepEqual(read("Synthetic message"), ["Message taken", "muted-foreground"])
  await act(async () => lines().find((element) => element.textContent!.includes("Synthetic message"))!.click())
  assert.match(timeline.textContent!, /AI took a message: "Synthetic refill request"/)
  assert.doesNotMatch(timeline.textContent!, /transferred/)
})

function threadTabs(host: HTMLElement) {
  const tab = (name: string) => [...host.querySelectorAll<HTMLButtonElement>('[aria-label="Conversation view"] button')]
    .find((element) => element.textContent === name)!
  return {
    pressed: () => [...host.querySelectorAll('[aria-label="Conversation view"] button[aria-pressed="true"]')].map((element) => element.textContent),
    show: (name: string) => act(async () => tab(name).click()),
    activity: () => host.querySelector('[aria-label="Conversation activity"]')!,
    texts: () => [...host.querySelectorAll('[aria-label="Conversation activity"] [role=article]')].map((element) => element.textContent),
    lines: () => host.querySelectorAll('[aria-label="Conversation activity"] button[aria-expanded]').length,
    composer: () => host.querySelector<HTMLFormElement>('form[aria-label="Message composer"]'),
  }
}

function textFixture(id: string, occurredAt: string, overrides: Partial<Message> = {}): ConversationTimelineItem {
  return { id, type: "MESSAGE", occurredAt, message: { ...message, id, body: `Synthetic text ${id}`, attachment: undefined, createdAt: occurredAt, ...overrides } }
}

test("the thread tabs show everything, only texts or only calls and keep the composer on each", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  conversation.items = [
    textFixture("first", "2026-09-05T11:00:00Z"),
    callLineFixture({ id: "missed", outcome: "MISSED" }),
    { id: "created", type: "TASK", occurredAt: "2026-09-05T13:00:00Z", taskActivity: "TASK_CREATED", task },
  ]
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  assert.deepEqual(tabs.pressed(), ["Everything"])
  assert.deepEqual([tabs.texts().length, tabs.lines()], [1, 1])
  assert.match(tabs.activity().textContent!, /Projected follow-up/)

  await tabs.show("Texts")
  assert.deepEqual(tabs.pressed(), ["Texts"])
  assert.deepEqual([tabs.texts().length, tabs.lines()], [1, 0])
  assert.doesNotMatch(tabs.activity().textContent!, /Projected follow-up|Called in/)
  assert.ok(tabs.composer())

  await tabs.show("Calls")
  assert.deepEqual(tabs.pressed(), ["Calls"])
  assert.deepEqual([tabs.texts().length, tabs.lines()], [0, 1])
  assert.doesNotMatch(tabs.activity().textContent!, /Projected follow-up|Synthetic text/)
  assert.ok(tabs.composer())
  assert.equal(conversation.timelineRequests, 1, "tabs filter the loaded history without another request")

  await typeInto(tabs.composer()!.querySelector("textarea")!, "Synthetic reply from Calls")
  await act(async () => {
    tabs.composer()!.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  assert.deepEqual(tabs.pressed(), ["Everything"], "sending from Calls shows the sent text")
  assert.match(tabs.activity().textContent!, /Synthetic reply from Calls/)
  assert.match(
    tabs.activity().querySelector('[data-slot="message-sender"]')!.textContent!,
    /^sender@example\.test · \d+:\d+\s[AP]M$/,
    "a text names its sender as soon as it is sent",
  )

  await tabs.show("Texts")
  const other = projectedWorkspace(projectedTask()).selection.engagement!
  await conversation.render(0, { engagement: { ...other, phone: "+15551230000" } })
  assert.deepEqual(threadTabs(conversation.host).pressed(), ["Everything"], "another conversation starts on Everything")
})

test("an outbound text names the staff member who sent it and an automated text stays Automated", async (t) => {
  const conversation = conversationHarness(t)
  const outbound = { direction: "OUTBOUND", delivery: "Delivered" } as const
  conversation.items = [
    textFixture("inbound", "2026-09-05T11:00:00Z"),
    textFixture("staff", "2026-09-05T11:01:00Z", { ...outbound, createdBy: { kind: "HUMAN", subject: "staff", email: "staff@example.test" } }),
    textFixture("automated", "2026-09-05T11:02:00Z", { ...outbound, createdBy: { kind: "SERVICE", subject: "text-automation" } }),
    textFixture("unresolved", "2026-09-05T11:03:00Z", { ...outbound, createdBy: { kind: "HUMAN", subject: "former-staff" } }),
  ]
  await conversation.render(0)
  const text = (id: string) => {
    const article = [...conversation.host.querySelectorAll('[aria-label="Conversation activity"] [role=article]')]
      .find((element) => element.textContent!.includes(`Synthetic text ${id}`))
    assert.ok(article, id)
    const sender = article.querySelector('[data-slot="message-sender"]')
    const footer = article.querySelector('[data-slot="message-footer"]')!
    assert.equal(sender?.closest('[data-slot="message-footer"]') ?? null, null, "the sender is outside the footer that hides until hover")
    assert.doesNotMatch(sender?.className ?? "", /opacity-0/)
    return { sender: sender?.textContent ?? "", footer: footer.textContent!, hidden: /opacity-0/.test(footer.className) }
  }
  assert.match(text("staff").sender, /^staff@example\.test · \d+:01\s[AP]M$/)
  assert.deepEqual([text("staff").footer, text("staff").hidden], ["Delivered", true])
  assert.match(text("automated").sender, /^Automated · \d+:02\s[AP]M$/)
  assert.equal(text("automated").footer, "Delivered")
  assert.equal(text("unresolved").sender, "")
  assert.match(text("unresolved").footer, /^\d+:03\s[AP]M·Delivered$/)
  assert.equal(text("inbound").sender, "")
  assert.match(text("inbound").footer, /^\d+:00\s[AP]M$/)
})

test("a tab with nothing loaded keeps loading earlier activity until something matches or the history ends", async (t) => {
  const conversation = conversationHarness(t)
  const pages: ConversationTimelinePage[] = [
    { items: [callLineFixture({ id: "newest", outcome: "MISSED" })], nextCursor: "one" },
    { items: [callLineFixture({ id: "older", outcome: "MISSED" })], nextCursor: "two" },
    { items: [textFixture("oldest", "2026-09-01T11:00:00Z")], nextCursor: "" },
  ]
  conversation.timelinePage = (cursor) => pages[["", "one", "two"].indexOf(cursor)]!
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  assert.equal(conversation.timelineRequests, 1)
  await tabs.show("Texts")
  await waitFor(() => tabs.texts().length === 1)
  assert.equal(conversation.timelineRequests, 3)
  assert.match(tabs.texts()[0]!, /Synthetic text oldest/)
  assert.doesNotMatch(tabs.activity().textContent!, /No texts yet/)

  await tabs.show("Calls")
  assert.equal(tabs.lines(), 2)
  assert.equal(conversation.timelineRequests, 3)
})

test("a tab searches at most three earlier pages by itself, then one page per request", async (t) => {
  const conversation = conversationHarness(t)
  const cursors = ["", "one", "two", "three", "four", "five"]
  conversation.timelinePage = (cursor) => {
    const index = cursors.indexOf(cursor)
    return index === cursors.length - 1
      ? { items: [textFixture("oldest", "2026-09-01T11:00:00Z")], nextCursor: "" }
      : { items: [callLineFixture({ id: `call-${index}`, outcome: "MISSED" })], nextCursor: cursors[index + 1]! }
  }
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  const earlier = () => [...conversation.host.querySelectorAll("button")].find((element) => element.textContent?.trim() === "Earlier activity")
  const settle = () => act(async () => new Promise((resolve) => setTimeout(resolve, 5)))
  await tabs.show("Texts")
  await waitFor(() => conversation.timelineRequests === 4 && earlier()?.disabled === false)
  await settle()
  assert.equal(conversation.timelineRequests, 4, "the search stops after three earlier pages")
  assert.match(tabs.activity().textContent!, /No texts yet/)
  assert.match(tabs.activity().textContent!, /Earlier activity is not loaded/)

  await act(async () => earlier()!.click())
  await waitFor(() => conversation.timelineRequests === 5 && earlier()?.disabled === false)
  await settle()
  assert.equal(conversation.timelineRequests, 5, "one request loads one earlier page")
  await act(async () => earlier()!.click())
  await waitFor(() => tabs.texts().length === 1)
  assert.equal(conversation.timelineRequests, 6)
})

test("leaving Everything and coming back returns the reader to where they were", async (t) => {
  const conversation = conversationHarness(t)
  conversation.items = [textFixture("first", "2026-09-05T11:00:00Z"), callLineFixture({ id: "missed", outcome: "MISSED" })]
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  const viewport = conversation.host.querySelector<HTMLElement>('[data-testid="message-timeline"]')!
  const scrolls: number[] = []
  const place = { scrollHeight: 1000, scrollTop: 300 }
  Object.defineProperties(viewport, {
    scrollHeight: { configurable: true, get: () => place.scrollHeight },
    clientHeight: { configurable: true, value: 200 },
    scrollTop: { configurable: true, get: () => place.scrollTop },
    scrollTo: { configurable: true, value: ({ top }: ScrollToOptions) => { place.scrollTop = top!; scrolls.push(top!) } },
  })
  const layout: Record<string, number> = { "MESSAGE:first": 250, "CALL_HISTORY:history-missed": 420 }
  window.HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
    if (this === viewport) return { top: 100, bottom: 300 } as DOMRect
    const top = 100 + (layout[this.dataset.messageId ?? ""] ?? 0) - place.scrollTop
    return { top, bottom: top + 170 } as DOMRect
  }
  const settle = () => act(async () => new Promise((resolve) => setTimeout(resolve, 5)))
  await tabs.show("Calls")
  await settle()
  assert.equal(scrolls.at(-1), 800, "Calls opens on its latest call")
  await tabs.show("Texts")
  await settle()
  assert.equal(scrolls.at(-1), 800)
  layout["MESSAGE:first"] = 600
  place.scrollHeight = 1400
  await tabs.show("Everything")
  await settle()
  assert.equal(scrolls.at(-1), 650, "Everything puts the item the reader was on back where it sat, 50 above the top edge, whatever the heights around it became")
  const before = scrolls.length
  await tabs.show("Everything")
  await settle()
  assert.equal(scrolls.length, before, "choosing the tab already shown does not move the reader")

  place.scrollTop = 1190
  await tabs.show("Calls")
  await settle()
  place.scrollHeight = 2000
  await tabs.show("Everything")
  await settle()
  assert.equal(scrolls.at(-1), 1800, "a reader who left Everything at the latest activity returns to the latest activity")
})

test("a failed earlier page shows the error with a retry instead of an empty tab, and a tab says so when the whole history holds nothing for it", async (t) => {
  const conversation = conversationHarness(t)
  conversation.timelinePage = (cursor) => cursor
    ? { items: [textFixture("older", "2026-09-01T11:00:00Z")], nextCursor: "" }
    : { items: [textFixture("newest", "2026-09-05T11:00:00Z")], nextCursor: "one" }
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  const earlier = () => [...conversation.host.querySelectorAll("button")].find((element) => element.textContent?.trim() === "Earlier activity")
  conversation.timelineStatus = 503
  await tabs.show("Calls")
  await waitFor(() => conversation.timelineRequests === 2 && earlier()?.disabled === false)
  await act(async () => new Promise((resolve) => setTimeout(resolve, 5)))
  assert.equal(conversation.timelineRequests, 2, "a failed earlier page is not requested again without the user asking")
  const alert = () => conversation.host.querySelector('[role="alert"]')
  assert.equal(alert()!.textContent, "Earlier activity could not be loadedTry again")
  assert.doesNotMatch(alert()!.textContent!, /Conversation unavailable/, "the conversation itself is on screen")
  assert.doesNotMatch(tabs.activity().textContent!, /No calls yet/)

  conversation.timelineStatus = 200
  await act(async () => [...alert()!.querySelectorAll("button")].find((element) => element.textContent?.trim() === "Try again")!.click())
  await waitFor(() => conversation.timelineRequests === 3 && !earlier())
  assert.equal(alert(), null)
  assert.match(tabs.activity().textContent!, /No calls yet/)
  assert.doesNotMatch(tabs.activity().textContent!, /Earlier activity is not loaded/)
  assert.ok(tabs.composer())
})

test("a manual earlier load that adds nothing for the tab says so in place", async (t) => {
  const conversation = conversationHarness(t)
  const cursors = ["", "one", "two", "three", "four"]
  conversation.timelinePage = (cursor) => {
    const index = cursors.indexOf(cursor)
    const items = index === 0
      ? [callLineFixture({ id: "newest", outcome: "MISSED" }), textFixture("newest", "2026-09-05T11:00:00Z")]
      : index === 4
        ? [callLineFixture({ id: "oldest", outcome: "MISSED" })]
        : [textFixture(`older-${index}-a`, `2026-09-0${4 - index}T11:00:00Z`), textFixture(`older-${index}-b`, `2026-09-0${4 - index}T10:00:00Z`)]
    return { items, nextCursor: cursors[index + 1] ?? "" }
  }
  await conversation.render(0)
  const tabs = threadTabs(conversation.host)
  const earlier = () => [...conversation.host.querySelectorAll("button")].find((element) => element.textContent?.trim() === "Earlier activity")
  const note = () => tabs.activity().querySelector('[role="status"]')?.textContent ?? ""
  const press = async (requests: number) => {
    await act(async () => earlier()!.click())
    await waitFor(() => conversation.timelineRequests === requests && earlier()?.disabled !== true)
    await act(async () => new Promise((resolve) => setTimeout(resolve, 5)))
  }
  await press(2)
  assert.equal(note(), "", "Everything shows what was loaded and needs no note")

  await tabs.show("Calls")
  assert.equal(tabs.lines(), 1)
  assert.equal(note(), "")
  await press(3)
  assert.equal(tabs.lines(), 1)
  assert.equal(note(), "No calls in the next 2 items. Load more to keep looking.")
  await press(4)
  assert.equal(note(), "No calls in the next 4 items. Load more to keep looking.", "each press that finds nothing changes what the reader sees")

  await tabs.show("Texts")
  assert.equal(note(), "", "the note belongs to the tab that was searched")
  await tabs.show("Calls")
  await press(5)
  assert.equal(tabs.lines(), 2)
  assert.equal(note(), "", "a load that found a call clears the note")
  assert.equal(earlier(), undefined)
})

test("an expanded line's Task follows the patient's Tasks while new activity stays held", async (t) => {
  const conversation = conversationHarness(t)
  const task: Task = { ...projectedTask(), id: "line-task", title: "Send the parking map", version: 1 }
  conversation.items = [callLineFixture({ id: "parking", outcome: "RESOLVED", answeredByEmail: "dana@example.test" }, { task })]
  await conversation.render(0)
  const { timeline, lines } = callLines(conversation.host)
  const viewport = conversation.host.querySelector<HTMLElement>('[data-testid="message-timeline"]')!
  await act(async () => lines()[0]!.click())
  assert.match(timeline.textContent!, /Send the parking map · Open/)
  assert.equal(conversation.summaryRequests, 0, "at the latest activity the thread itself is live")

  await act(async () => {
    Object.defineProperties(viewport, {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 200 },
      scrollTop: { configurable: true, value: 0 },
    })
    viewport.dispatchEvent(new window.Event("scroll"))
  })
  await waitFor(() => conversation.summaryRequests === 1)
  assert.match(timeline.textContent!, /Send the parking map · Open/)

  const closed: Task = {
    ...task, state: "COMPLETED", closeReason: "DONE", version: 2, completedAt: "2026-09-06T12:00:00Z",
    completedBy: { kind: "HUMAN", subject: "staff", email: "staff@example.test" },
  }
  conversation.summary = { ...conversation.summary, openTasks: [], openTaskCount: 0, closedToday: [closed] }
  conversation.items = [
    textFixture("arrived-while-reading", "2026-09-06T12:01:00Z"),
    callLineFixture({ id: "parking", outcome: "RESOLVED", answeredByEmail: "dana@example.test" }, { task: closed }),
  ]
  const requests = conversation.timelineRequests
  await conversation.render(1)
  await waitFor(() => /Send the parking map · Done, by staff@example\.test/.test(timeline.textContent!))
  assert.doesNotMatch(timeline.textContent!, /Send the parking map · Open/)
  assert.doesNotMatch(timeline.textContent!, /Synthetic text arrived-while-reading/, "new activity stays behind the control")
  assert.equal(conversation.timelineRequests, requests, "the held thread is not reloaded")
  assert.ok([...conversation.host.querySelectorAll("button")].some((element) => element.textContent?.trim() === "New activity"))

  conversation.summary = { ...conversation.summary, openTasks: [{ ...task, version: 3 }], openTaskCount: 1, closedToday: [] }
  await conversation.render(2)
  await waitFor(() => /Send the parking map · Open/.test(timeline.textContent!))
})

test("the thread names the callback caller and marks which Task each entry describes", async (t) => {
  const conversation = conversationHarness(t)
  const { selected, review, history } = callbackFixture()
  conversation.items = history
  const histories: NumberHistory[] = []
  await conversation.render(0, { selectedTaskID: selected.id, onHistoryChange: (value) => histories.push(value) })
  const timeline = conversation.host.querySelector('[aria-label="Conversation activity"]')!
  const text = timeline.textContent ?? ""
  assert.match(text, /caller@example\.test called\d[^A-Za-z]*[AP]MNo answer/)
  assert.match(text, /Missed call review completed after caller@example\.test's callback attempt/)
  assert.match(text, /This Task created by AI/)
  assert.doesNotMatch(text, /Task completed after callback attempt/)
  assert.ok(timeline.querySelector(`button[aria-label="View task: ${selected.title}"]`))
  assert.ok(timeline.querySelector(`button[aria-label="View task: ${review.title}"]`))
  assert.equal(histories.at(-1)?.phone, selected.phone)
  assert.equal(histories.at(-1)?.items.length, history.length)
})

test("the Task panel credits the callback caller from the Task and flags an open Task only with loaded evidence", () => {
  const { selected, review, history } = callbackFixture()
  const panel = (task: Task, numberHistory?: ConversationTimelineItem[]) => renderToStaticMarkup(
    <DataProvider>
      <TaskCallContext
        task={task}
        activeCall={undefined}
        callControlsShowing={false}
        view="task"
        numberHistory={numberHistory}
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        onOpenTask={() => {}}
        patientTasks={() => null}
      />
    </DataProvider>,
  )
  const open = panel(selected, history)
  assert.match(open, /caller@example\.test called this number \(no answer\) · .+\. This Task is still open\./)
  assert.ok(open.indexOf("This Task is still open.") < open.indexOf("Complete &amp; next"))
  assert.doesNotMatch(panel(selected), /still open/)
  assert.doesNotMatch(panel(selected, history.filter((item) => item.type !== "CALL_HISTORY")), /still open/)
  const callbackReview = { ...review, callbackCompletion: { callerEmail: "caller@example.test" } }
  assert.match(panel(callbackReview), /Missed call review completed after caller@example\.test&#x27;s callback attempt/)
  assert.doesNotMatch(panel(callbackReview), /Done, automatically/)
  assert.match(panel(review), /Done, automatically/)
})

test("an AI Task shows the missed handoff call attached to it", () => {
  const task: Task = {
    ...projectedTask(),
    origin: "ABITA_AI",
    relatedInteractionCount: 1,
    interactions: [{ callId: "call-1", type: "VOICEMAIL", occurredAt: "2026-08-30T12:01:00Z" }],
  }
  const panel = (value: Task) => renderToStaticMarkup(
    <DataProvider>
      <TaskCallContext
        task={value}
        activeCall={undefined}
        callControlsShowing={false}
        view="task"
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        onOpenTask={() => {}}
        patientTasks={() => null}
      />
    </DataProvider>,
  )
  assert.match(panel(task), /aria-label="Call recovery source"/)
  assert.doesNotMatch(panel({ ...task, relatedInteractionCount: 0, interactions: [] }), /Call recovery source/)
  assert.doesNotMatch(
    panel({ ...task, origin: "HUMAN_CALL_FOLLOW_UP", callId: "call-1" }),
    /Call recovery source/,
    "a follow-up's own call is not an attached missed call",
  )
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
    <DataProvider>
      <TaskCallContext
        task={task}
        nextTask={next}
        onSelectTask={(row) => selected.push(row.id)}
        activeCall={undefined}
        callControlsShowing={false}
        view="task"
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        onOpenTask={() => {}}
        patientTasks={() => null}
      />
    </DataProvider>,
  ))
  const actions = () => [...host.querySelectorAll<HTMLButtonElement>("section[aria-label='Focused Task'] div.flex-col button")]
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

test("the focused Task shows each projected version and completes only the version the operator reviewed", async (t) => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const completions: unknown[] = []
  const reviewed: Task = { ...projectedTask(), origin: "APPOINTMENT_REVIEW", title: "Synthetic booking v1" }
  const changed: Task = { ...reviewed, title: "Synthetic booking v2", version: 2 }
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
    if (url === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    assert.match(url, /\/v1\/tasks\/[^/]+\/complete$/)
    const body = await (input as Request).json()
    completions.push(body)
    return Response.json({ ...changed, state: "COMPLETED", version: 3 })
  })
  const render = (task: Task) => act(async () => root.render(
    <DataProvider>
      <TaskCallContext
        task={task}
        activeCall={undefined}
        callControlsShowing={false}
        view="task"
        taskCallError=""
        onTaskUpdated={() => {}}
        onReturnToCall={() => {}}
        onOpenTask={() => {}}
        patientTasks={() => null}
      />
    </DataProvider>,
  ))
  const button = (name: string) => [...host.querySelectorAll<HTMLButtonElement>("button")].find((candidate) => candidate.textContent?.trim() === name)
  try {
    await render(reviewed)
    assert.equal(button("Verify & next")?.disabled, false)
    await render(changed)
    assert.equal(host.querySelector("h2")?.textContent, changed.title, "the panel shows the projected Task, not a held copy")
    assert.equal(button("Verify & next")?.disabled, true, "a version the operator has not reviewed cannot be completed")
    await act(async () => button("Review latest")!.click())
    assert.equal(button("Verify & next")?.disabled, false)
    await act(async () => button("Verify & next")!.click())
    assert.deepEqual(completions, [{ expectedVersion: changed.version, reason: "DONE" }], "completion submits the projected version as done")
  } finally {
    await act(async () => root.unmount())
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
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
    closeReason: "DONE",
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
  await conversation.render(0)
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
  const activity = () => conversation.host.querySelector('[aria-label="Conversation activity"]')?.textContent ?? ""
  assert.match(activity(), /Synthetic reply/)
  assert.equal(conversation.timelineRequests, 1)

  await typeInto(textarea, "Second reply")
  await submit()
  assert.equal(conversation.sends.length, 3)
  assert.match(activity(), /Synthetic reply/, "a second send keeps the first sent Message on screen")
  assert.match(activity(), /Second reply/)

  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 800)) })
  assert.equal(conversation.timelineRequests, 2, "the Sending Message reloads once its hold ends")
})

test("Enter sends a text and Shift+Enter keeps writing, and touch keyboards keep Enter for new lines", async (t) => {
  const conversation = conversationHarness(t)
  let coarsePointer = true
  Object.defineProperty(window, "matchMedia", { configurable: true, value: () => ({ matches: coarsePointer }) })
  await conversation.render(0)
  const textarea = conversation.host.querySelector<HTMLTextAreaElement>('form[aria-label="Message composer"] textarea')!
  await typeInto(textarea, "Synthetic reply")
  const press = (shiftKey: boolean) => act(async () => {
    textarea.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Enter", shiftKey, bubbles: true, cancelable: true }))
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  await press(false)
  assert.equal(conversation.sends.length, 0, "a touch keyboard Enter adds a line")
  coarsePointer = false
  await press(true)
  assert.equal(conversation.sends.length, 0)
  await press(false)
  assert.equal(conversation.sends.length, 1)
  assert.equal(conversation.sends[0]!.body, "Synthetic reply")
})

test("a live update while scrolled up keeps every loaded item and offers New activity", async (t) => {
  const conversation = conversationHarness(t)
  const task = projectedTask()
  const entry = (id: string, day: number): ConversationTimelineItem => ({
    type: "TASK", id, occurredAt: `2026-09-${String(day).padStart(2, "0")}T12:00:00Z`, taskActivity: "TASK_CREATED", task: { ...task, id, title: `Synthetic ${id}` },
  })
  let history = ["f", "e", "d", "c", "b", "a", "z", "y"].map((id, index) => entry(id, 16 - index))
  conversation.timelinePage = (cursor) => {
    const start = cursor ? history.findIndex((item) => item.id === cursor) + 1 : 0
    const items = history.slice(start, start + 2)
    return { items, nextCursor: start + 2 < history.length ? items.at(-1)!.id : "" }
  }
  const activity = () => conversation.host.querySelector('[aria-label="Conversation activity"]')?.textContent ?? ""
  const button = (name: string) => Array.from(conversation.host.querySelectorAll("button")).find((element) => element.textContent?.trim() === name)
  const viewport = () => conversation.host.querySelector<HTMLElement>('[data-testid="message-timeline"]')!
  const scrollTo = (scrollTop: number) => act(async () => {
    Object.defineProperties(viewport(), {
      scrollHeight: { configurable: true, value: 1000 },
      clientHeight: { configurable: true, value: 200 },
      scrollTop: { configurable: true, value: scrollTop },
    })
    viewport().dispatchEvent(new window.Event("scroll"))
  })
  let release!: () => void
  const gate = () => { conversation.timelineGate = new Promise<void>((resolve) => { release = resolve }) }
  const settle = () => act(async () => new Promise((resolve) => setTimeout(resolve, 0)))

  await conversation.render(0)
  await waitFor(() => /Synthetic e/.test(activity()))
  await act(async () => button("Earlier activity")!.click())
  await waitFor(() => /Synthetic c/.test(activity()))

  await scrollTo(0)
  history = [entry("g", 17), ...history]
  await conversation.render(1)
  await waitFor(() => Boolean(button("New activity")))
  for (const id of ["c", "d", "e", "f"]) assert.match(activity(), new RegExp(`Synthetic ${id}`), `loaded item ${id} stays while scrolled up`)
  assert.doesNotMatch(activity(), /Synthetic g/)

  await act(async () => button("Earlier activity")!.click())
  await waitFor(() => /Synthetic a/.test(activity()))
  assert.ok(button("New activity"), "loading earlier activity keeps the pending update")
  assert.doesNotMatch(activity(), /Synthetic g/)

  await scrollTo(800)
  await waitFor(() => /Synthetic g/.test(activity()))
  assert.equal(button("New activity"), undefined, "returning to the latest activity applies the update")

  gate()
  history = [entry("h", 18), ...history]
  await conversation.render(2)
  await scrollTo(0)
  await waitFor(() => Boolean(button("New activity")))
  await act(async () => release())
  await settle()
  assert.doesNotMatch(activity(), /Synthetic h/, "a refresh in flight when the reader scrolls up does not land")
  assert.ok(button("New activity"))

  conversation.timelineGate = undefined
  await act(async () => button("New activity")!.click())
  await waitFor(() => /Synthetic h/.test(activity()))
  await scrollTo(0)
  gate()
  await act(async () => button("Earlier activity")!.click())
  history = [entry("i", 19), ...history]
  conversation.textTask = { ...task, version: 2 }
  await conversation.render(3)
  await act(async () => release())
  await waitFor(() => /Synthetic a/.test(activity()))
  assert.ok(button("New activity"), "a live update during loading earlier activity stays pending")
  assert.doesNotMatch(activity(), /Synthetic i/)

  conversation.timelineGate = undefined
  conversation.timelineStatus = 503
  const requests = conversation.timelineRequests
  await act(async () => button("Earlier activity")!.click())
  await waitFor(() => conversation.timelineRequests > requests && button("Earlier activity")?.disabled === false)
  assert.ok(button("New activity"), "a failed earlier page keeps the pending update")
  assert.doesNotMatch(activity(), /Synthetic z/)

  conversation.timelineStatus = 200
  await act(async () => button("New activity")!.click())
  await waitFor(() => /Synthetic i/.test(activity()))
  assert.equal(button("New activity"), undefined)
})

test("a sent Message does not reload its conversation after navigation", async (t) => {
  const conversation = conversationHarness(t)
  await conversation.render(0)
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

function LiveDetail({ revision, children }: { revision: number; children: ReactNode }) {
  useLiveRefresh({ workspaceVersion: 0, detailRevision: revision })
  return children
}

function conversationHarness(t: TestContext) {
  const dom = installDOM()
  dom.window.HTMLMediaElement.prototype.play = async () => {}
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
    summaryRequests: 0,
    summary: { phone: "", displayName: "", names: [], openTasks: [], openTaskCount: 0, closedToday: [] } as PhoneSummary,
    callStatus: 200,
    callRequests: [] as string[],
    tokenStatus: 200,
    textTask: undefined as Task | undefined,
    timelineGate: undefined as Promise<void> | undefined,
    completions: [] as unknown[],
    sends: [] as { idempotencyKey: string; destination?: string; body: string }[],
    sendStatus: 201,
    items: [] as ConversationTimelineItem[],
    timelinePage: ((): ConversationTimelinePage => ({ items: conversation.items, nextCursor: "" })) as (cursor: string) => ConversationTimelinePage,
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
    if (url.includes("/v1/calling/calls/")) {
      const path = new URL(url).pathname
      conversation.callRequests.push(path)
      if (path.endsWith("/voicemail-playback")) return Response.json({ token: "synthetic-playback", expiresAt: "2026-09-05T13:00:00Z" })
      return conversation.callStatus === 200
        ? Response.json({ id: path.split("/").at(-1), voicemail: { audioState: "READY", durationSeconds: 22, outcome: "VOICEMAIL" } })
        : Response.json({ code: "UNAVAILABLE" }, { status: conversation.callStatus })
    }
    if (url.endsWith("/v1/messages")) {
      const body = await (input as Request).json()
      conversation.sends.push(body)
      return conversation.sendStatus === 201
        ? Response.json({ status: "created", message: { ...message, id: `sent-${conversation.sends.length}`, direction: "OUTBOUND", body: body.body, delivery: "Sending", attachment: undefined, createdBy: { kind: "HUMAN", subject: "sender", email: "sender@example.test" } } }, { status: 201 })
        : Response.json({ error: { code: "DESTINATION_UNAVAILABLE", message: "Synthetic conflict", correlationId: "correlation-1", retryable: false } }, { status: conversation.sendStatus })
    }
    if (url.includes("/v1/tasks/") && url.endsWith("/complete")) {
      const body = await (input as Request).json()
      conversation.completions.push(body)
      return Response.json({ ...conversation.textTask, state: "COMPLETED" })
    }
    if (/\/v1\/engagements\/%2B1555123\d{4}\/summary\?/.test(url)) {
      conversation.summaryRequests += 1
      return Response.json(conversation.summary)
    }
    assert.match(url, /\/v1\/engagements\/%2B1555123\d{4}\/timeline\?/)
    conversation.timelineRequests += 1
    await conversation.timelineGate
    return conversation.timelineStatus === 200
      ? Response.json(conversation.timelinePage(new URL(url).searchParams.get("cursor") ?? ""))
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
        <DataProvider>
        <LiveDetail revision={revision}>
        <EngagementWorkspaceView
          practiceName="Synthetic Practice"
          engagement={projection.selection.engagement!}
          practiceID={projection.scope.practiceID}
          textTask={conversation.textTask}
          patientTasksOpen={false}
          onPatientTasks={() => {}}
          onTextTaskUpdated={() => {}}
          onTaskCreated={() => {}}
          onTaskOpen={(task) => conversation.opened.push(`task:${task.id}`)}
          onCallOpen={(id) => conversation.opened.push(`call:${id}`)}
          onAIInteractionOpen={(id) => conversation.opened.push(`ai:${id}`)}
          calling={{
            callingOccupied: false,
            outboundPending: false,
            ownsSoftphone: false,
            startOutbound: async () => undefined,
          }}
          {...overrides}
        />
        </LiveDetail>
        </DataProvider>,
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
  await waitFor(() => !done().disabled)

  conversation.timelineGate = new Promise<void>((resolve) => { release = resolve })
  conversation.textTask = { ...task, version: 3 }
  await conversation.render(1)
  assert.equal(done().disabled, true, "new Task version is not proof the new messages loaded")
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [])
  conversation.timelineStatus = 503
  await act(async () => { release(); await conversation.timelineGate })
  await waitFor(() => /The conversation could not be loaded\./.test(conversation.host.textContent ?? ""))
  assert.equal(done().disabled, true, "failed refresh must leave unseen work pending")

  conversation.timelineStatus = 200
  conversation.timelineGate = undefined
  await conversation.render(2)
  await waitFor(() => !done().disabled)
  await act(async () => done().click())
  assert.deepEqual(conversation.completions, [{ expectedVersion: 3, reason: "DONE" }], "completion submits the version whose conversation loaded as done")
})

function completionRequests(t: TestContext, respond: (url: string) => unknown) {
  const previousAPIURL = process.env.NEXT_PUBLIC_PORTAL_API_URL
  process.env.NEXT_PUBLIC_PORTAL_API_URL = "http://portal.test"
  clearAccessToken()
  const sent: { path: string; body: unknown }[] = []
  t.mock.method(globalThis, "fetch", async (input: RequestInfo | URL) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url
    if (url === "/api/auth/token") return Response.json({ token: "synthetic-token" })
    const path = new URL(url).pathname
    if (/\/complete(-group)?$/.test(path)) sent.push({ path, body: await (input as Request).json() })
    return Response.json(respond(path))
  })
  t.after(() => {
    clearAccessToken()
    if (previousAPIURL === undefined) delete process.env.NEXT_PUBLIC_PORTAL_API_URL
    else process.env.NEXT_PUBLIC_PORTAL_API_URL = previousAPIURL
  })
  return sent
}

test("a completion request carries the reason it was given", async (t) => {
  const task = projectedTask()
  const sent = completionRequests(t, () => ({ ...task, state: "COMPLETED", version: 2 }))
  const authority = createWorkspaceAuthorityAdapter()
  for (const reason of ["DONE", "NOT_NEEDED", "UNREACHABLE"] as const) {
    await authority.completeTask("synthetic-token", task, reason, new AbortController().signal)
  }
  assert.deepEqual(sent.map((request) => request.body), [
    { expectedVersion: task.version, reason: "DONE" },
    { expectedVersion: task.version, reason: "NOT_NEEDED" },
    { expectedVersion: task.version, reason: "UNREACHABLE" },
  ])
})

test("a voicemail played to the end completes its Task as done", async (t) => {
  const dom = installDOM()
  const host = document.createElement("div")
  document.body.append(host)
  const root = createRoot(host)
  const task: Task = {
    ...projectedTask(), origin: "VOICEMAIL_RECOVERY", recoveryOutcome: "VOICEMAIL", callId: "call-1",
  }
  const sent = completionRequests(t, (path) => path.endsWith("/voicemail-playback")
    ? { token: "synthetic-playback", expiresAt: "2026-08-30T13:00:00Z" }
    : path.endsWith("/complete") ? { ...task, state: "COMPLETED", version: 2 } : task)
  const call = {
    id: "call-1",
    voicemail: { audioState: "READY", durationSeconds: 12, outcome: "VOICEMAIL" },
  } as ComponentProps<typeof VoicemailSource>["call"]
  try {
    await act(async () => root.render(
      <DataProvider><VoicemailSource call={call} taskID={task.id} /></DataProvider>,
    ))
    dom.window.HTMLMediaElement.prototype.play = async () => {}
    await act(async () => [...host.querySelectorAll("button")].find((candidate) => candidate.textContent === "Play")!.click())
    await waitFor(() => host.querySelector("audio") !== null)
    await act(async () => { host.querySelector("audio")!.dispatchEvent(new dom.window.Event("ended")) })
    await waitFor(() => sent.length === 1)
    assert.deepEqual(sent, [{ path: "/v1/tasks/task-1/complete", body: { expectedVersion: task.version, reason: "DONE" } }])
  } finally {
    await act(async () => root.unmount())
    dom.window.close()
  }
})

async function waitFor(ready: () => boolean) {
  for (let attempt = 0; attempt < 50 && !ready(); attempt++) {
    await act(async () => new Promise((resolve) => setTimeout(resolve, 0)))
  }
  assert.ok(ready(), "condition was not reached")
}

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
