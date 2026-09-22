import assert from "node:assert/strict"
import test from "node:test"

import type {
  AccessDiscovery,
  AiInteractionDetail,
  Location,
  Practice,
  Task,
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

test("first load restores only authorized scope and presents one coherent projection", async () => {
  const preferences = new Map([
    ["acuity.selectedPractice", "removed-practice"],
    ["acuity.selectedLocation", "removed-location"],
    ["acuity.taskLocationScope.practice-1", "removed-location"],
  ])
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(4),
      tasks: taskPage([task("task-1")]),
    }),
    realtime: realtime.adapter,
    preferences: {
      read: (key) => preferences.get(key) ?? null,
      write: (key, value) => preferences.set(key, value),
    },
  })

  await projection.start()
  assert.deepEqual(realtime.scope, {
    practiceID: "practice-1",
    locationID: "location-1",
  })

  await realtime.reconcile(0)

  const state = projection.getSnapshot()
  assert.equal(state.loadState, "ready")
  assert.equal(state.scope.practiceID, "practice-1")
  assert.equal(state.scope.locationID, "location-1")
  assert.equal(state.scope.locationScopeID, "")
  assert.equal(state.workspace?.version, 4)
  assert.deepEqual(state.tasks.items.map((item) => item.id), ["task-1"])
  assert.equal(state.selection.task?.id, "task-1")
  assert.equal(state.selection.engagement?.phone, "+15551234567")
  assert.equal(state.selection.view, "engagement")
  assert.equal(preferences.get("acuity.selectedPractice"), "practice-1")
  assert.equal(preferences.get("acuity.selectedLocation"), "location-1")
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

test("completed Task leaves the open queue but stays selected while it remains authorized", async () => {
  const realtime = deterministicRealtime()
  let taskItems = [task("task-1")]
  let selectedTask = task("task-1")
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(16),
    tasks: taskPage(taskItems),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...baseAuthority,
      tasks: async (_token, request) => success(
          request.state === "OPEN"
            ? taskPage(taskItems)
            : taskPage(selectedTask.state === "COMPLETED" ? [selectedTask] : []),
      ),
      task: async () => success(selectedTask),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  taskItems = []
  selectedTask = task("task-1", { state: "COMPLETED", version: 2, completedBy: { kind: "HUMAN", subject: "other-staff" } })
  await realtime.reconcile(0)

  const state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items, [])
  assert.deepEqual(state.completedTasks.items, [selectedTask])
  assert.equal(state.selection.task?.completedBy?.subject, "other-staff")
  assert.equal(state.selection.task?.state, "COMPLETED")
  assert.equal(state.selection.task?.version, 2)
  assert.equal(state.selection.contextPanelOpen, true)
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
  const authority: WorkspaceAuthorityAdapter = {
    ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(11), tasks: taskPage([openTask]) }),
    tasks: async (_token, request) => success(taskPage(request.state === "OPEN" ? (completed ? [] : [openTask]) : (completed ? [completedTask] : []))),
    completeTask: async () => { completed = true; return success(completedTask) },
    task: async () => success(completed ? completedTask : openTask),
  }
  const projection = createWorkspaceProjection({ authority, realtime: realtime.adapter, preferences: memoryPreferences() })
  await projection.start()
  await realtime.reconcile(0)
  const staleSnapshot = await realtime.prepareReconciliation(0)
  await projection.dispatch({ type: "complete-task", task: openTask })
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

test("successful Task creation refetches authoritative query membership", async () => {
  const realtime = deterministicRealtime()
  let currentPage = taskPage([])
  const projection = createWorkspaceProjection({
    authority: { ...deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(22),
      tasks: currentPage,
    }), tasks: async () => success(currentPage) },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  currentPage = taskPage([task("created-1")])
  await projection.dispatch({ type: "task-created", task: task("created-1") })

  assert.deepEqual(
    projection.getSnapshot().tasks.items.map((item) => item.id),
    ["created-1"],
  )
  assert.equal(realtime.refreshes, 1)
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
        refresh() {
          refreshes += 1
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
    async prepareReconciliation(minimumVersion: number) {
      assert.ok(callbacks)
      assert.ok(scope)
      const controller = new AbortController()
      return callbacks.reconcile({
        scope,
        token: "token",
        signal: controller.signal,
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
    await projection.dispatch({ type: "select-scope", practiceID: "practice-1", locationScopeID: "location-2" })
    assert.equal(projection.getSnapshot().selection.view, "manage-agent")
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
    unread: false,
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
      missedCalls: 0,
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

test("My Tasks queries follow-up while completed history includes every origin", async () => {
  const realtime = deterministicRealtime()
  const clock = new ManualClock()
  const requests: Parameters<WorkspaceAuthorityAdapter["tasks"]>[1][] = []
  const active = [task("ai"), task("missed", { origin: "MISSED_CALL_RECOVERY" }), task("voicemail", { origin: "VOICEMAIL_RECOVERY" })]
  const done = task("done", { state: "COMPLETED", completedBy: { kind: "HUMAN", subject: "other-staff" } })
  const projection = createWorkspaceProjection({
    authority: {
      ...deterministicAuthority({ discovery: accessDiscovery(), snapshot: workspaceSnapshot(22), tasks: taskPage(active) }),
      tasks: async (_token, request) => {
        requests.push(request)
        return success(taskPage(request.state === "OPEN" ? active : [done]))
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    environment: { clock },
  })
  await projection.start()
  await realtime.reconcile(0)
  assert.deepEqual(projection.getSnapshot().rail.expanded, ["tasks"])
  assert.deepEqual(projection.getSnapshot().tasks.items, active)
  assert.deepEqual(projection.getSnapshot().completedTasks.items, [done])
  assert.equal(requests.length, 2)
  assert.deepEqual(requests.map(({ state, folder, kind, responsibility, grouped, limit, includeCounts }) => ({ state, folder, kind, responsibility, grouped, limit, includeCounts })), [
    { state: "OPEN", folder: undefined, kind: "follow_up", responsibility: "mine", grouped: true, limit: 50, includeCounts: true },
    { state: "COMPLETED", folder: undefined, kind: undefined, responsibility: "mine", grouped: false, limit: 10, includeCounts: false },
  ])
  await clock.advance(90_000)
  await projection.dispatch({ type: "visibility-changed" })
  assert.equal(requests.length, 2, "personal-attention timer must not query retired windows")
  projection.stop()
})

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

test("text attention expiry refreshes without replacing the selected workspace with loading", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(), snapshot: workspaceSnapshot(4), tasks: taskPage([task("task-1")]),
    }),
    realtime: realtime.adapter,
    preferences: { read: () => null, write: () => {} },
  })
  await projection.dispatch({ type: "refresh-text-attention" })
  assert.equal(realtime.refreshes, 0)
  await projection.start()
  await realtime.reconcile(0)
  const selected = projection.getSnapshot().selection.task?.id
  const before = realtime.refreshes
  await projection.dispatch({ type: "refresh-text-attention" })
  assert.equal(realtime.refreshes, before + 1)
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
