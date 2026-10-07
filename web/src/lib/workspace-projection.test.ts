import assert from "node:assert/strict"
import test from "node:test"

import { createWorkspaceSync } from "./workspace-sync/workspace-sync.ts"
import type { WorkspaceLocation } from "./workspace-location.ts"

import type {
  AccessDiscovery,
  AiInteractionDetail,
  Location,
  Practice,
  Task,
  TaskChangeQueryRequest,
  TaskPage,
  WorkspaceSnapshot,
} from "./api/generated/types.gen.ts"
import {
  createWorkspaceProjection,
  type WorkspaceAuthorityAdapter,
  type WorkspaceAuthorityResult,
  type WorkspaceRealtimeAdapter,
  type WorkspaceRealtimeCallbacks,
  WorkspaceProjectionAccessError,
} from "./workspace-projection.ts"

test("initial workspace shows a conversation without opening Task details", async () => {
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
  await projection.dispatch({ type: "select-task", task: first })
  assert.equal(projection.getSnapshot().selection.contextPanelOpen, true)
  projection.stop()
})

test("returning to work restores the selected conversation after another view", async () => {
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
  await projection.dispatch({ type: "select-manage-agent" })
  assert.equal(projection.getSnapshot().selection.view, "manage-agent")
  await projection.dispatch({ type: "select-work" })
  assert.equal(projection.getSnapshot().selection.view, "engagement")
  assert.equal(projection.getSnapshot().selection.engagement?.phone, first.phone)
  projection.stop()
})

test("returning to work without a conversation shows the empty workspace", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await projection.dispatch({ type: "select-manage-agent" })
  await projection.dispatch({ type: "select-work" })
  assert.equal(projection.getSnapshot().selection.view, "none")
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
  assert.deepEqual(state.tasks.items, [])
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
  const authority: WorkspaceAuthorityAdapter = {
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
  await projection.dispatch({ type: "load-more", window: "tasks" })
  await projection.dispatch({ type: "load-more", window: "completedTasks" })
  for (let refresh = 0; refresh < 2; refresh += 1) {
    assert.deepEqual(projection.getSnapshot().tasks.items.map((item) => item.id), ["open-1", "open-2"])
    assert.deepEqual(projection.getSnapshot().completedTasks.items.map((item) => item.id), ["done-1", "done-2"])
    await realtime.reconcile(0)
  }
  projection.stop()
})

test("a failed reconcile during load-more still lands the requested page and releases loading", async () => {
  const { projection, realtime, control, delayed, openIDs } = await startWithDelayableTasks()
  control.delayNext = true
  const load = projection.dispatch({ type: "load-more", window: "tasks" })
  assert.equal(projection.getSnapshot().tasks.loading, true)
  await waitUntil(() => !control.delayNext)
  control.workspaceAvailable = false
  await assert.rejects(realtime.reconcile(0), /workspace authority is unavailable/)
  assert.equal(projection.getSnapshot().tasks.loading, true)
  delayed.resolve(success(taskPage([task("open-2")])))
  await load

  assert.equal(projection.getSnapshot().tasks.loading, false)
  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  projection.stop()
})

test("a reconcile during load-more keeps the page the operator asked for", async () => {
  const { projection, realtime, control, delayed, openIDs } = await startWithDelayableTasks()
  control.delayNext = true
  const load = projection.dispatch({ type: "load-more", window: "tasks" })
  await waitUntil(() => !control.delayNext)
  await realtime.reconcile(0)
  assert.deepEqual(openIDs(), ["open-1"])
  assert.equal(projection.getSnapshot().tasks.loading, true)
  delayed.resolve(success(taskPage([task("open-2")])))
  await load

  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  assert.equal(projection.getSnapshot().tasks.loading, false)
  await realtime.reconcile(0)
  assert.deepEqual(openIDs(), ["open-1", "open-2"])
  projection.stop()
})

test("a superseded Task refresh releases loading when the reconcile that replaced it fails", async () => {
  const { projection, realtime, control, delayed, openIDs } = await startWithDelayableTasks()
  control.delayNext = true
  const refresh = projection.dispatch({ type: "refresh-text-attention" })
  assert.equal(projection.getSnapshot().tasks.loading, true)
  await waitUntil(() => !control.delayNext)
  control.workspaceAvailable = false
  await assert.rejects(realtime.reconcile(0), /workspace authority is unavailable/)
  delayed.resolve(success(taskPage([task("superseded-refresh")])))
  await refresh

  assert.equal(projection.getSnapshot().tasks.loading, false)
  assert.equal(projection.getSnapshot().completedTasks.loading, false)
  assert.deepEqual(openIDs(), ["open-1"])
  projection.stop()
})

async function startWithDelayableTasks() {
  const realtime = deterministicRealtime()
  const delayed = deferred<WorkspaceAuthorityResult<TaskPage>>()
  const control = { delayNext: false, workspaceAvailable: true }
  const authority: WorkspaceAuthorityAdapter = {
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
  const openIDs = () => projection.getSnapshot().tasks.items.map((item) => item.id)
  return { projection, realtime, control, delayed, openIDs }
}

test("changing scope, failing closed, and stopping abort in-flight authority requests", async () => {
  const realtime = deterministicRealtime()
  const signals: AbortSignal[] = []
  let workspaceResult: WorkspaceAuthorityResult<WorkspaceSnapshot> = success(workspaceSnapshot(7))
  const authority: WorkspaceAuthorityAdapter = {
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
  const authority: WorkspaceAuthorityAdapter = {
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
  assert.equal(state.tasks.items[0]?.version, 3)
  assert.equal(state.tasks.items[0]?.title, "Authoritative detail")
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
  assert.deepEqual(state.tasks.items, [])
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
  const authority: WorkspaceAuthorityAdapter = {
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
  assert.deepEqual(state.tasks.items.map((item) => item.id), ["second-task"])
  assert.deepEqual(state.completedTasks.items, [])
  assert.equal(state.selection.task?.id, "initial-task")
  projection.stop()
})

test("confirmed completion moves a Task to shared completed history and preserves selected evidence", async () => {
  const realtime = deterministicRealtime()
  const openTask = task("task-1", { category: "insurance" })
  const completedTask = task("task-1", { category: "insurance", state: "COMPLETED", version: 2, completedAt: "2026-08-30T12:10:00Z", completedBy: { kind: "HUMAN", subject: "other-staff" } })
  let completed = false
  let taskReads = 0
  const authority: WorkspaceAuthorityAdapter = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(11), tasks: taskPage([openTask]) }),
    tasks: async (_token, request) => {
      taskReads += 1
      return success(taskPage(request.state === "OPEN" ? (completed ? [] : [openTask]) : (completed ? [completedTask] : [])))
    },
    completeTask: async () => { completed = true; return success(completedTask) },
    task: async () => success(completed ? completedTask : openTask),
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: openTask })
  const staleSnapshot = await realtime.prepareReconciliation(0)
  taskReads = 0
  await projection.dispatch({ type: "complete-task", task: openTask })
  assert.equal(taskReads, 2, "one authoritative read per Task window")
  staleSnapshot.apply()
  const state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items, [])
  assert.equal(state.tasks.counts.tasks, 0)
  assert.deepEqual(state.completedTasks.items, [completedTask])
  assert.deepEqual(state.selection.task, completedTask)
  assert.equal(state.selection.contextPanelOpen, true)
  assert.equal(state.completion.pendingTaskID, "")
  assert.equal(realtime.refreshes, 1)
  projection.stop()
})
test("temporary token failure keeps Task completion retryable without expiring the session", async () => {
  const realtime = deterministicRealtime()
  let tokenAvailable = true
  const openTask = task("task-1")
  const authority: WorkspaceAuthorityAdapter = {
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
  await projection.dispatch({ type: "complete-task", task: openTask })

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "ready")
  assert.equal(
    state.completion.error,
    "Task completion is temporarily unavailable. Retry in a moment.",
  )
  assert.equal(state.completion.errorTaskID, openTask.id)

  tokenAvailable = true
  await projection.dispatch({ type: "complete-task", task: openTask })
  assert.equal(
    projection.getSnapshot().completion.error,
    "This Task could not be completed. Retry from the row or open its details.",
  )
  projection.stop()
})

test("one Task count query serves active Tasks and pagination preserves those counts", async () => {
  const realtime = deterministicRealtime()
  const requests: Array<{ state?: string; cursor?: string; includeCounts?: boolean }> = []
  const counts = taskPage(Array.from({ length: 6 }, (_, i) => task(`task-${i}`))).counts
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(13), tasks: taskPage([]) }),
      tasks: async (_token, request) => {
        requests.push(request)
        const page = request.cursor
          ? { items: [task("task-2")], nextCursor: "" }
          : { items: [task("task-1")], nextCursor: "more" }
        return success({ ...page, ...(request.includeCounts !== false ? { counts } : {}) })
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.deepEqual(requests.map((request) => request.includeCounts), [true, false])
  await projection.dispatch({ type: "load-more", window: "tasks" })
  assert.equal(requests.at(-1)?.includeCounts, false)
  assert.deepEqual(projection.getSnapshot().tasks.counts, counts)
  requests.length = 0
  await realtime.reconcile(0)
  assert.equal(requests.filter((request) => request.includeCounts !== false).length, 1)
  assert.deepEqual(projection.getSnapshot().tasks.counts, counts)
  projection.stop()
})

test("rail preferences restore through the projection and corrupted values fail safe", async () => {
  const values = new Map<string, string>([
    [
      "acuity.attentionRail.user-1.practice-1",
      JSON.stringify({
        scrollTop: "not-a-number",
        taskCategory: "obsolete",
        expanded: ["unknown"],
      }),
    ],
  ])
  const preferences = {
    read: (key: string) => values.get(key) ?? null,
    write: (key: string, value: string) => values.set(key, value),
  }
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(14),
      tasks: taskPage([]),
    }),
    realtime: deterministicRealtime().adapter,
    preferences,
  })

  await projection.start()
  assert.deepEqual(projection.getSnapshot().rail, {
    expanded: ["tasks"],
    taskCategory: "all",
    scrollTop: 0,
  })

  await projection.dispatch({ type: "toggle-rail-section", section: "completed" })
  await projection.dispatch({ type: "set-task-category", category: "billing" })
  await projection.dispatch({ type: "remember-rail-scroll", scrollTop: 42 })

  assert.deepEqual(
    JSON.parse(values.get("acuity.attentionRail.user-1.practice-1") ?? ""),
    {
      version: 1,
      expanded: ["tasks", "completed"],
      taskCategory: "billing",
      scrollTop: 42,
    },
  )
  projection.stop()
})

test("retired Billing preference restores the visible All types filter", async () => {
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(14),
      tasks: taskPage([]),
    }),
    realtime: deterministicRealtime().adapter,
    preferences: {
      read: (key) => key.startsWith("acuity.attentionRail.")
        ? JSON.stringify({ version: 1, taskCategory: "billing", scrollTop: 0 })
        : null,
      write: () => {},
    },
  })
  await projection.start()
  assert.equal(projection.getSnapshot().rail.taskCategory, "all")
  projection.stop()
})

test("switching responsibility views refreshes both Task windows and preserves the selected type", async () => {
  const realtime = deterministicRealtime()
  const requests: Parameters<WorkspaceAuthorityAdapter["tasks"]>[1][] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(14), tasks: taskPage([]) }),
      tasks: async (_token, request) => {
        requests.push(request)
        const id = `${request.responsibility}-${request.state}`
        return success(taskPage([task(id, { state: request.state, category: request.category })]))
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "set-task-category", category: "optical" })
  for (const responsibility of ["all", "mine"] as const) {
    const stale = await realtime.prepareReconciliation(0)
    requests.length = 0
    await projection.dispatch({ type: "set-task-filters", responsibility })
    stale.apply()
    const state = projection.getSnapshot()
    assert.equal(state.rail.taskResponsibility, responsibility)
    assert.equal(state.rail.taskCategory, "optical")
    assert.deepEqual(requests.map((request) => [request.state, request.responsibility, request.category]), [
      ["OPEN", responsibility, "optical"],
      ["COMPLETED", responsibility, "optical"],
    ])
    assert.deepEqual(state.tasks.items.map((item) => item.id), [`${responsibility}-OPEN`])
    assert.deepEqual(state.completedTasks.items.map((item) => item.id), [`${responsibility}-COMPLETED`])
  }
  const requestCount = requests.length
  await projection.dispatch({ type: "toggle-rail-section", section: "completed" })
  assert.equal(requests.length, requestCount, "expanding the shelf uses the current authoritative window")
  assert.ok(projection.getSnapshot().rail.expanded.includes("completed"))
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
  assert.deepEqual(state.tasks.items, [])
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

test("authoritative recovery detail updates its rail row and selection together", async () => {
  const realtime = deterministicRealtime()
  const recoveryV2 = task("recovery-1", {
    origin: "MISSED_CALL_RECOVERY",
    version: 2,
    title: "Recovery row",
  })
  const recoveryV3 = task("recovery-1", {
    origin: "MISSED_CALL_RECOVERY",
    version: 3,
    title: "Authoritative recovery detail",
  })
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(18),
    tasks: taskPage([]),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...baseAuthority,
      tasks: async (_token, request) => success(
          request.state === "OPEN"
            ? taskPage([recoveryV2])
            : taskPage([]),
      ),
      task: async () => success(recoveryV3),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "select-task", task: recoveryV2 })
  await realtime.reconcile(0)

  const state = projection.getSnapshot()
  assert.equal(state.tasks.items[0]?.version, 3)
  assert.equal(state.tasks.items[0]?.title, "Authoritative recovery detail")
  assert.equal(state.selection.task?.version, 3)
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
}): WorkspaceAuthorityAdapter {
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

function missing(): { kind: "missing" } {
  return { kind: "missing" }
}

function unavailable(): { kind: "unavailable" } {
  return { kind: "unavailable" }
}

function unauthorizedResult(): { kind: "unauthorized" } {
  return { kind: "unauthorized" }
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
        callingEnabled: true,
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
    assert.equal(projection.getSnapshot().selection.agentPage, "transcripts")
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

function task(id: string, overrides: Partial<Task> = {}): Task {
  return {
    id,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    phone: "+15551234567",
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

test("requested Task counts cannot silently become zero when omitted", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(13), tasks: taskPage([]) }),
      tasks: async () => success({ items: [], nextCursor: "" }),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.ok(projection.getSnapshot().tasks.error, "missing authoritative counts must be visible")
  projection.stop()
})

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
  assert.deepEqual(projection.getSnapshot().tasks.counts, currentPage.counts)
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
    const committedCounts = projection.getSnapshot().tasks.counts
    assert.equal(committedCounts.tasks, type === "task-created" ? 2 : 1)
    pending.apply()
    assert.deepEqual(projection.getSnapshot().tasks.counts, committedCounts)
    assert.equal(realtime.refreshes, 1)
    projection.stop()
  })
}

test("selected Task detail refresh cannot discard the authoritative grouped membership", async () => {
  const realtime = deterministicRealtime()
  const first=task("group-first")
  const second=task("group-second")
  const grouped={...first,groupMembers:[first,second]}
  const projection=createWorkspaceProjection({
    authority:{...deterministicAuthority({discovery:accessDiscovery(),snapshot:workspaceSnapshot(20),tasks:taskPage([grouped])}),task:async()=>success(first)},
    realtime:realtime.adapter,preferences:memoryPreferences(),
  })
  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({type:"select-task",task:grouped})
  await realtime.reconcile(0)
  assert.deepEqual(projection.getSnapshot().tasks.items[0]?.groupMembers?.map((member)=>member.id),["group-first","group-second"])
  assert.equal(projection.getSnapshot().selection.taskGroup, grouped)
  await projection.dispatch({type:"select-task",task:second})
  assert.equal(projection.getSnapshot().selection.taskGroup, undefined)
  projection.stop()
})

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
    const before = projection.getSnapshot().tasks
    await projection.dispatch({ type: "open-task-context", task: task("activity-open-unflagged") })
    assert.deepEqual(projection.getSnapshot().tasks.items, before.items)
    assert.deepEqual(projection.getSnapshot().tasks.counts, before.counts)
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
  const pending = projection.dispatch({ type: "complete-task", task: open })
  assert.equal(projection.getSnapshot().completion.pendingTaskID, open.id)
  assert.deepEqual(projection.getSnapshot().tasks.items, [open])
  assert.deepEqual(projection.getSnapshot().completedTasks.items, [])
  failLists = true
  committed.resolve(success(completed))
  await pending
  assert.deepEqual(projection.getSnapshot().selection.task, completed)
  assert.deepEqual(projection.getSnapshot().tasks.items, [open], "failed list read cannot establish updated membership")
  assert.ok(projection.getSnapshot().tasks.error)
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
  assert.deepEqual(projection.getSnapshot().tasks.items, [open])
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
  const requests: Parameters<WorkspaceAuthorityAdapter["tasks"]>[1][] = []
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(22), tasks: taskPage([first, next]) }),
      tasks: async (_token, request) => {
        requests.push(request)
        return success(taskPage(request.state === "COMPLETED" ? (done ? [completed] : []) : done ? [next] : [first, next]))
      },
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
  assert.ok(requests.filter((request) => request.state === "OPEN").every((request) => request.ordering === "recent"))
  projection.stop()
})

test("text attention expiry refreshes only Task windows and preserves selected context", async () => {
  const realtime = deterministicRealtime()
  const open = task("task-1")
  const base = deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(4), tasks: taskPage([open]) })
  let taskReads = 0
  let workspaceReads = 0
  let expired = false
  const projection = createWorkspaceProjection({
    authority: {
      ...base,
      workspace: async (...args) => { workspaceReads += 1; return base.workspace(...args) },
      tasks: async (_token, request) => {
        taskReads += 1
        return success(taskPage(expired || request.state === "COMPLETED" ? [] : [open]))
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })
  await projection.dispatch({ type: "refresh-text-attention" })
  assert.equal(taskReads, 0)
  await projection.start()
  await realtime.reconcile(0)
  const selected = projection.getSnapshot().selection.task?.id
  taskReads = 0
  workspaceReads = 0
  expired = true
  await projection.dispatch({ type: "refresh-text-attention" })
  assert.equal(taskReads, 2)
  assert.equal(workspaceReads, 0)
  assert.equal(realtime.refreshes, 0)
  assert.deepEqual(projection.getSnapshot().tasks.items, [])
  assert.equal(projection.getSnapshot().tasks.counts.tasks, 0)
  assert.equal(projection.getSnapshot().loadState, "ready")
  assert.equal(projection.getSnapshot().selection.task?.id, selected)
  projection.stop()
})

test("a former Texts selection becomes a persistent independent folder", async () => {
  const values = new Map([["acuity.attentionRail.user-1.practice-1", JSON.stringify({ version: 1, expanded: [], taskCategory: "texts", scrollTop: 0 })]])
  const preferences = { read: (key: string) => values.get(key) ?? null, write: (key: string, value: string) => values.set(key, value) }
  const options = { authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(4), tasks: taskPage([]) }), realtime: deterministicRealtime().adapter, preferences }
  const projection = createWorkspaceProjection(options)
  await projection.start()
  assert.deepEqual(projection.getSnapshot().rail.expanded, ["texts"])
  assert.equal(projection.getSnapshot().rail.taskCategory, "all")
  await projection.dispatch({ type: "toggle-rail-section", section: "texts" })
  projection.stop()
  const restored = createWorkspaceProjection({ ...options, realtime: deterministicRealtime().adapter })
  await restored.start()
  assert.deepEqual(restored.getSnapshot().rail.expanded, [])
  restored.stop()
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
    const authority: WorkspaceAuthorityAdapter = {
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

test("refresh reopens the Task named in the URL instead of the first Task", async () => {
  const realtime = deterministicRealtime()
  const first = task("first-task")
  const second = task("second-task", { phone: "+15557654321" })
  const navigation = memoryNavigation({ view: "task", taskID: second.id })
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([first, second]) }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation,
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, second.id)
  assert.equal(projection.getSnapshot().selection.engagement?.phone, second.phone)
  assert.deepEqual(navigation.writes, [])
  await projection.dispatch({ type: "select-task", task: first })
  assert.deepEqual(navigation.writes, [{ location: { view: "task", taskID: first.id }, mode: "push" }])
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

test("restored Task keeps its group once the Task window arrives", async () => {
  const realtime = deterministicRealtime()
  const member = task("member-task")
  const anchor = task("anchor-task", { groupMembers: [task("anchor-task"), member] })
  const base = deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(1), tasks: taskPage([anchor]) })
  const projection = createWorkspaceProjection({
    authority: { ...base, task: async (_token, taskID) => taskID === anchor.id ? success(task(anchor.id)) : missing() },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    navigation: memoryNavigation({ view: "task", taskID: anchor.id }),
  })
  await projection.start()
  assert.equal(projection.getSnapshot().selection.taskGroup, undefined)
  await realtime.reconcile(0)
  assert.equal(projection.getSnapshot().selection.task?.id, anchor.id)
  assert.deepEqual(projection.getSnapshot().selection.taskGroup?.groupMembers?.map((item) => item.id), [anchor.id, member.id])
  projection.stop()
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
    { location: { view: "manage-agent" }, mode: "push" },
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
  assert.equal(projection.getSnapshot().selection.agentPage, "transcripts")
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

function memoryNavigation(initial: WorkspaceLocation = { view: "none" }) {
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

function liveWorkspace({ openRows, hidden = () => false, environment, feed = false }: {
  openRows: number
  hidden?: () => boolean
  environment?: Parameters<typeof createWorkspaceProjection>[0]["environment"]
  feed?: boolean
}) {
  const origin = Date.UTC(2026, 7, 30, 12)
  const server = {
    version: 1,
    open: Array.from({ length: openRows }, (_, index) =>
      task(`open-${String(index).padStart(3, "0")}`, {
        updatedAt: new Date(origin - index * 60_000).toISOString(),
        ...(feed ? { phone: `+1555000${String(index).padStart(4, "0")}` } : {}),
      })),
    completed: [] as Task[],
    changes: [] as Array<{ version: number; ids: string[] | null }>,
    feedAvailable: true,
    commit(ids: string[] | null) {
      server.version += 1
      server.changes.push({ version: server.version, ids })
    },
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
  const authority: WorkspaceAuthorityAdapter = {
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
      server.commit([target.id])
      return success(done)
    },
    ...(feed ? {
      taskChanges: async (_token: string, request: TaskChangeQueryRequest) => {
        requests.push(`changes:${request.sinceVersion}`)
        if (!server.feedAvailable) return unavailable()
        const range = server.changes.filter((change) => change.version > request.sinceVersion)
        const ids = new Set(range.flatMap((change) => change.ids ?? []))
        const incomplete = range.length !== server.version - request.sinceVersion || range.some((change) => !change.ids)
        const tasks = [...server.open, ...server.completed].filter((item) => ids.has(item.id))
        const families = new Set(tasks.map((item) => `${item.locationId}:${item.phone}`))
        return success(incomplete
          ? { version: server.version, complete: false, tasks: [], openTasks: [], completedTasks: [] }
          : {
              version: server.version,
              complete: true,
              tasks,
              openTasks: server.open.filter((item) => families.has(`${item.locationId}:${item.phone}`)),
              completedTasks: server.completed.filter((item) => ids.has(item.id)),
              counts: taskPage(server.open).counts,
            })
      },
    } : {}),
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
      while (projection.getSnapshot().tasks.items.length < count) {
        await projection.dispatch({ type: "load-more", window: "tasks" })
      }
    },
    reconciles: () => requests.filter((request) => request === "workspace").length,
  }
}

function manualClock() {
  const clock = { now: 0, timers: new Map<number, () => void>(), next: 0 }
  return {
    clock,
    environment: {
      clock: {
        now: () => clock.now,
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

async function liveWorkspaceWith300OpenRows() {
  const manual = manualClock()
  const live = liveWorkspace({ openRows: 320, environment: manual.environment })
  await live.connect()
  await live.loadOpenRows(300)
  live.requests.length = 0
  return { live, manual }
}

test("one hint with 300 loaded open Tasks refreshes only the first pages and keeps deeper rows", async () => {
  const { live, manual } = await liveWorkspaceWith300OpenRows()
  const selectedID = live.projection.getSnapshot().selection.task?.id
  live.server.open = [task("new-top", { updatedAt: "2026-08-30T13:00:00Z" }), ...live.server.open]
  live.server.version = 2
  manual.clock.now += 1_000
  live.send("hint", 2)
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === 2)
  await live.settle()
  assert.deepEqual(live.requests.toSorted(), [`task:${selectedID}`, "tasks:COMPLETED:", "tasks:OPEN:", "workspace"])
  const ids = live.projection.getSnapshot().tasks.items.map((item) => item.id)
  assert.equal(ids.length, 301)
  assert.equal(new Set(ids).size, 301)
  assert.equal(ids[0], "new-top")
  assert.equal(ids.at(-1), "open-299")

  live.requests.length = 0
  manual.clock.now += 60_000
  manual.fireAll()
  await waitUntil(() => live.reconciles() === 1)
  await live.settle()
  assert.equal(live.requests.filter((request) => request.startsWith("tasks:OPEN")).length, 7)
  assert.equal(live.projection.getSnapshot().tasks.items.length, 321)
  live.projection.stop()
})

test("a load-more overtaken by a refresh retries from the new cursor instead of skipping a pushed-down Task", async () => {
  const live = liveWorkspace({ openRows: 60 })
  await live.connect()
  let release!: () => void
  live.server.pageGate = new Promise<void>((resolve) => { release = resolve })
  const load = live.projection.dispatch({ type: "load-more", window: "tasks" })
  await waitUntil(() => live.requests.includes("tasks:OPEN:open-049"))
  live.server.pageGate = undefined
  live.server.open = [task("new-top", { updatedAt: "2026-08-30T13:00:00Z" }), ...live.server.open]
  live.server.version = 2
  live.send("hint", 2)
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === 2)
  release()
  await load
  assert.equal(live.projection.getSnapshot().tasks.loading, false)
  assert.equal(live.requests.filter((request) => request.startsWith("tasks:OPEN:open-04")).length, 2)
  const ids = live.projection.getSnapshot().tasks.items.map((item) => item.id)
  assert.equal(ids.length, 61)
  assert.equal(new Set(ids).size, 61)
  assert.ok(ids.includes("open-049"))
  live.projection.stop()
})

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
  assert.equal(live.requests.length, 4)
  live.projection.stop()
})

test("a burst of hints during an in-flight reconcile runs at most one follow-up", async () => {
  const live = liveWorkspace({ openRows: 60 })
  await live.connect()
  live.requests.length = 0
  let release!: () => void
  live.server.workspaceGate = new Promise<void>((resolve) => { release = resolve })
  live.server.version = 2
  live.send("hint", 2)
  await waitUntil(() => live.reconciles() === 1)
  live.server.workspaceGate = undefined
  for (let version = 3; version <= 12; version += 1) {
    live.server.version = version
    live.send("hint", version)
  }
  await live.settle()
  release()
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === 12)
  await live.settle()
  assert.equal(live.reconciles(), 2)
  live.projection.stop()
})

test("completing a Task refreshes once and skips the server hint for the same version", async () => {
  const manual = manualClock()
  const live = liveWorkspace({ openRows: 3, environment: manual.environment })
  await live.connect()
  const first = live.projection.getSnapshot().tasks.items[0]
  await live.projection.dispatch({ type: "select-task", task: first })
  manual.fireAll()
  live.requests.length = 0
  await live.projection.dispatch({ type: "complete-task", task: first })
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

async function liveWorkspaceWithFeed(openRows = 320, loaded = 300) {
  const manual = manualClock()
  const live = liveWorkspace({ openRows, environment: manual.environment, feed: true })
  await live.connect()
  await live.loadOpenRows(loaded)
  live.requests.length = 0
  return { live, manual }
}

async function hintAndSettle(live: ReturnType<typeof liveWorkspace>) {
  live.send("hint", live.server.version)
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === live.server.version)
  await live.settle()
}

test("with the Task change feed one hint costs the snapshot and one change request and patches deep rows", async () => {
  const { live, manual } = await liveWorkspaceWithFeed()
  const deep = live.server.open[250]!
  live.server.open[250] = { ...deep, title: "Renamed deep Task", version: deep.version + 1 }
  live.server.commit([deep.id])
  manual.clock.now += 1_000
  await hintAndSettle(live)
  assert.deepEqual(live.requests.toSorted(), ["changes:1", "workspace"])
  let items = live.projection.getSnapshot().tasks.items
  assert.equal(items.length, 300)
  assert.equal(new Set(items.map((item) => item.id)).size, 300)
  assert.equal(items[250]?.id, deep.id)
  assert.equal(items[250]?.title, "Renamed deep Task")

  live.requests.length = 0
  const finished = live.server.open[10]!
  const finishedAt = "2026-08-30T13:30:00Z"
  const top = task("new-top", { updatedAt: "2026-08-30T13:00:00Z", phone: "+15559990000" })
  live.server.open = [top, ...live.server.open.filter((item) => item.id !== finished.id)]
  live.server.completed = [{ ...finished, state: "COMPLETED", completedAt: finishedAt, updatedAt: finishedAt, version: finished.version + 1 }]
  live.server.commit([top.id, finished.id])
  await hintAndSettle(live)
  assert.deepEqual(live.requests.toSorted(), ["changes:2", "workspace"])
  const snapshot = live.projection.getSnapshot()
  items = snapshot.tasks.items
  assert.equal(items[0]?.id, "new-top")
  assert.ok(!items.some((item) => item.id === finished.id))
  assert.equal(items.length, 300)
  assert.deepEqual(snapshot.completedTasks.items.map((item) => item.id), [finished.id])
  assert.equal(snapshot.tasks.counts.tasks, live.server.open.length)
  live.projection.stop()
})

test("a feed change below the loaded window is left for load-more instead of being inserted", async () => {
  const { live } = await liveWorkspaceWithFeed()
  const unloaded = live.server.open[310]!
  live.server.open[310] = { ...unloaded, title: "Unloaded rename", version: unloaded.version + 1 }
  live.server.commit([unloaded.id])
  await hintAndSettle(live)
  const items = live.projection.getSnapshot().tasks.items
  assert.equal(items.length, 300)
  assert.ok(!items.some((item) => item.id === unloaded.id))
  await live.projection.dispatch({ type: "load-more", window: "tasks" })
  assert.equal(live.projection.getSnapshot().tasks.items.find((item) => item.id === unloaded.id)?.title, "Unloaded rename")
  live.projection.stop()
})

test("a changed selected Task is re-read once and an unchanged one is not", async () => {
  const { live } = await liveWorkspaceWithFeed(60, 50)
  const selected = live.projection.getSnapshot().selection.task!
  const other = live.server.open[5]!
  live.server.open[5] = { ...other, title: "Other rename", version: other.version + 1 }
  live.server.commit([other.id])
  await hintAndSettle(live)
  assert.deepEqual(live.requests.toSorted(), ["changes:1", "workspace"])
  live.requests.length = 0
  live.server.open[0] = { ...live.server.open[0]!, title: "Selected rename", version: selected.version + 1 }
  live.server.commit([selected.id])
  await hintAndSettle(live)
  assert.deepEqual(live.requests.toSorted(), ["changes:2", `task:${selected.id}`, "workspace"])
  assert.equal(live.projection.getSnapshot().selection.task?.title, "Selected rename")
  live.projection.stop()
})

for (const fallback of ["undeclared", "unavailable"] as const) {
  test(`an ${fallback} change feed falls back to the first-page reconcile`, async () => {
    const { live } = await liveWorkspaceWithFeed(60, 50)
    const selectedID = live.projection.getSnapshot().selection.task?.id
    live.server.open = [task("new-top", { updatedAt: "2026-08-30T13:00:00Z", phone: "+15559990000" }), ...live.server.open]
    if (fallback === "unavailable") live.server.feedAvailable = false
    live.server.commit(fallback === "undeclared" ? null : ["new-top"])
    await hintAndSettle(live)
    assert.deepEqual(live.requests.toSorted(), ["changes:1", `task:${selectedID}`, "tasks:COMPLETED:", "tasks:OPEN:", "workspace"])
    assert.equal(live.projection.getSnapshot().tasks.items[0]?.id, "new-top")
    assert.equal(live.projection.getSnapshot().tasks.items.length, 50)
    live.server.feedAvailable = true
    live.requests.length = 0
    live.server.commit([])
    await hintAndSettle(live)
    assert.deepEqual(live.requests.toSorted(), ["changes:2", "workspace"])
    live.projection.stop()
  })
}

test("the feed keeps deep rows current so the deep refresh waits five minutes unless a fallback made them stale", async () => {
  const { live, manual } = await liveWorkspaceWithFeed()
  live.server.commit([live.server.open[0]!.id])
  await hintAndSettle(live)
  live.requests.length = 0
  manual.clock.now += 60_000
  manual.fireAll()
  await waitUntil(() => live.reconciles() === 1)
  await live.settle()
  assert.deepEqual(live.requests.toSorted(), ["changes:2", "workspace"])

  live.requests.length = 0
  manual.clock.now += 240_000
  manual.fireAll()
  await waitUntil(() => live.reconciles() === 1)
  await live.settle()
  assert.equal(live.requests.filter((request) => request.startsWith("tasks:OPEN")).length, 6)
  assert.ok(!live.requests.some((request) => request.startsWith("changes:")))

  live.requests.length = 0
  live.server.commit(null)
  await hintAndSettle(live)
  live.requests.length = 0
  manual.clock.now += 60_000
  manual.fireAll()
  await waitUntil(() => live.reconciles() === 1)
  await live.settle()
  assert.equal(live.requests.filter((request) => request.startsWith("tasks:OPEN")).length, 6)
  live.projection.stop()
})

test("a hidden tab defers feed requests and catches up with one snapshot and one change request", async () => {
  let hidden = false
  const live = liveWorkspace({ openRows: 120, hidden: () => hidden, feed: true })
  await live.connect()
  await live.loadOpenRows(120)
  live.requests.length = 0
  hidden = true
  for (let index = 0; index < 5; index += 1) {
    live.server.commit([live.server.open[index * 20 + 1]!.id])
    live.send("hint", live.server.version)
  }
  await live.settle()
  assert.deepEqual(live.requests, [])
  hidden = false
  await live.projection.dispatch({ type: "visibility-changed" })
  await waitUntil(() => live.projection.getSnapshot().workspace?.version === 6)
  await live.settle()
  assert.deepEqual(live.requests.toSorted(), ["changes:1", "workspace"])
  live.projection.stop()
})
