import assert from "node:assert/strict"
import test from "node:test"

import { createWorkspaceSync } from "./workspace-sync/workspace-sync.ts"
import type { PortalFailure } from "./clients/portal-request.ts"
import type { WorkspaceLocation } from "./workspace-location.ts"

import type {
  AccessDiscovery,
  AiInteractionDetail,
  CallingCall,
  Location,
  PatientWorkPage,
  PatientWorkQueryRequest,
  PatientWorkRow,
  Practice,
  Task,
  TaskPage,
  WorkspaceSnapshot,
} from "./api/generated/types.gen.ts"
import {
  createWorkspaceProjection as createProjection,
  type WorkspaceAuthorityAdapter,
  type WorkspaceAuthorityResult,
  type WorkspaceRealtimeAdapter,
  type WorkspaceRealtimeCallbacks,
  WorkspaceProjectionAccessError,
  type WorkspaceProjectionState,
} from "./workspace-projection.ts"

type TestAuthority = Omit<WorkspaceAuthorityAdapter, "patients"> &
  Partial<Pick<WorkspaceAuthorityAdapter, "patients">>

function createWorkspaceProjection(
  options: Omit<Parameters<typeof createProjection>[0], "authority"> & { authority: TestAuthority },
) {
  const { authority } = options
  return createProjection({
    ...options,
    authority: {
      ...authority,
      patients: authority.patients ?? (async (token, request, signal) => {
        const result = await authority.tasks(token, {
          practiceId: request.practiceId,
          ...(request.locationId ? { locationId: request.locationId } : {}),
          ...(request.search ? { search: request.search } : {}),
          ...(request.cursor ? { cursor: request.cursor } : {}),
          state: "OPEN",
          responsibility: request.scope === "everyone" ? "all" : "mine",
          limit: request.limit,
        }, signal)
        return result.kind === "success"
          ? success(patientPage(result.data.items.map((item) => patientRow(item)), result.data.nextCursor))
          : result
      }),
    },
  })
}

function openTasks(state: WorkspaceProjectionState) {
  return state.patients.items.map((row) => row.top)
}

test("initial workspace shows a conversation without opening Task details and returning to work restores it", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first]),
    }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.view, "engagement")
  assert.equal(projection.getSnapshot().selection.engagement?.phone, first.phone)
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, false)
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, false)
  await projection.dispatch({ type: "select-manage-agent" })
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  await projection.dispatch({ type: "select-work" })
  assert.equal(projection.getSnapshot().selection.view, "engagement")
  assert.equal(projection.getSnapshot().selection.engagement?.phone, first.phone)
  await projection.dispatch({ type: "select-task", task: first })
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, true)
  projection.stop()
})

test("scope changes clear every window and obsolete delayed responses from the old scope", async () => {
  const preferences = new Map<string, string>()
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(4),
      tasks: taskPage([task("old-scope-task")]),
    }),
    realtime: realtime.adapter,
    preferences: {
      read: (key) => preferences.get(key) ?? null,
      write: (key, value) => preferences.set(key, value),
    },
  })

  await projection.start()
  const delayedOldScope = await realtime.prepareReconciliation(0)

  await projection.dispatch({
    type: "select-scope",
    practiceID: "practice-1",
    locationScopeID: "location-2",
  })
  delayedOldScope.apply()

  const state = projection.getSnapshot()
  assert.deepEqual(realtime.scope, {
    practiceID: "practice-1",
    locationID: "location-2",
  })
  assert.equal(state.loadState, "loading")
  assert.deepEqual(state.scope, {
    practiceID: "practice-1",
    locationID: "location-2",
    locationScopeID: "location-2",
  })
  assert.equal(state.workspace, undefined)
  assert.deepEqual(openTasks(state), [])
  assert.deepEqual(state.completedTasks.items, [])
  assert.equal(state.selection.task, undefined)
  assert.equal(
    preferences.get("acuity.taskLocationScope.practice-1"),
    "location-2",
  )
  projection.stop()
})

test("pagination appends uniquely and refresh retains active and completed window depth", async () => {
  const realtime = deterministicRealtime()
  const authority: TestAuthority = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(7), tasks: taskPage([]) }),
    tasks: async (_token, request) => {
      const completed = request.state === "COMPLETED"
      const prefix = completed ? "done" : "open"
      const first = task(`${prefix}-1`, completed ? { state: "COMPLETED" } : {})
      const second = task(`${prefix}-2`, completed ? { state: "COMPLETED" } : {})
      return success(request.cursor
        ? taskPage([first, second])
        : { ...taskPage([first]), nextCursor: `${prefix}-more` })
    },
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "load-more", window: "patients" })
  await projection.dispatch({ type: "load-more", window: "completedTasks" })
  for (let refresh = 0; refresh < 2; refresh += 1) {
    assert.deepEqual(openTasks(projection.getSnapshot()).map((item) => item.id), ["open-1", "open-2"])
    assert.deepEqual(projection.getSnapshot().completedTasks.items.map((item) => item.id), ["done-1", "done-2"])
    await realtime.reconcile(0)
  }
  projection.stop()
})

test("a reconcile during load-more keeps the page the operator asked for", async () => {
  const { projection, realtime, control, delayed, openIDs } = await startWithDelayableTasks()
  control.delayNext = true
  const load = projection.dispatch({ type: "load-more", window: "patients" })
  await waitUntil(() => !control.delayNext)
  await realtime.reconcile(0)
  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  delayed.resolve(success(taskPage([task("superseded-page")])))
  await load

  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  assert.equal(projection.getSnapshot().patients.loading, false)
  await realtime.reconcile(0)
  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  projection.stop()
})

test("a superseded load-more releases loading when the reconcile that replaced it fails", async () => {
  const { projection, realtime, control, delayed, openIDs } = await startWithDelayableTasks()
  control.delayNext = true
  const pending = projection.dispatch({ type: "load-more", window: "patients" })
  assert.equal(projection.getSnapshot().patients.loading, true)
  await waitUntil(() => !control.delayNext)
  control.workspaceAvailable = false
  await assert.rejects(realtime.reconcile(0), /workspace authority is unavailable/)
  delayed.resolve(success(taskPage([task("superseded")])))
  await pending

  assert.equal(projection.getSnapshot().patients.loading, false)
  assert.equal(projection.getSnapshot().completedTasks.loading, false)
  assert.deepEqual(openIDs(), ["open-1"])
  await projection.dispatch({ type: "load-more", window: "patients" })
  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  projection.stop()
})

async function startWithDelayableTasks() {
  const realtime = deterministicRealtime()
  const delayed = deferred<WorkspaceAuthorityResult<TaskPage>>()
  const control = { delayNext: false, workspaceAvailable: true }
  const authority: TestAuthority = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(7), tasks: taskPage([]) }),
    workspace: async () => control.workspaceAvailable ? success(workspaceSnapshot(7)) : unavailable(),
    tasks: async (_token, request) => {
      if (request.state === "COMPLETED") return success(taskPage([]))
      if (control.delayNext) {
        control.delayNext = false
        return delayed.promise
      }
      return success(request.cursor ? taskPage([task("open-2")]) : { ...taskPage([task("open-1")]), nextCursor: "open-more" })
    },
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  const openIDs = () => openTasks(projection.getSnapshot()).map((item) => item.id)
  return { projection, realtime, control, delayed, openIDs }
}

test("changing scope, failing closed, and stopping abort in-flight authority requests", async () => {
  const realtime = deterministicRealtime()
  const signals: AbortSignal[] = []
  let workspaceResult: WorkspaceAuthorityResult<WorkspaceSnapshot> = success(workspaceSnapshot(7))
  const authority: TestAuthority = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(7), tasks: taskPage([task("open-1")]) }),
    workspace: async () => workspaceResult,
    call: (_token, _callID, signal) => {
      signals.push(signal)
      return new Promise((resolve) => signal.addEventListener("abort", () => resolve(unavailable())))
    },
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)

  const scopedCall = projection.dispatch({ type: "open-call-context", callID: "call-1" })
  await waitUntil(() => signals.length === 1)
  assert.equal(signals[0]!.aborted, false)
  await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
  assert.equal(signals[0]!.aborted, true)
  await scopedCall

  await realtime.reconcile(0)
  const closedCall = projection.dispatch({ type: "open-call-context", callID: "call-2" })
  await waitUntil(() => signals.length === 2)
  assert.equal(signals[1]!.aborted, false)
  workspaceResult = unauthorizedResult()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().loadState, "unauthorized")
  assert.equal(signals[1]!.aborted, true)
  await closedCall

  const restarted = createWorkspaceProjection({ authority: { ...authority, workspace: async () => success(workspaceSnapshot(7)) }, realtime: realtime.adapter, preferences: memoryPreferences() })
  await restarted.start()
  await realtime.reconcile(0)
  const stoppedCall = restarted.dispatch({ type: "open-call-context", callID: "call-3" })
  await waitUntil(() => signals.length === 3)
  restarted.stop()
  assert.equal(signals[2]!.aborted, true)
  await stoppedCall
  projection.stop()
})

test("authoritative detail refresh updates rail and selection together then clears missing context", async () => {
  const realtime = deterministicRealtime()
  let taskItems = [task("task-1")]
  let selectedResult: WorkspaceAuthorityResult<Task> = success(task("task-1"))
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(9),
    tasks: taskPage(taskItems),
  })
  const authority: TestAuthority = {
    ...baseAuthority,
    tasks: async (_token, request) => success(
        request.state === "OPEN"
          ? taskPage(taskItems)
          : taskPage([]),
    ),
    task: async () => selectedResult,
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)

  taskItems = [task("task-1", { version: 2, title: "Updated in queue" })]
  selectedResult = success(
    task("task-1", { version: 3, title: "Authoritative detail" }),
  )
  await realtime.reconcile(0)

  let state = projection.getSnapshot()
  assert.equal(state.patients.items[0]?.top.version, 3)
  assert.equal(state.patients.items[0]?.top.title, "Authoritative detail")
  assert.equal(state.selection.task?.version, 3)

  selectedResult = unavailable()
  await realtime.reconcile(0)
  state = projection.getSnapshot()
  assert.equal(state.selection.task?.version, 3)
  assert.equal(
    state.selection.taskError,
    "Task details are temporarily unavailable.",
  )
  await projection.dispatch({ type: "retry" })
  assert.equal(realtime.refreshes, 1)

  taskItems = []
  selectedResult = missing()
  await realtime.reconcile(0)

  state = projection.getSnapshot()
  assert.equal(state.selection.task, undefined)
  assert.equal(state.selection.contextPanelOpen, false)
  assert.deepEqual(openTasks(state), [])
  projection.stop()
})

test("search resets only Task windows and delayed prior search cannot overwrite the new key", async () => {
  const realtime = deterministicRealtime()
  const firstSearchRequested = deferred<void>()
  const releaseFirstSearch = deferred<void>()
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(10),
    tasks: taskPage([task("initial-task")]),
  })
  const authority: TestAuthority = {
    ...baseAuthority,
    tasks: async (_token, request) => {
      if (request.state === "COMPLETED") {
        return success(taskPage([]))
      }
      if (request.search === "first") {
        firstSearchRequested.resolve()
        await releaseFirstSearch.promise
      }
      return success(taskPage([
          task(request.search ? `${request.search}-task` : "initial-task"),
        ]))
    },
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-search", value: "first" })
  const staleSearch = projection.dispatch({ type: "submit-search" })
  await firstSearchRequested.promise
  await projection.dispatch({ type: "set-search", value: "second" })
  await projection.dispatch({ type: "submit-search" })
  releaseFirstSearch.resolve()
  await staleSearch

  const state = projection.getSnapshot()
  assert.equal(state.search.applied, "second")
  assert.deepEqual(openTasks(state).map((item) => item.id), ["second-task"])
  assert.deepEqual(state.completedTasks.items, [])
  assert.equal(state.selection.task?.id, "initial-task")
  projection.stop()
})

test("confirmed completion moves a Task to shared completed history and preserves selected evidence", async () => {
  const realtime = deterministicRealtime()
  const openTask = task("task-1", { category: "insurance" })
  const completedTask = task("task-1", { category: "insurance", state: "COMPLETED", version: 2, completedAt: "2026-08-30T12:10:00Z", completedBy: { kind: "HUMAN", subject: "other-staff" } })
  let completed = false
  let sentReason = ""
  let taskReads = 0
  const authority: TestAuthority = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(11), tasks: taskPage([openTask]) }),
    tasks: async (_token, request) => {
      taskReads += 1
      return success(taskPage(request.state === "OPEN" ? (completed ? [] : [openTask]) : (completed ? [completedTask] : [])))
    },
    completeTask: async (_token, _task, reason) => { completed = true; sentReason = reason; return success(completedTask) },
    task: async () => success(completed ? completedTask : openTask),
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: openTask })
  const staleSnapshot = await realtime.prepareReconciliation(0)
  taskReads = 0
  await projection.dispatch({ type: "complete-task", task: openTask, reason: "NOT_NEEDED" })
  assert.equal(sentReason, "NOT_NEEDED")
  assert.equal(taskReads, 2, "one authoritative read per Task window")
  staleSnapshot.apply()
  const state = projection.getSnapshot()
  assert.deepEqual(openTasks(state), [])
  assert.equal(state.patients.counts.all, 0)
  assert.deepEqual(state.completedTasks.items, [completedTask])
  assert.deepEqual(state.selection.task, completedTask)
  assert.equal(state.selection.contextPanelOpen, true)
  assert.deepEqual(state.completion.pending, [])
  assert.equal(realtime.refreshes, 1)
  projection.stop()
})
test("temporary token failure keeps Task completion retryable without expiring the session", async () => {
  const realtime = deterministicRealtime()
  let tokenAvailable = true
  const openTask = task("task-1")
  const authority: TestAuthority = {
    ...deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(20),
      tasks: taskPage([openTask]),
    }),
    authenticate: async () =>
      tokenAvailable
        ? { status: "authenticated", token: "token" }
        : { status: "unavailable" },
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  tokenAvailable = false
  await projection.dispatch({ type: "complete-task", task: openTask, reason: "DONE" })

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "ready")
  assert.deepEqual(state.completion.errors, {
    [openTask.id]: "Task completion is temporarily unavailable. Retry in a moment.",
  })

  tokenAvailable = true
  await projection.dispatch({ type: "complete-task", task: openTask, reason: "DONE" })
  assert.equal(
    projection.getSnapshot().completion.errors[openTask.id],
    "This Task could not be completed. Retry from the row or open its details.",
  )
  projection.stop()
})

test("each page of patients brings the counts and totals the list shows", async () => {
  const realtime = deterministicRealtime()
  const requests: PatientWorkQueryRequest[] = []
  const counts = { all: 6, callBack: 2, texts: 1, appointments: 0, unowned: 3 }
  const totals = { openTasks: 9, patients: 7, patientsOutsideScope: 1 }
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(13), tasks: taskPage([]) }),
      patients: async (_token, request) => {
        requests.push(request)
        return success({
          ...patientPage(request.cursor ? [patientRow(task("task-1")), patientRow(task("task-2"))] : [patientRow(task("task-1"))], request.cursor ? "" : "more"),
          counts,
          totals,
        })
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.deepEqual(requests.map((request) => [request.scope, request.filter, request.cursor]), [["mine", "all", undefined]])
  assert.deepEqual(projection.getSnapshot().patients.counts, counts)
  assert.deepEqual(projection.getSnapshot().patients.totals, totals)
  await projection.dispatch({ type: "load-more", window: "patients" })
  assert.equal(requests.at(-1)?.cursor, "more")
  assert.deepEqual(openTasks(projection.getSnapshot()).map((item) => item.id), ["task-1", "task-2"], "a patient already listed is not listed twice")
  assert.deepEqual(projection.getSnapshot().patients.counts, counts)
  requests.length = 0
  await realtime.reconcile(0)
  assert.deepEqual(requests.map((request) => request.cursor), [undefined, "more"], "a refresh reloads the pages already shown")
  assert.deepEqual(projection.getSnapshot().patients.totals, totals)
  projection.stop()
})

test("rail preferences restore through the projection and corrupted or older values fail safe", async () => {
  const key = "acuity.attentionRail.user-1.practice-1"
  const fresh = { scope: "mine", filter: "all", completedExpanded: false, scrollTop: 0 }
  for (const [stored, restored] of [
    [{ scrollTop: "not-a-number", taskCategory: "obsolete", expanded: ["unknown"] }, fresh],
    [{ version: 1, expanded: ["tasks", "texts"], taskResponsibility: "all", taskCategory: "optical", scrollTop: 120 }, fresh],
    [{ version: 2, scope: "team", filter: "all", completedExpanded: false, scrollTop: 0 }, fresh],
    [{ version: 2, scope: "everyone", filter: "calls", completedExpanded: false, scrollTop: 0 }, fresh],
    [
      { version: 2, scope: "everyone", filter: "unowned", completedExpanded: true, scrollTop: 64 },
      { scope: "everyone", filter: "unowned", completedExpanded: true, scrollTop: 64 },
    ],
  ] as const) {
    const values = new Map<string, string>([[key, JSON.stringify(stored)]])
    const projection = createWorkspaceProjection({
      authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([]) }),
      realtime: deterministicRealtime().adapter,
      preferences: {
        read: (name: string) => values.get(name) ?? null,
        write: (name: string, value: string) => values.set(name, value),
      },
    })
    await projection.start()
    assert.deepEqual(projection.getSnapshot().rail, restored, JSON.stringify(stored))
    projection.stop()
  }

  const values = new Map<string, string>()
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([]) }),
    realtime: realtime.adapter,
    preferences: {
      read: (name: string) => values.get(name) ?? null,
      write: (name: string, value: string) => values.set(name, value),
    },
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "toggle-completed" })
  await projection.dispatch({ type: "set-rail-view", scope: "everyone" })
  await projection.dispatch({ type: "set-rail-view", filter: "callBack" })
  await projection.dispatch({ type: "remember-rail-scroll", scrollTop: 42 })
  assert.deepEqual(JSON.parse(values.get(key) ?? ""), {
    version: 2,
    scope: "everyone",
    filter: "callBack",
    completedExpanded: true,
    scrollTop: 42,
  })
  projection.stop()
})

test("changing the scope or the filter reloads the list and the completed shelf for that view", async () => {
  const realtime = deterministicRealtime()
  const patientRequests: PatientWorkQueryRequest[] = []
  const completedRequests: Parameters<WorkspaceAuthorityAdapter["tasks"]>[1][] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([]) }),
      patients: async (_token, request) => {
        patientRequests.push(request)
        return success(patientPage([patientRow(task(`${request.scope}-${request.filter}`))]))
      },
      tasks: async (_token, request) => {
        completedRequests.push(request)
        return success(taskPage([task(`${request.responsibility}-done`, { state: "COMPLETED" })]))
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  for (const [intent, scope, filter, responsibility] of [
    [{ type: "set-rail-view", scope: "everyone" }, "everyone", "all", "all"],
    [{ type: "set-rail-view", filter: "texts" }, "everyone", "texts", "all"],
    [{ type: "set-rail-view", scope: "mine" }, "mine", "texts", "mine"],
    [{ type: "set-rail-view", filter: "unowned" }, "mine", "unowned", "mine"],
  ] as const) {
    const stale = await realtime.prepareReconciliation(0)
    patientRequests.length = 0
    completedRequests.length = 0
    await projection.dispatch(intent)
    stale.apply()
    const state = projection.getSnapshot()
    assert.deepEqual([state.rail.scope, state.rail.filter], [scope, filter])
    assert.deepEqual(patientRequests.map((request) => [request.scope, request.filter]), [[scope, filter]])
    assert.deepEqual(completedRequests.map((request) => [request.state, request.responsibility]), [["COMPLETED", responsibility]])
    assert.deepEqual(openTasks(state).map((item) => item.id), [`${scope}-${filter}`])
    assert.deepEqual(state.completedTasks.items.map((item) => item.id), [`${responsibility}-done`])
    assert.equal(state.selection.task?.id, "mine-all", "the patient opened on first load stays selected after it left the list")
  }
  const requestCount = patientRequests.length + completedRequests.length
  await projection.dispatch({ type: "toggle-completed" })
  assert.equal(patientRequests.length + completedRequests.length, requestCount, "expanding the shelf uses the current authoritative window")
  assert.equal(projection.getSnapshot().rail.completedExpanded, true)
  projection.stop()
})

test("a filter or scope change never moves the selection or the panel", async () => {
  const realtime = deterministicRealtime()
  const followUp = task("follow-up", { phone: "+13055550142" })
  const text = task("text", { phone: "+13055550142", origin: "INBOUND_MESSAGE_REVIEW" })
  const other = task("other", { phone: "+13055550171", origin: "INBOUND_MESSAGE_REVIEW" })
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([followUp, text, other]) }),
      patients: async (_token, request) => success(patientPage(
        request.scope === "everyone" ? [patientRow(other)]
          : request.filter === "texts"
            ? [patientRow(other), patientRow(text, { openCount: 2 })]
            : request.filter === "callBack" ? [] : [patientRow(followUp, { openCount: 2 }), patientRow(other)],
      )),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: followUp })
  await projection.dispatch({ type: "open-patient-tasks" })
  const reading = projection.getSnapshot().selection
  for (const intent of [
    { type: "set-rail-view", filter: "texts" },
    { type: "set-rail-view", filter: "callBack" },
    { type: "set-rail-view", scope: "everyone" },
    { type: "set-rail-view", filter: "all" },
  ] as const) {
    await projection.dispatch(intent)
    await realtime.reconcile(0)
    assert.deepEqual(projection.getSnapshot().selection, reading, JSON.stringify(intent))
  }
  assert.deepEqual(openTasks(projection.getSnapshot()).map((item) => item.id), [other.id], "the list changed while the selection did not")
  await projection.dispatch({ type: "set-search", value: "other" })
  await projection.dispatch({ type: "submit-search" })
  await realtime.reconcile(0)
  assert.deepEqual(projection.getSnapshot().selection, reading, "a text search")
  projection.stop()
})

test("a chip keeps a searched number that has no Task", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([task("open")]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-search", value: "(305) 555-0199" })
  await projection.dispatch({ type: "submit-search" })
  const searched = projection.getSnapshot().selection
  assert.equal(searched.engagement?.phone, "+13055550199")
  assert.equal(searched.task, undefined)
  await projection.dispatch({ type: "set-rail-view", filter: "callBack" })
  await projection.dispatch({ type: "set-rail-view", scope: "everyone" })
  await realtime.reconcile(0)
  assert.deepEqual(projection.getSnapshot().selection, searched)
  projection.stop()
})

test("search and live refreshes never open a patient once the first load has settled", async () => {
  const realtime = deterministicRealtime()
  let rows: Task[] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([]) }),
      tasks: async (_token, request) => success(taskPage(request.state === "OPEN" ? rows : [])),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.view, "none")
  rows = [task("arrived-later")]
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-search", value: "arrived" })
  await projection.dispatch({ type: "submit-search" })
  await projection.dispatch({ type: "set-rail-view", scope: "everyone" })
  await realtime.reconcile(0)
  const state = projection.getSnapshot()
  assert.deepEqual(openTasks(state).map((item) => item.id), ["arrived-later"])
  assert.equal(state.selection.task, undefined)
  assert.equal(state.selection.view, "none")
  projection.stop()
})

test("authorization loss in one query fails closed across all protected windows", async () => {
  const realtime = deterministicRealtime()
  let unauthorized = false
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(15),
    tasks: taskPage([task("task-1")]),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...baseAuthority,
      tasks: async (_token, request) =>
        unauthorized && request.state === "COMPLETED"
          ? unauthorizedResult()
          : success(request.state === "OPEN" ? taskPage([task("task-1")]) : taskPage([])),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  unauthorized = true
  await realtime.reconcile(0)

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "unauthorized")
  assert.equal(state.discovery, undefined)
  assert.equal(state.workspace, undefined)
  assert.deepEqual(openTasks(state), [])
  assert.deepEqual(state.completedTasks.items, [])
  assert.equal(state.selection.engagement, undefined)
  projection.stop()
})

test("expired authentication is distinct from lost workspace authority", async () => {
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({
        discovery: accessDiscovery(),
        snapshot: workspaceSnapshot(17),
        tasks: taskPage([]),
      }),
      authenticate: async () => ({ status: "unauthenticated" }),
    },
    realtime: deterministicRealtime().adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "unauthenticated")
  assert.equal(state.discovery, undefined)
  assert.equal(state.workspace, undefined)
  projection.stop()
})

test("realtime token expiry fails closed as unauthenticated before sync fallback", async () => {
  const realtime = deterministicRealtime()
  let authenticated = true
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({
        discovery: accessDiscovery(),
        snapshot: workspaceSnapshot(21),
        tasks: taskPage([]),
      }),
      authenticate: async () =>
        authenticated
          ? { status: "authenticated", token: "token" }
          : { status: "unauthenticated" },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  authenticated = false
  assert.equal(await realtime.getToken(), undefined)

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "unauthenticated")
  assert.equal(state.workspace, undefined)
  projection.stop()
})

test("selected AI Interaction detail refreshes authoritatively and missing detail clears context", async () => {
  const realtime = deterministicRealtime()
  let detailResult: WorkspaceAuthorityResult<AiInteractionDetail> = success(
    aiInteractionDetail("interaction-1", "First detail"),
  )
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(19),
    tasks: taskPage([]),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...baseAuthority,
      aiInteraction: async () => detailResult,
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({
    type: "open-ai-context",
    interactionID: "interaction-1",
  })
  assert.equal(
    projection.getSnapshot().selection.aiInteraction?.summary,
    "First detail",
  )

  detailResult = success(
    aiInteractionDetail("interaction-1", "Refreshed detail"),
  )
  await realtime.reconcile(0)
  assert.equal(
    projection.getSnapshot().selection.aiInteraction?.summary,
    "Refreshed detail",
  )

  detailResult = missing()
  await realtime.reconcile(0)
  const state = projection.getSnapshot()
  assert.equal(state.selection.aiInteractionID, "")
  assert.equal(state.selection.aiInteraction, undefined)
  assert.equal(state.selection.contextPanelOpen, false)
  projection.stop()
})

function deterministicAuthority({
  discovery,
  snapshot,
  tasks,
}: {
  discovery: AccessDiscovery
  snapshot: WorkspaceSnapshot
  tasks: TaskPage
}): TestAuthority {
  return {
    authenticate: async () => ({ status: "authenticated", token: "token" }),
    discover: async () => success(discovery),
    workspace: async () => success(snapshot),
    tasks: async (_token, request) => success(request.state === "OPEN" ? tasks : taskPage([])),
    aiInteraction: async () => missing(),
    task: async (_token, taskID) => {
      const selected = tasks.items.find((item) => item.id === taskID)
      return selected ? success(selected) : missing()
    },
    call: async () => missing(),
    completeTask: async () => unavailable(),
  }
}

function success<T>(data: T): WorkspaceAuthorityResult<T> {
  return { kind: "success", data }
}

function missing(): PortalFailure {
  return { kind: "missing", retryable: false, status: 404 }
}

function unavailable(): PortalFailure {
  return { kind: "unavailable", retryable: true, status: 503 }
}

function unauthorizedResult(): PortalFailure {
  return { kind: "unauthorized", retryable: false, status: 403 }
}

function deterministicRealtime() {
  let callbacks: WorkspaceRealtimeCallbacks | undefined
  let scope: { practiceID: string; locationID: string } | undefined
  let refreshes = 0
  const adapter: WorkspaceRealtimeAdapter = {
    connect(nextCallbacks) {
      callbacks = nextCallbacks
      return {
        setScope(nextScope) {
          scope = nextScope
        },
        async refresh() {
          refreshes += 1
          if (!scope) return
          try {
            const reconciliation = await nextCallbacks.reconcile({ scope, token: "token", signal: new AbortController().signal, minimumVersion: 0 })
            reconciliation.apply()
          } catch (error) {
            if (error instanceof WorkspaceProjectionAccessError) nextCallbacks.onUnauthorized()
          }
        },
        visibilityChanged() {},
        stop() {},
      }
    },
  }
  return {
    adapter,
    get scope() {
      return scope
    },
    get refreshes() {
      return refreshes
    },
    setConnection(connection: "connecting" | "connected" | "degraded") {
      assert.ok(callbacks)
      callbacks.onStateChange(connection)
    },
    async getToken() {
      assert.ok(callbacks)
      return callbacks.getToken()
    },
    async reconcile(minimumVersion: number) {
      try {
        const reconciliation = await this.prepareReconciliation(minimumVersion)
        reconciliation.apply()
      } catch (error) {
        if (error instanceof WorkspaceProjectionAccessError) {
          callbacks?.onUnauthorized()
          return
        }
        throw error
      }
    },
    async prepareReconciliation(minimumVersion: number, signal = new AbortController().signal) {
      assert.ok(callbacks)
      assert.ok(scope)
      return callbacks.reconcile({
        scope,
        token: "token",
        signal,
        minimumVersion,
      })
    },
  }
}

function accessDiscovery(): AccessDiscovery {
  return {
    actor: {
      type: "HUMAN",
      subject: "user-1",
      email: "staff@abita.test",
    },
    platformOperator: false,
    practices: [
      {
        ...practice(),
        membership: {
          id: "membership-1",
          role: "STAFF",
          locationScope: "ALL",
        },
        locations: [location("location-1"), location("location-2")],
      },
    ],
  }
}

test("booking analytics is Admin-only and operator diagnostics remain separate", async () => {
  for (const role of ["ADMIN", "STAFF"] as const) {
    const discovery = accessDiscovery()
    discovery.practices[0].membership!.role = role
    const realtime = deterministicRealtime()
    const projection = createWorkspaceProjection({
      authority: deterministicAuthority({ discovery, snapshot: workspaceSnapshot(1), tasks: taskPage([]) }),
      realtime: realtime.adapter,
      preferences: { read: () => null, write: () => {} },
    })
    await projection.start()
    await projection.dispatch({ type: "select-analytics" })
    assert.equal(projection.getSnapshot().selection.view, role === "ADMIN" ? "analytics" : "none")
    await projection.dispatch({ type: "select-operator-analytics" })
    assert.notEqual(projection.getSnapshot().selection.view, "operator-analytics")
    if (role === "ADMIN") {
      await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
      assert.equal(projection.getSnapshot().selection.view, "analytics")
    }
    await projection.dispatch({ type: "select-manage-agent" })
    assert.equal(projection.getSnapshot().selection.view, "manage-agent")
    assert.equal(projection.getSnapshot().selection.agentPage, "home")
    await projection.dispatch({ type: "select-manage-agent", page: "insurance" })
    await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
    assert.equal(projection.getSnapshot().selection.view, "manage-agent")
    assert.equal(projection.getSnapshot().selection.agentPage, "insurance")
    projection.stop()
  }
})

function practice(): Practice {
  return { id: "practice-1", name: "Example Eye Group", version: 1 }
}

function location(id: string): Location {
  return { id, name: id === "location-1" ? "Downtown" : "North" }
}

function workspaceSnapshot(version: number): WorkspaceSnapshot {
  return {
    schemaVersion: "2026-07-24",
    version,
    state: "EMPTY",
    actor: {
      type: "HUMAN",
      subject: "user-1",
      email: "staff@abita.test",
    },
    practice: practice(),
    location: location("location-1"),
    platformOperator: false,
    navigation: [],
  }
}

function patientRow(top: Task, overrides: Partial<PatientWorkRow> = {}): PatientWorkRow {
  return {
    phone: top.phone,
    displayName: "",
    openCount: 1,
    highPriority: top.urgency === "high_priority",
    hasUnowned: false,
    activityAt: top.updatedAt,
    top,
    ...overrides,
  }
}

function patientPage(rows: PatientWorkRow[], nextCursor = ""): PatientWorkPage {
  return {
    rows,
    counts: { all: rows.length, callBack: 0, texts: 0, appointments: 0, unowned: 0 },
    totals: { openTasks: rows.length, patients: rows.length, patientsOutsideScope: 0 },
    nextCursor,
  }
}

function phoneFor(id: string) {
  let hash = 0
  for (const character of id) hash = (hash * 31 + character.charCodeAt(0)) % 10_000_000
  return `+1555${String(hash).padStart(7, "0")}`
}

function task(id: string, overrides: Partial<Task> = {}): Task {
  return {
    id,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    phone: phoneFor(id),
    title: "Return patient call",
    state: "OPEN",
    origin: "ABITA_AI",
    urgency: "normal",
    createdBy: { kind: "SERVICE", subject: "abita" },
    createdAt: "2026-08-30T12:00:00Z",
    version: 1,
    updatedAt: "2026-08-30T12:00:00Z",
    relatedInteractionCount: 0,
    interactions: [],
    ...overrides,
  }
}

function taskPage(items: Task[]): TaskPage {
  return {
    items,
    nextCursor: "",
    counts: {
      tasks: items.length,
      categories: {
        billing: 0,
        appointments: 0,
        documentation: 0,
        optical: 0,
        medication: 0,
        referrals: 0,
        other: items.length,
      },
    },
  }
}

function memoryPreferences() {
  const values = new Map<string, string>()
  return {
    read: (key: string) => values.get(key) ?? null,
    write: (key: string, value: string) => values.set(key, value),
  }
}

function aiInteractionDetail(id: string, summary: string): AiInteractionDetail {
  return {
    id,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    sourceCallId: `${id}-call`,
    phone: "+15551234567",
    officePhone: "+15557654321",
    startedAt: "2026-08-30T12:00:00Z",
    endedAt: "2026-08-30T12:05:00Z",
    status: "COMPLETED",
    summary,
    appointmentOutcome: "BOOKING",
    appointment: {},
    createdAt: "2026-08-30T12:05:00Z",
    updatedAt: "2026-08-30T12:05:00Z",
  }
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

test("opening Task context preserves pending authoritative count reconciliation", async () => {
  const realtime = deterministicRealtime()
  const openTask = task("task-1")
  let currentPage = taskPage([openTask])
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(13), tasks: currentPage }),
      tasks: async () => success(currentPage),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  currentPage = taskPage([openTask, task("task-2")])
  const pending = await realtime.prepareReconciliation(0)
  await projection.dispatch({ type: "open-task-context", task: openTask })
  pending.apply()
  assert.equal(projection.getSnapshot().patients.counts.all, 2)
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, true)
  projection.stop()
})

for (const type of ["task-committed", "task-created"] as const) {
  test(`${type} fences counts fetched before the committed change`, async () => {
    const realtime = deterministicRealtime()
    const openTask = task("task-1")
    let currentPage = taskPage([openTask])
    const projection = createWorkspaceProjection({
      authority: {
        ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(13), tasks: currentPage }),
        tasks: async () => success(currentPage),
      },
      realtime: realtime.adapter,
      preferences: memoryPreferences(),
    })
    await projection.start()
    await realtime.reconcile(0)
    currentPage = taskPage(Array.from({ length: 5 }, (_, i) => task(`task-${i + 1}`)))
    const pending = await realtime.prepareReconciliation(0)
    currentPage = taskPage(type === "task-created" ? [openTask, task("new-task")] : [openTask])
    await projection.dispatch({ type, task: type === "task-created" ? task("new-task") : task("task-1", { version: 2 }) })
    const committedCounts = projection.getSnapshot().patients.counts
    assert.equal(committedCounts.all, type === "task-created" ? 2 : 1)
    pending.apply()
    assert.deepEqual(projection.getSnapshot().patients.counts, committedCounts)
    assert.equal(realtime.refreshes, 1)
    projection.stop()
  })
}

for (const legacyFilters of [false, true]) {
  test(`opening Activity Task preserves an empty queue${legacyFilters ? " with retired saved filters" : ""}`, async () => {
    const realtime = deterministicRealtime()
    const projection = createWorkspaceProjection({
      authority: {
        ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(20), tasks: taskPage([]) }),
        tasks: async (_token, request) => {
          assert.ok(request.state === "OPEN" || request.state === "COMPLETED")
          return success(taskPage([]))
        },
      },
      realtime: realtime.adapter,
      preferences: {
        read: (key) => legacyFilters && key === "acuity.attentionRail.user-1.practice-1"
          ? JSON.stringify({ version: 1, scrollTop: 0, taskState: "COMPLETED" })
          : null,
        write: () => {},
      },
    })
    await projection.start()
    await realtime.reconcile(0)
    const before = projection.getSnapshot().patients
    await projection.dispatch({ type: "open-task-context", task: task("activity-open-unflagged") })
    assert.deepEqual(projection.getSnapshot().patients.items, before.items)
    assert.deepEqual(projection.getSnapshot().patients.counts, before.counts)
    assert.equal(projection.getSnapshot().selection.task?.id, "activity-open-unflagged")
    projection.stop()
  })
}

test("completion waits for commitment and failed refresh retains rows with a visible error", async () => {
  const realtime = deterministicRealtime()
  const open = task("open")
  const completed = task("open", { state: "COMPLETED", version: 2 })
  const committed = deferred<WorkspaceAuthorityResult<Task>>()
  let failLists = false
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(22), tasks: taskPage([open]) }),
      tasks: async (_token, request) => failLists ? unavailable() : success(taskPage(request.state === "OPEN" ? [open] : [])),
      completeTask: async () => committed.promise,
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  const pending = projection.dispatch({ type: "complete-task", task: open, reason: "DONE" })
  assert.deepEqual(projection.getSnapshot().completion.pending, [open.id])
  assert.deepEqual(openTasks(projection.getSnapshot()), [open])
  assert.deepEqual(projection.getSnapshot().completedTasks.items, [])
  failLists = true
  committed.resolve(success(completed))
  await pending
  assert.deepEqual(projection.getSnapshot().selection.task, completed)
  assert.deepEqual(openTasks(projection.getSnapshot()), [open], "failed list read cannot establish updated membership")
  assert.ok(projection.getSnapshot().patients.error)
  assert.ok(projection.getSnapshot().completedTasks.error)
  projection.stop()
})

test("phone search opens Engagement history without inventing or hiding a Task", async () => {
  const realtime = deterministicRealtime()
  const requests: Parameters<WorkspaceAuthorityAdapter["tasks"]>[1][] = []
  const open = task("open")
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(22), tasks: taskPage([open]) }),
      tasks: async (_token, request) => {
        requests.push(request)
        return success(taskPage(request.state === "OPEN" ? [open] : []))
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-search", value: "(555) 123-4567" })
  await projection.dispatch({ type: "submit-search" })
  assert.equal(projection.getSnapshot().selection.engagement?.phone, "+15551234567")
  assert.equal(projection.getSnapshot().selection.task, undefined)
  assert.deepEqual(openTasks(projection.getSnapshot()), [open])
  assert.equal(projection.getSnapshot().search.applied, "")
  assert.equal(requests.at(-1)?.search, undefined)
  projection.stop()
})

test("Complete and next uses refreshed recent order without moving on remote completion", async () => {
  const realtime = deterministicRealtime()
  const first = task("first")
  const next = task("next")
  const completed = task("first", { state: "COMPLETED", version: 2 })
  let done = false
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(22), tasks: taskPage([first, next]) }),
      tasks: async (_token, request) =>
        success(taskPage(request.state === "COMPLETED" ? (done ? [completed] : []) : done ? [next] : [first, next])),
    }, realtime: realtime.adapter, preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: first })
  done = true
  await projection.dispatch({ type: "task-committed", task: completed })
  assert.equal(projection.getSnapshot().selection.task?.id, first.id)
  await projection.dispatch({ type: "task-committed", task: completed, advance: true })
  assert.equal(projection.getSnapshot().selection.task?.id, next.id)
  projection.stop()
})

for (const obsolete of ["scope", "abort", "stop"] as const) {
  test(`obsolete ${obsolete} denial cannot clear the authorized workspace`, async () => {
    const realtime = deterministicRealtime()
    const requested = deferred<void>()
    const release = deferred<void>()
    let delay = false
    const base = deterministicAuthority({
      discovery: accessDiscovery(), snapshot: workspaceSnapshot(20), tasks: taskPage([task("first")]),
    })
    const authority: TestAuthority = {
      ...base,
      workspace: async (token, scope, signal) => {
        if (delay && scope.locationID === "location-1") {
          requested.resolve()
          await release.promise
          return unauthorizedResult()
        }
        return base.workspace(token, scope, signal)
      },
    }
    const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
    await projection.start()
    await realtime.reconcile(0)
    delay = true
    const controller = new AbortController()
    const pending = realtime.prepareReconciliation(0, controller.signal)
    await requested.promise
    if (obsolete === "scope") {
      await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
      await realtime.reconcile(0)
    } else if (obsolete === "abort") {
      controller.abort()
    } else {
      projection.stop()
    }
    const authorized = projection.getSnapshot()
    release.resolve()
    const stale = await pending
    stale.apply()
    assert.equal(projection.getSnapshot(), authorized)
    assert.equal(projection.getSnapshot().loadState, "ready")
    projection.stop()
  })
}

for (const outcome of ["authenticated", "unauthenticated"] as const) {
  test(`obsolete ${outcome} token cannot clear a same-Location scope change`, async (t) => {
    const release = deferred<void>()
    const authenticating = deferred<void>()
    let delay = false
    let connections = 0
    let workspaceReads = 0
    const base = deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(20), tasks: taskPage([]) })
    const projection = createWorkspaceProjection({
      authority: {
        ...base,
        workspace: async (...args) => {
          workspaceReads += 1
          return base.workspace(...args)
        },
        authenticate: async () => {
          if (delay) {
            delay = false
            authenticating.resolve()
            await release.promise
            return outcome === "authenticated"
              ? { status: "authenticated", token: "token" }
              : { status: "unauthenticated" }
          }
          return base.authenticate()
        },
      },
      realtime: {
        connect: (callbacks) => createWorkspaceSync({
          ...callbacks,
          realtimeURL: "https://realtime.example",
          fetch: async () => {
            connections += 1
            return new Response(new ReadableStream({
              start(controller) {
                controller.enqueue(new TextEncoder().encode(
                  'event: ready\ndata: {"practiceId":"practice-1","version":20}\n\n',
                ))
              },
            }))
          },
        }),
      },
      preferences: memoryPreferences(),
    })
    t.after(() => projection.stop())
    await projection.start()
    await waitUntil(() => projection.getSnapshot().loadState === "ready")
    const previous = projection.getSnapshot().scope
    delay = true
    await projection.dispatch({ type: "retry" })
    await authenticating.promise
    const locationScopeID = previous.locationScopeID ? "" : previous.locationID
    await projection.dispatch({
      type: "select-scope",
      practiceID: previous.practiceID,
      locationScopeID,
    })
    assert.equal(projection.getSnapshot().scope.locationID, previous.locationID)
    release.resolve()
    await waitUntil(() => projection.getSnapshot().loadState === "unauthorized" || workspaceReads === 2)
    assert.equal(projection.getSnapshot().loadState, "ready")
    assert.ok(projection.getSnapshot().discovery)
    assert.equal(projection.getSnapshot().scope.locationScopeID, locationScopeID)
    assert.equal(connections, 1)
  })
}

async function waitUntil(predicate: () => boolean) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (predicate()) return
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
  assert.fail("workspace did not reach the expected state")
}


test("failed retries without a workspace snapshot preserve unavailable until recovery", async () => {
  const realtime = deterministicRealtime()
  let available = false
  const base = deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(4), tasks: taskPage([]) })
  const projection = createWorkspaceProjection({
    authority: {
      ...base,
      workspace: async (...args) => available ? base.workspace(...args) : unavailable(),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await assert.rejects(realtime.reconcile(0), /workspace authority is unavailable/)
  realtime.setConnection("degraded")
  assert.equal(projection.getSnapshot().loadState, "unavailable")
  for (let attempt = 0; attempt < 2; attempt += 1) {
    await projection.dispatch({ type: "retry" })
    await assert.rejects(realtime.reconcile(0), /workspace authority is unavailable/)
    assert.equal(projection.getSnapshot().loadState, "unavailable")
    assert.equal(projection.getSnapshot().workspace, undefined)
  }
  available = true
  await projection.dispatch({ type: "retry" })
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().loadState, "ready")
  assert.equal(projection.getSnapshot().workspace?.version, 4)
  projection.stop()
})

test("an unavailable URL Task falls back to the first Task without adding history", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const navigation = memoryNavigation({ view: "task", taskID: "gone-task" })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, first.id)
  assert.deepEqual(navigation.writes, [{ location: { view: "task", taskID: first.id }, mode: "replace" }])
  projection.stop()
})

test("refresh restores authorized page views and drops unauthorized ones", async () => {
  for (const [requested, expected] of [["manage-agent", "manage-agent"], ["analytics", "engagement"]] as const) {
    const realtime = deterministicRealtime()
    const navigation = memoryNavigation({ view: requested })
    const projection = createWorkspaceProjection({
      authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([task("first-task")]) }),
      realtime: realtime.adapter,
      preferences: memoryPreferences(),
      navigation,
    })
    await projection.start()
    await realtime.reconcile(0)
    assert.equal(projection.getSnapshot().selection.view, expected)
    assert.deepEqual(navigation.writes.map((write) => write.mode), expected === "engagement" ? ["replace"] : [])
    projection.stop()
  }
})

test("back and forward restore the Task or page view in the URL", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const second = task("second-task", { phone: "+15557654321" })
  const navigation = memoryNavigation()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first, second]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: second })
  await projection.dispatch({ type: "select-manage-agent" })
  assert.deepEqual(navigation.writes, [
    { location: { view: "task", taskID: first.id }, mode: "replace" },
    { location: { view: "task", taskID: second.id }, mode: "push" },
    { location: { view: "manage-agent" }, mode: "push" },
  ])
  navigation.go({ view: "task", taskID: second.id })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.view, "engagement")
  assert.equal(projection.getSnapshot().selection.task?.id, second.id)
  navigation.go({ view: "task", taskID: first.id })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.task?.id, first.id)
  navigation.go({ view: "manage-agent" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  navigation.go({ view: "task", taskID: "gone-task" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  assert.deepEqual(navigation.writes.slice(3), [{ location: { view: "manage-agent" }, mode: "replace" }])
  projection.stop()
})

test("the agent Home is the landing page and Work keeps its own URL", async () => {
  const realtime = deterministicRealtime()
  const navigation = memoryNavigation({ view: "manage-agent" })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([task("first-task")]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  assert.equal(projection.getSnapshot().selection.agentPage, "home")
  assert.equal(projection.getSnapshot().selection.task, undefined)
  assert.deepEqual(navigation.writes, [])
  await projection.dispatch({ type: "select-work" })
  assert.equal(projection.getSnapshot().selection.view, "engagement")
  assert.equal(projection.getSnapshot().selection.task?.id, "first-task")
  assert.deepEqual(navigation.writes, [{ location: { view: "task", taskID: "first-task" }, mode: "push" }])
  projection.stop()
})

test("Work with no Tasks has its own URL", async () => {
  const realtime = deterministicRealtime()
  const navigation = memoryNavigation({ view: "manage-agent" })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-work" })
  assert.equal(projection.getSnapshot().selection.view, "none")
  assert.deepEqual(navigation.writes, [{ location: { view: "work" }, mode: "push" }])
  await projection.dispatch({ type: "select-manage-agent" })
  navigation.go({ view: "work" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.view, "none")
  projection.stop()
})

test("Manage agent pages are kept in the URL and restored by refresh and back", async () => {
  const realtime = deterministicRealtime()
  const navigation = memoryNavigation({ view: "manage-agent", page: "knowledge" })
  const discovery = accessDiscovery()
  discovery.practices[0].membership!.role = "ADMIN"
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery, snapshot: workspaceSnapshot(1), tasks: taskPage([task("first-task")]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  assert.equal(projection.getSnapshot().selection.agentPage, "knowledge")
  await projection.dispatch({ type: "select-manage-agent", page: "insurance" })
  await projection.dispatch({ type: "select-manage-agent", page: "transcripts" })
  assert.deepEqual(navigation.writes, [
    { location: { view: "manage-agent", page: "insurance" }, mode: "push" },
    { location: { view: "manage-agent", page: "transcripts" }, mode: "push" },
  ])
  navigation.go({ view: "manage-agent", page: "insurance" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.agentPage, "insurance")
  navigation.go({ view: "analytics" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.view, "analytics")
  assert.equal(projection.getSnapshot().selection.agentPage, "insurance")
  navigation.go({ view: "manage-agent" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().selection.agentPage, "home")
  assert.equal(navigation.writes.length, 2)
  projection.stop()
})

test("a URL Task from another authorized Practice opens in that Practice", async () => {
  const realtime = deterministicRealtime()
  const discovery = accessDiscovery()
  const otherPractice = { ...discovery.practices[0], id: "practice-2", name: "Second Eye Group", locations: [location("location-3")] }
  const linked = task("linked-task", { practiceId: otherPractice.id, locationId: "location-3" })
  const preferences = memoryPreferences()
  preferences.write("acuity.selectedPractice", "practice-1")
  const base = deterministicAuthority({ discovery: { ...discovery, practices: [...discovery.practices, otherPractice] }, snapshot: workspaceSnapshot(1), tasks: taskPage([task("first-task")]) })
  const projection = createWorkspaceProjection({
    authority: { ...base, task: async (_token, taskID) => taskID === linked.id ? success(linked) : missing() },
    realtime: realtime.adapter,
    preferences,
    navigation: memoryNavigation({ view: "task", taskID: linked.id }),
  })
  await projection.start()
  assert.deepEqual(realtime.scope, { practiceID: otherPractice.id, locationID: "location-3" })
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, linked.id)
  assert.equal(preferences.read("acuity.selectedPractice"), otherPractice.id)
  projection.stop()
})

test("a Task chosen while back is loading wins over the restored Task", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const second = task("second-task", { phone: "+15557654321" })
  const third = task("third-task", { phone: "+15550001111" })
  const pending = deferred<WorkspaceAuthorityResult<Task>>()
  const navigation = memoryNavigation()
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first, second, third]) }),
      task: () => pending.promise,
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: second })
  navigation.go({ view: "task", taskID: first.id })
  const restoring = projection.dispatch({ type: "navigation-changed" })
  await projection.dispatch({ type: "select-task", task: third })
  pending.resolve(success(first))
  await restoring
  assert.equal(projection.getSnapshot().selection.task?.id, third.id)
  assert.deepEqual(navigation.writes.at(-1), { location: { view: "task", taskID: third.id }, mode: "push" })
  projection.stop()
})

test("a temporarily unavailable URL Task stays in the URL behind a visible retry", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const navigation = memoryNavigation({ view: "task", taskID: first.id })
  let available = false
  const base = deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first]) })
  const projection = createWorkspaceProjection({
    authority: { ...base, task: async (...args) => available ? base.task(...args) : unavailable() },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  assert.equal(projection.getSnapshot().loadState, "unavailable")
  assert.deepEqual(navigation.writes, [])
  available = true
  await projection.dispatch({ type: "retry" })
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, first.id)
  assert.deepEqual(navigation.writes, [])
  available = false
  navigation.go({ view: "task", taskID: "other-task" })
  await projection.dispatch({ type: "navigation-changed" })
  assert.equal(projection.getSnapshot().loadState, "unavailable")
  assert.deepEqual(navigation.writes, [])
  projection.stop()
})

test("back during the initial load restores the newer URL", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const second = task("second-task", { phone: "+15557654321" })
  const navigation = memoryNavigation({ view: "task", taskID: first.id })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first, second]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  assert.equal(projection.getSnapshot().loadState, "loading")
  navigation.go({ view: "task", taskID: second.id })
  await projection.dispatch({ type: "navigation-changed" })
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, second.id)
  assert.deepEqual(navigation.writes, [])
  projection.stop()
})

test("back during a real sync reconciliation reloads the unchanged scope", async (t) => {
  const first = task("first-task")
  const second = task("second-task", { phone: "+15557654321" })
  const navigation = memoryNavigation({ view: "task", taskID: first.id })
  const gate = deferred<void>()
  const requested = deferred<void>()
  let workspaceReads = 0
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  const base = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(1),
    tasks: taskPage([first, second]),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...base,
      workspace: async (...args) => {
        workspaceReads += 1
        requested.resolve()
        if (workspaceReads === 1) await gate.promise
        return base.workspace(...args)
      },
    },
    realtime: {
      connect: (callbacks) => createWorkspaceSync({
        ...callbacks,
        realtimeURL: "https://realtime.example",
        fetch: async () => new Response(new ReadableStream<Uint8Array>({
          start(controller) { stream = controller },
        })),
      }),
    },
    preferences: memoryPreferences(),
    navigation,
  })
  t.after(() => projection.stop())

  await projection.start()
  await waitUntil(() => Boolean(stream))
  stream!.enqueue(new TextEncoder().encode(
    'event: ready\ndata: {"practiceId":"practice-1","version":1}\n\n',
  ))
  await requested.promise
  navigation.go({ view: "task", taskID: second.id })
  await projection.dispatch({ type: "navigation-changed" })
  gate.resolve()

  await waitUntil(() => projection.getSnapshot().loadState === "ready")
  assert.ok(projection.getSnapshot().workspace)
  assert.equal(projection.getSnapshot().selection.task?.id, second.id)
  assert.equal(workspaceReads, 2)
})

function memoryNavigation(initial: WorkspaceLocation = { view: "work" }) {
  let current = initial
  const writes: Array<{ location: WorkspaceLocation; mode: "push" | "replace" }> = []
  return {
    writes,
    go(location: WorkspaceLocation) {
      current = location
    },
    read: () => current,
    write(location: WorkspaceLocation, mode: "push" | "replace") {
      current = location
      writes.push({ location, mode })
    },
  }
}

type LiveRequest = Parameters<WorkspaceAuthorityAdapter["tasks"]>[1]

function liveWorkspace({ openRows, hidden = () => false, environment }: {
  openRows: number
  hidden?: () => boolean
  environment?: Parameters<typeof createWorkspaceProjection>[0]["environment"]
}) {
  const origin = Date.UTC(2026, 7, 30, 12)
  const server = {
    version: 1,
    open: Array.from({ length: openRows }, (_, index) =>
      task(`open-${String(index).padStart(3, "0")}`, { updatedAt: new Date(origin - index * 60_000).toISOString() })),
    completed: [] as Task[],
    workspaceGate: undefined as Promise<void> | undefined,
    pageGate: undefined as Promise<void> | undefined,
  }
  const requests: string[] = []
  const page = (rows: Task[], request: LiveRequest) => {
    const start = request.cursor ? rows.findIndex((row) => row.id === request.cursor) + 1 : 0
    const limit = request.limit ?? 50
    const items = rows.slice(start, start + limit)
    return success({
      items,
      nextCursor: start + limit < rows.length ? items.at(-1)!.id : "",
      ...(request.includeCounts !== false ? { counts: taskPage(rows).counts } : {}),
    })
  }
  const authority: TestAuthority = {
    authenticate: async () => ({ status: "authenticated", token: "token" }),
    discover: async () => success(accessDiscovery()),
    workspace: async () => {
      requests.push("workspace")
      const version = server.version
      await server.workspaceGate
      return success(workspaceSnapshot(version))
    },
    tasks: async (_token, request) => {
      requests.push(`tasks:${request.state}:${request.cursor ?? ""}`)
      if (request.cursor) await server.pageGate
      return page(request.state === "OPEN" ? server.open : server.completed, request)
    },
    task: async (_token, taskID) => {
      requests.push(`task:${taskID}`)
      const found = [...server.open, ...server.completed].find((item) => item.id === taskID)
      return found ? success(found) : missing()
    },
    aiInteraction: async () => {
      requests.push("ai")
      return missing()
    },
    call: async () => {
      requests.push("call")
      return missing()
    },
    completeTask: async (_token, target) => {
      requests.push("complete")
      const at = new Date(origin + 60_000).toISOString()
      const done = task(target.id, { state: "COMPLETED", version: target.version + 1, completedAt: at, updatedAt: at })
      server.open = server.open.filter((item) => item.id !== target.id)
      server.completed = [done, ...server.completed]
      server.version += 1
      return success(done)
    },
  }
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  const realtime: WorkspaceRealtimeAdapter = {
    connect(callbacks) {
      const sync = createWorkspaceSync({
        realtimeURL: "https://realtime.example",
        fetch: async () => new Response(new ReadableStream<Uint8Array>({ start(controller) { stream = controller } })),
        getToken: callbacks.getToken,
        reconcile: callbacks.reconcile,
        onStateChange: callbacks.onStateChange,
        onUnauthorized: callbacks.onUnauthorized,
        isHidden: hidden,
      })
      return { setScope: sync.setScope, refresh: sync.refresh, visibilityChanged: sync.visibilityChanged, stop: sync.stop }
    },
  }
  const projection = createWorkspaceProjection({ authority, realtime, preferences: memoryPreferences(), environment })
  const send = (type: "ready" | "hint", version: number) =>
    stream!.enqueue(new TextEncoder().encode(`event: ${type}\ndata: {"practiceId":"practice-1","version":${version}}\n\n`))
  const settle = () => new Promise((resolve) => setTimeout(resolve, 20))
  return {
    projection, server, requests, send, settle,
    async connect() {
      await projection.start()
      await waitUntil(() => Boolean(stream))
      send("ready", server.version)
      await waitUntil(() => projection.getSnapshot().workspace?.version === server.version)
      await settle()
    },
    async loadOpenRows(count: number) {
      while (projection.getSnapshot().patients.items.length < count) {
        await projection.dispatch({ type: "load-more", window: "patients" })
      }
    },
    reconciles: () => requests.filter((request) => request === "workspace").length,
  }
}

function manualClock() {
  const clock = { timers: new Map<number, () => void>(), next: 0 }
  return {
    clock,
    environment: {
      clock: {
        setTimeout: (callback: () => void) => {
          clock.timers.set(++clock.next, callback)
          return clock.next
        },
        clearTimeout: (id: number) => {
          clock.timers.delete(id)
        },
      },
    },
    fireAll() {
      const pending = [...clock.timers.values()]
      clock.timers.clear()
      for (const fire of pending) fire()
    },
  }
}

test("a hidden tab makes no requests on hints and one catch-up when visible", async () => {
  let hidden = false
  const live = liveWorkspace({ openRows: 120, hidden: () => hidden })
  await live.connect()
  await live.loadOpenRows(120)
  live.requests.length = 0
  hidden = true
  for (let version = 2; version <= 6; version += 1) {
    live.server.version = version
    live.send("hint", version)
  }
  await live.settle()
  assert.deepEqual(live.requests, [])
  hidden = false
  await live.projection.dispatch({ type: "visibility-changed" })
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === 6)
  await live.settle()
  assert.equal(live.reconciles(), 1)
  assert.equal(live.requests.length, 6)
  live.projection.stop()
})

test("completing a Task refreshes once and skips the server hint for the same version", async () => {
  const manual = manualClock()
  const live = liveWorkspace({ openRows: 3, environment: manual.environment })
  await live.connect()
  const first = live.projection.getSnapshot().patients.items[0].top
  await live.projection.dispatch({ type: "select-task", task: first })
  manual.fireAll()
  live.requests.length = 0
  await live.projection.dispatch({ type: "complete-task", task: first, reason: "DONE" })
  const revision = live.projection.getSnapshot().detailRevision
  manual.fireAll()
  assert.equal(live.projection.getSnapshot().detailRevision, revision + 1)
  assert.deepEqual(live.requests.toSorted(), ["complete", `task:${first.id}`, "tasks:COMPLETED:", "tasks:OPEN:", "workspace"])
  assert.equal(live.projection.getSnapshot().workspace?.version, 2)
  assert.deepEqual(live.projection.getSnapshot().completedTasks.items.map((item) => item.id), [first.id])
  live.send("hint", 2)
  await live.settle()
  assert.equal(live.reconciles(), 1)
  assert.equal(live.requests.length, 5)
  live.projection.stop()
})

function liveCall(overrides: Partial<CallingCall> = {}): CallingCall {
  return {
    id: "call-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    direction: "INBOUND",
    entryPoint: "STANDALONE",
    state: "CONNECTED",
    phone: "+15551234567",
    phoneSource: "provider",
    displayName: "",
    nameSource: "",
    transferReason: "",
    reasonSource: "",
    providerTermination: "",
    endRequested: false,
    callerId: "+15550000000",
    retryAllowed: false,
    version: 1,
    ...overrides,
  }
}

async function projectionOnCall() {
  const realtime = deterministicRealtime()
  const recovery = task("recovery", { origin: "MISSED_CALL_RECOVERY" })
  const other = task("other")
  const rows = new Map([[recovery.id, recovery], [other.id, other]])
  const authority: TestAuthority = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(30), tasks: taskPage([recovery, other]) }),
    tasks: async (_token, request) =>
      success(taskPage([...rows.values()].filter((row) => row.state === request.state))),
    task: async (_token, taskID) => {
      const row = rows.get(taskID)
      return row ? success(row) : missing()
    },
    completeTask: async (_token, target) => {
      const completed: Task = { ...rows.get(target.id)!, state: "COMPLETED", version: target.version + 1, completedAt: "2026-08-30T12:10:00Z" }
      rows.set(completed.id, completed)
      return success(completed)
    },
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: recovery })
  await projection.dispatch({
    type: "call-connected",
    call: liveCall({ phone: recovery.phone, recoveryTask: { id: recovery.id, title: recovery.title, state: "OPEN", relatedInteractionCount: 0 } }),
  })
  const onCall = projection.getSnapshot().selection
  assert.equal(onCall.contextView, "call")
  assert.equal(onCall.task?.id, recovery.id)
  return { projection, rows, recovery, other, onCall }
}

function assertStillOnCall(
  projection: ReturnType<typeof createWorkspaceProjection>,
  onCall: ReturnType<ReturnType<typeof createWorkspaceProjection>["getSnapshot"]>["selection"],
) {
  const { selection } = projection.getSnapshot()
  assert.equal(selection.contextView, "call")
  assert.equal(selection.contextPanelOpen, true)
  assert.equal(selection.view, "engagement")
  assert.deepEqual(selection.engagement, onCall.engagement)
  assert.equal(selection.task?.id, onCall.task?.id)
}

test("ticking the call's own Task during a call keeps the call view and updates the rail", async () => {
  const { projection, recovery, other, onCall } = await projectionOnCall()
  await projection.dispatch({ type: "complete-task", task: recovery, reason: "DONE" })
  assertStillOnCall(projection, onCall)
  const state = projection.getSnapshot()
  assert.deepEqual(openTasks(state), [other])
  assert.equal(state.patients.counts.all, 1)
  assert.deepEqual(state.completedTasks.items.map((row) => row.id), [recovery.id])
  assert.equal(state.selection.task?.state, "COMPLETED")
  assert.deepEqual(state.completion.pending, [])
  projection.stop()
})

test("closing another Task as Not needed during a call keeps the call view and updates the rail", async () => {
  const { projection, recovery, other, onCall } = await projectionOnCall()
  await projection.dispatch({ type: "complete-task", task: other, reason: "NOT_NEEDED" })
  assertStillOnCall(projection, onCall)
  const state = projection.getSnapshot()
  assert.deepEqual(openTasks(state), [recovery])
  assert.equal(state.patients.counts.all, 1)
  assert.deepEqual(state.completedTasks.items.map((row) => row.id), [other.id])
  projection.stop()
})

test("reopening a Task during a call keeps the call view and updates the rail", async () => {
  const { projection, rows, recovery, other, onCall } = await projectionOnCall()
  await projection.dispatch({ type: "complete-task", task: recovery, reason: "DONE" })
  const reopened: Task = { ...recovery, version: 3 }
  rows.set(reopened.id, reopened)
  await projection.dispatch({ type: "task-committed", task: reopened })
  assertStillOnCall(projection, onCall)
  const state = projection.getSnapshot()
  assert.deepEqual(openTasks(state), [reopened, other])
  assert.equal(state.patients.counts.all, 2)
  assert.deepEqual(state.completedTasks.items, [])
  assert.deepEqual(state.selection.task, reopened)
  projection.stop()
})

test("a phone search opens and closes the patient's Tasks without selecting one", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(31), tasks: taskPage([task("open")]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-search", value: "(555) 987-6543" })
  await projection.dispatch({ type: "submit-search" })
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, false)

  await projection.dispatch({ type: "open-patient-tasks" })
  let { selection } = projection.getSnapshot()
  assert.equal(selection.contextView, "patient-tasks")
  assert.equal(selection.contextPanelOpen, true)
  assert.equal(selection.engagement?.phone, "+15559876543")
  assert.equal(selection.task, undefined)

  await projection.dispatch({ type: "close-context" })
  selection = projection.getSnapshot().selection
  assert.equal(selection.contextPanelOpen, false)
  assert.equal(selection.engagement?.phone, "+15559876543")
  assert.equal(selection.task, undefined)
  projection.stop()
})

test("a text review opens and closes the patient's Tasks and stays the selected review", async () => {
  const realtime = deterministicRealtime()
  const review = task("review", { origin: "INBOUND_MESSAGE_REVIEW", title: "Review inbound text" })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(32), tasks: taskPage([review]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: review })

  await projection.dispatch({ type: "open-patient-tasks" })
  let { selection } = projection.getSnapshot()
  assert.equal(selection.contextView, "patient-tasks")
  assert.equal(selection.contextPanelOpen, true)
  assert.deepEqual(selection.task, review)

  await projection.dispatch({ type: "close-context" })
  selection = projection.getSnapshot().selection
  assert.equal(selection.contextView, "patient-tasks")
  assert.equal(selection.contextPanelOpen, false)
  assert.deepEqual(selection.task, review)
  projection.stop()
})

test("a completion that fails or conflicts refreshes every open Task detail read", async () => {
  for (const failure of [
    { kind: "conflict", retryable: false, status: 409 },
    { kind: "unavailable", retryable: true, status: 503 },
  ] satisfies PortalFailure[]) {
    const realtime = deterministicRealtime()
    const open = task("open")
    const projection = createWorkspaceProjection({
      authority: {
        ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(33), tasks: taskPage([open]) }),
        completeTask: async () => failure,
      },
      realtime: realtime.adapter,
      preferences: memoryPreferences(),
    })
    await projection.start()
    await realtime.reconcile(0)
    const before = projection.getSnapshot().detailRevision
    await projection.dispatch({ type: "complete-task", task: open, reason: "DONE" })
    const state = projection.getSnapshot()
    assert.deepEqual(Object.keys(state.completion.errors), [open.id])
    assert.equal(state.detailRevision, before + 1, `${failure.kind} refetches the reads that showed the stale Task`)
    projection.stop()
  }
})

test("a text review below the patient's top item opens from the patient's Tasks and stays selected", async () => {
  const realtime = deterministicRealtime()
  const phone = "+13055550142"
  const followUp = task("follow-up", { phone })
  const text = task("text", { phone, origin: "INBOUND_MESSAGE_REVIEW" })
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(40), tasks: taskPage([followUp, text]) }),
      patients: async () => success(patientPage([patientRow(followUp, { openCount: 2 })])),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "open-patient-tasks" })
  await projection.dispatch({ type: "select-task", task: text })
  for (const settle of [
    async () => {},
    () => realtime.reconcile(0),
    () => projection.dispatch({ type: "set-rail-view", filter: "callBack" }),
  ]) {
    await settle()
    const { selection } = projection.getSnapshot()
    assert.equal(selection.task?.id, text.id)
    assert.equal(selection.engagement?.phone, phone)
    assert.deepEqual([selection.view, selection.contextView], ["engagement", "task"])
  }
  projection.stop()
})

function advancingProjection(before: PatientWorkRow[], after: PatientWorkRow[], known: Task[]) {
  const realtime = deterministicRealtime()
  const flow = { closed: false }
  const rows = new Map(known.map((item) => [item.id, item]))
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(41), tasks: taskPage([]) }),
      patients: async () => success(patientPage(flow.closed ? after : before)),
      task: async (_token, taskID) => {
        const row = rows.get(taskID)
        return row ? success(row) : missing()
      },
      completeTask: async (_token, target) => {
        const completed: Task = { ...rows.get(target.id)!, state: "COMPLETED", version: target.version + 1 }
        rows.set(completed.id, completed)
        flow.closed = true
        return success(completed)
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  return { projection, realtime, flow, rows }
}

const advancePhones = { a: "+13055550101", x: "+13055550102", y: "+13055550103" }
const advanceTask = (id: string, phone: keyof typeof advancePhones, overrides: Partial<Task> = {}) =>
  task(id, { phone: advancePhones[phone], ...overrides })

for (const [label, before, after, want] of [
  ["stays with the patient while they have open work", ["x-1", "a-1", "y-1"], ["x-1", "a-2", "y-1"], "a-2"],
  ["moves to the row that followed the patient", ["x-1", "a-1", "y-1"], ["x-1", "y-1"], "y-1"],
  ["moves to the previous row when none followed", ["x-1", "a-1"], ["x-1"], "x-1"],
  ["clears the selection when the list is empty", ["a-1"], [], undefined],
] as const) {
  const tasks = new Map(["a-1", "a-2", "x-1", "y-1"].map((id) => [id, advanceTask(id, id[0] as keyof typeof advancePhones)]))
  const rowsFor = (ids: readonly string[]) => ids.map((id) => patientRow(tasks.get(id)!))
  const closed = tasks.get("a-1")!

  test(`Complete and next ${label}`, async () => {
    const { projection, realtime, flow, rows } = advancingProjection(rowsFor(before), rowsFor(after), [...tasks.values()])
    await projection.start()
    await realtime.reconcile(0)
    await projection.dispatch({ type: "select-task", task: closed })
    const completed: Task = { ...closed, state: "COMPLETED", version: 2 }
    rows.set(completed.id, completed)
    flow.closed = true
    await projection.dispatch({ type: "task-committed", task: completed, advance: true })
    assertAdvanced(projection.getSnapshot(), want)
    projection.stop()
  })

  test(`a tick on the selected Task in the patient's Tasks ${label}`, async () => {
    const { projection, realtime } = advancingProjection(rowsFor(before), rowsFor(after), [...tasks.values()])
    await projection.start()
    await realtime.reconcile(0)
    await projection.dispatch({ type: "select-task", task: closed })
    await projection.dispatch({ type: "open-patient-tasks" })
    await projection.dispatch({ type: "complete-task", task: closed, reason: "DONE", advance: true })
    const state = projection.getSnapshot()
    assertAdvanced(state, want)
    assert.equal(
      state.selection.contextView,
      want === "a-2" ? "patient-tasks" : "task",
      "the patient's Task list stays open while the patient has more to tick",
    )
    projection.stop()
  })

  test(`a call wrap-up that closed the selected Task ${label}`, async () => {
    const { projection, realtime, flow, rows } = advancingProjection(rowsFor(before), rowsFor(after), [...tasks.values()])
    await projection.start()
    await realtime.reconcile(0)
    await projection.dispatch({ type: "select-task", task: closed })
    await projection.dispatch({ type: "remember-return-task", taskID: closed.id })
    const call = liveCall({ phone: closed.phone, taskId: closed.id })
    await projection.dispatch({ type: "call-connected", call })
    rows.set(closed.id, { ...closed, state: "COMPLETED", version: 2 })
    flow.closed = true
    await projection.dispatch({ type: "call-disposition", result: { call, closedTaskIds: [closed.id] } })
    assertAdvanced(projection.getSnapshot(), want)
    projection.stop()
  })
}

function assertAdvanced(state: WorkspaceProjectionState, want: string | undefined) {
  assert.equal(state.selection.task?.id, want)
  if (want) {
    assert.equal(state.selection.engagement?.phone, state.selection.task?.phone)
    assert.equal(state.selection.view, "engagement")
    return
  }
  assert.equal(state.selection.engagement, undefined)
  assert.equal(state.selection.view, "none")
  assert.equal(state.selection.contextPanelOpen, false)
}

test("a tick on another Task, or one that is not asked to advance, leaves the selection alone", async () => {
  const tasks = [advanceTask("x-1", "x"), advanceTask("a-1", "a"), advanceTask("y-1", "y")]
  const [x, a] = tasks
  const { projection, realtime } = advancingProjection(tasks.map((item) => patientRow(item)), [patientRow(tasks[2])], tasks)
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: a })
  await projection.dispatch({ type: "complete-task", task: x, reason: "DONE", advance: true })
  assert.equal(projection.getSnapshot().selection.task?.id, a.id)
  await projection.dispatch({ type: "complete-task", task: a, reason: "DONE" })
  assert.equal(projection.getSnapshot().selection.task?.id, a.id)
  assert.equal(projection.getSnapshot().selection.task?.state, "COMPLETED")
  projection.stop()
})

test("a call wrap-up that closed another patient's Task returns to the Task the staff member left", async () => {
  const tasks = [advanceTask("x-1", "x"), advanceTask("y-1", "y"), advanceTask("a-1", "a")]
  const [x, y, a] = tasks
  const { projection, realtime, flow } = advancingProjection(tasks.map((item) => patientRow(item)), [patientRow(y), patientRow(a)], tasks)
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: a })
  await projection.dispatch({ type: "remember-return-task", taskID: a.id })
  const call = liveCall({ phone: x.phone })
  await projection.dispatch({ type: "call-connected", call })
  flow.closed = true
  await projection.dispatch({ type: "call-disposition", result: { call, closedTaskIds: [x.id] } })
  assert.equal(projection.getSnapshot().selection.task?.id, a.id)
  projection.stop()
})

test("ticks save independently and each failure stays on its own Task", async () => {
  const realtime = deterministicRealtime()
  const first = task("first")
  const second = task("second")
  const third = task("third")
  const saves = new Map([[first.id, deferred<WorkspaceAuthorityResult<Task>>()], [second.id, deferred<WorkspaceAuthorityResult<Task>>()], [third.id, deferred<WorkspaceAuthorityResult<Task>>()]])
  const requested: string[] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(42), tasks: taskPage([first, second, third]) }),
      completeTask: async (_token, target) => {
        requested.push(target.id)
        return saves.get(target.id)!.promise
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  const pending = [first, second, third, first].map((item) =>
    projection.dispatch({ type: "complete-task", task: item, reason: "DONE" }))
  await waitUntil(() => requested.length === 3)
  assert.deepEqual(requested, [first.id, second.id, third.id], "each Task is sent once while the others are still saving")
  assert.deepEqual(projection.getSnapshot().completion.pending, [first.id, second.id, third.id])

  saves.get(second.id)!.resolve({ kind: "conflict", retryable: false, status: 409 })
  await pending[1]
  assert.deepEqual(projection.getSnapshot().completion.pending, [first.id, third.id])
  saves.get(third.id)!.resolve(unavailable())
  saves.get(first.id)!.resolve(success({ ...first, state: "COMPLETED", version: 2 }))
  await Promise.all(pending)
  const settled = projection.getSnapshot().completion
  assert.deepEqual(settled.pending, [])
  assert.deepEqual(Object.keys(settled.errors).sort(), [second.id, third.id].sort())
  assert.match(settled.errors[second.id], /changed elsewhere/)

  saves.set(second.id, deferred<WorkspaceAuthorityResult<Task>>())
  const retry = projection.dispatch({ type: "complete-task", task: second, reason: "DONE" })
  await waitUntil(() => requested.length === 4)
  assert.deepEqual(Object.keys(projection.getSnapshot().completion.errors), [third.id], "a retry clears only its own failure")
  saves.get(second.id)!.resolve(success({ ...second, state: "COMPLETED", version: 2 }))
  await retry
  projection.stop()
})

for (const [label, stored, counts, restored] of [
  ["No owner with nothing unowned falls back to All", "unowned", { unowned: 0 }, "all"],
  ["No owner with unowned work is kept", "unowned", { unowned: 2 }, "unowned"],
  ["Appt checks where none are offered or waiting falls back to All", "appointments", { appointments: 0 }, "all"],
  ["Appt checks with a check waiting is kept", "appointments", { appointments: 1 }, "appointments"],
  ["Call back with nothing to call back is kept", "callBack", { callBack: 0 }, "callBack"],
] as const) {
  test(`on reload a stored ${label}`, async () => {
    const key = "acuity.attentionRail.user-1.practice-1"
    const values = new Map([[key, JSON.stringify({ version: 2, scope: "mine", filter: stored, completedExpanded: false, scrollTop: 0 })]])
    const realtime = deterministicRealtime()
    const requests: string[] = []
    const projection = createWorkspaceProjection({
      authority: {
        ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(43), tasks: taskPage([]) }),
        patients: async (_token, request) => {
          requests.push(request.filter ?? "")
          const page = patientPage(request.filter === restored ? [patientRow(task(`shown-under-${restored}`))] : [])
          return success({ ...page, counts: { ...page.counts, all: 1, ...counts } })
        },
      },
      realtime: realtime.adapter,
      preferences: { read: (name: string) => values.get(name) ?? null, write: (name: string, value: string) => values.set(name, value) },
    })
    await projection.start()
    await realtime.reconcile(0)
    const state = projection.getSnapshot()
    assert.equal(state.rail.filter, restored)
    assert.equal(JSON.parse(values.get(key) ?? "").filter, restored)
    assert.deepEqual(openTasks(state).map((item) => item.id), [`shown-under-${restored}`])
    assert.equal(state.selection.task?.id, `shown-under-${restored}`, "the first load still opens the first patient")
    assert.deepEqual(requests, restored === stored ? [stored] : [stored, "all"])

    await projection.dispatch({ type: "set-rail-view", filter: "unowned" })
    await realtime.reconcile(0)
    assert.equal(projection.getSnapshot().rail.filter, "unowned", "a chip the staff member picks is never replaced")
    projection.stop()
  })
}

test("a search that hides every unowned patient does not replace the stored No owner filter", async () => {
  const realtime = deterministicRealtime()
  const requests: string[] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(44), tasks: taskPage([]) }),
      patients: async (_token, request) => {
        requests.push(`${request.filter}:${request.search ?? ""}`)
        const page = patientPage([])
        return success({ ...page, counts: { ...page.counts, unowned: request.search ? 0 : 3 } })
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-rail-view", filter: "unowned" })
  await projection.dispatch({ type: "set-search", value: "nobody" })
  await projection.dispatch({ type: "submit-search" })
  requests.length = 0
  await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().rail.filter, "unowned")
  assert.deepEqual([...new Set(requests)], ["unowned:nobody"])
  projection.stop()
})
