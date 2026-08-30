import assert from "node:assert/strict"
import test from "node:test"

import type {
  AccessDiscovery,
  AiOutcomePage,
  AiInteractionDetail,
  CallingCall,
  Location,
  MessageThreadPage,
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
  assert.deepEqual(state.recoveryTasks.items, [])
  assert.deepEqual(state.messages.items, [])
  assert.deepEqual(state.aiOutcomes.items, [])
  assert.equal(state.selection.task, undefined)
  assert.equal(
    preferences.get("acuity.taskLocationScope.practice-1"),
    "location-2",
  )
  projection.stop()
})

test("pagination appends uniquely and authoritative refresh retains each window depth", async () => {
  const realtime = deterministicRealtime()
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(7),
    tasks: taskPage([task("task-1")]),
  })
  const authority: WorkspaceAuthorityAdapter = {
    ...baseAuthority,
    tasks: async (_token, request) => {
      if (request.folder === "missed_calls") {
        return success(taskPage([
            task("recovery-1", { origin: "MISSED_CALL_RECOVERY" }),
          ]))
      }
      return success(
          request.cursor === "task-more"
            ? taskPage([task("task-1"), task("task-2")])
            : { ...taskPage([task("task-1")]), nextCursor: "task-more" },
      )
    },
    messageThreads: async (_token, request) => success(
        request.cursor === "message-more"
          ? messagePage([message("message-1"), message("message-2")])
          : {
              ...messagePage([message("message-1")]),
              nextCursor: "message-more",
            },
    ),
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "load-more", window: "tasks" })
  await projection.dispatch({ type: "load-more", window: "messages" })

  let state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items.map((item) => item.id), ["task-1", "task-2"])
  assert.deepEqual(state.recoveryTasks.items.map((item) => item.id), [
    "recovery-1",
  ])
  assert.deepEqual(state.messages.items.map((item) => item.id), [
    "message-1",
    "message-2",
  ])

  await realtime.reconcile(0)
  state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items.map((item) => item.id), ["task-1", "task-2"])
  assert.deepEqual(state.messages.items.map((item) => item.id), [
    "message-1",
    "message-2",
  ])
  projection.stop()
})

test("appointment outcome windows keep independent cursors and one visible failure state", async () => {
  const realtime = deterministicRealtime()
  let failCancellations = false
  const authority: WorkspaceAuthorityAdapter = {
    ...deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(8),
      tasks: taskPage([task("task-1")]),
    }),
    aiOutcomes: async (_token, request) => {
      if (failCancellations && request.appointmentAction === "CANCELLED") {
        return unavailable()
      }
      if (request.appointmentAction === "BOOKED") {
        return success({
            ...outcomePage([
              outcome("booking-1", "BOOKED"),
              ...(request.cursor ? [outcome("booking-2", "BOOKED")] : []),
            ]),
            nextCursor: request.cursor ? "" : "booking-more",
          })
      }
      if (request.appointmentAction === "CANCELLED") {
        return success(outcomePage([outcome("cancellation-1", "CANCELLED")]))
      }
      return success(outcomePage([outcome("reschedule-1", "RESCHEDULED")]))
    },
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "load-more-outcomes", folder: "bookings" })

  let state = projection.getSnapshot()
  assert.deepEqual(state.aiOutcomes.items.map((item) => item.id), [
    "booking-1",
    "cancellation-1",
    "reschedule-1",
    "booking-2",
  ])
  assert.equal(state.aiOutcomes.nextCursors.bookings, "")
  assert.equal(state.aiOutcomes.nextCursors.cancellations, "")

  failCancellations = true
  await realtime.reconcile(0)
  state = projection.getSnapshot()
  assert.equal(state.loadState, "ready")
  assert.deepEqual(state.tasks.items.map((item) => item.id), ["task-1"])
  assert.deepEqual(state.aiOutcomes.items.map((item) => item.id), [
    "booking-1",
    "cancellation-1",
    "reschedule-1",
    "booking-2",
  ])
  assert.equal(
    state.aiOutcomes.error,
    "AI appointment updates are unavailable.",
  )
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
        request.folder === "work"
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
          request.folder === "work"
            ? taskPage(taskItems)
            : taskPage([]),
      ),
      task: async () => success(selectedTask),
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  taskItems = []
  selectedTask = task("task-1", { state: "COMPLETED", version: 2 })
  await realtime.reconcile(0)

  const state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items, [])
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
      if (request.folder === "missed_calls") {
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
    messageThreads: async () => success(messagePage([message("message-1")])),
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
  assert.deepEqual(state.messages.items.map((item) => item.id), ["message-1"])
  assert.equal(state.selection.task?.id, "initial-task")
  projection.stop()
})

test("committed completion updates counts and selection once, then reconciles authoritatively", async () => {
  const realtime = deterministicRealtime()
  const openTask = task("task-1", { category: "billing" })
  const authority: WorkspaceAuthorityAdapter = {
    ...deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(11),
      tasks: taskPage([openTask]),
    }),
    completeTask: async () => success(task("task-1", {
        category: "billing",
        state: "COMPLETED",
        version: 2,
        completedAt: "2026-08-30T12:10:00Z",
        completedBy: { kind: "HUMAN", subject: "user-1" },
      })),
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({ type: "complete-task", task: openTask })

  const state = projection.getSnapshot()
  assert.deepEqual(state.tasks.items, [])
  assert.equal(state.tasks.counts.tasks, 0)
  assert.equal(state.tasks.counts.categories.billing, 0)
  assert.equal(state.selection.task, undefined)
  assert.equal(state.selection.contextPanelOpen, false)
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

test("selection intent clears committed unread markers through the projection", async () => {
  const realtime = deterministicRealtime()
  let markedThreadID = ""
  const unreadTask = task("task-1", {
    unread: true,
    conversationThreadId: "message-1",
  })
  const authority: WorkspaceAuthorityAdapter = {
    ...deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(12),
      tasks: taskPage([unreadTask]),
    }),
    messageThreads: async () => success(messagePage([message("message-1")])),
    markMessageThreadRead: async (_token, threadID) => {
      markedThreadID = threadID
      return success({})
    },
  }
  const projection = createWorkspaceProjection({
    authority,
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
  await projection.dispatch({
    type: "select-engagement",
    engagement: {
      phone: "+15551234567",
      locations: [{ id: "location-1", name: "Downtown" }],
      latestActivity: "2026-08-30T12:00:00Z",
      openTaskCount: 1,
      unread: true,
    },
  })

  const state = projection.getSnapshot()
  assert.equal(markedThreadID, "message-1")
  assert.equal(state.messages.items[0]?.unread, false)
  assert.equal(state.tasks.items[0]?.unread, false)
  assert.equal(state.selection.engagement?.unread, false)
  projection.stop()
})

test("hidden AI refresh defers and visibility performs one bounded catch-up", async () => {
  const realtime = deterministicRealtime()
  const clock = new ManualClock()
  let hidden = true
  let outcomeQueries = 0
  const baseAuthority = deterministicAuthority({
    discovery: accessDiscovery(),
    snapshot: workspaceSnapshot(13),
    tasks: taskPage([]),
  })
  const projection = createWorkspaceProjection({
    authority: {
      ...baseAuthority,
      aiOutcomes: async () => {
        outcomeQueries += 1
        return success(outcomePage())
      },
    },
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
    environment: { clock, isHidden: () => hidden },
  })

  await projection.start()
  await realtime.reconcile(0)
  assert.equal(outcomeQueries, 3)

  await clock.advance(60_000)
  assert.equal(outcomeQueries, 3)

  hidden = false
  await projection.dispatch({ type: "visibility-changed" })
  await projection.dispatch({ type: "visibility-changed" })
  await clock.advance(0)
  assert.equal(outcomeQueries, 6)
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
    expanded: [],
    expandedAppointments: [],
    taskCategory: "all",
    scrollTop: 0,
  })

  await projection.dispatch({ type: "toggle-rail-section", section: "tasks" })
  await projection.dispatch({ type: "set-task-category", category: "billing" })
  await projection.dispatch({ type: "remember-rail-scroll", scrollTop: 42 })

  assert.deepEqual(
    JSON.parse(values.get("acuity.attentionRail.user-1.practice-1") ?? ""),
    {
      version: 1,
      expanded: ["tasks"],
      expandedAppointments: [],
      taskCategory: "billing",
      scrollTop: 42,
    },
  )
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
      messageThreads: async () =>
        unauthorized
          ? unauthorizedResult()
          : success(messagePage([message("message-1")])),
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
  assert.deepEqual(state.messages.items, [])
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

test("successful Task creation projects once and requests authoritative reconciliation", async () => {
  const realtime = deterministicRealtime()
  const projection = createWorkspaceProjection({
    authority: deterministicAuthority({
      discovery: accessDiscovery(),
      snapshot: workspaceSnapshot(22),
      tasks: taskPage([]),
    }),
    realtime: realtime.adapter,
    preferences: memoryPreferences(),
  })

  await projection.start()
  await realtime.reconcile(0)
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
          request.folder === "missed_calls"
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
  assert.equal(state.recoveryTasks.items[0]?.version, 3)
  assert.equal(state.recoveryTasks.items[0]?.title, "Authoritative recovery detail")
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
    type: "select-ai-interaction",
    interaction: outcome("interaction-1", "BOOKED"),
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
    tasks: async () => success(tasks),
    messageThreads: async () => success(messagePage()),
    aiOutcomes: async () => success(outcomePage()),
    aiInteraction: async () => missing(),
    task: async (_token, taskID) => {
      const selected = tasks.items.find((item) => item.id === taskID)
      return selected ? success(selected) : missing()
    },
    call: async () => missing(),
    completeTask: async () => unavailable(),
    reviewAIOutcome: async () => unavailable(),
    markMessageThreadRead: async () => unavailable(),
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

function messagePage(
  items: MessageThreadPage["items"] = [],
): MessageThreadPage {
  return { items, nextCursor: "" }
}

function outcomePage(items: AiOutcomePage["items"] = []): AiOutcomePage {
  return {
    items,
    nextCursor: "",
    counts: { tasks: 0, bookings: 0, cancellations: 0, reschedules: 0 },
  }
}

void ({} as CallingCall)

function message(
  id: string,
  overrides: Partial<MessageThreadPage["items"][number]> = {},
): MessageThreadPage["items"][number] {
  return {
    id,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Downtown",
    officePhone: "+15557654321",
    externalPhone: "+15551234567",
    outboundBlocked: false,
    createdAt: "2026-08-30T12:00:00Z",
    updatedAt: "2026-08-30T12:00:00Z",
    preview: "Please call me back.",
    latestDirection: "INBOUND",
    latestDelivery: "Delivered",
    latestActivity: "2026-08-30T12:00:00Z",
    openTaskCount: 0,
    unread: true,
    ...overrides,
  }
}

function memoryPreferences() {
  const values = new Map<string, string>()
  return {
    read: (key: string) => values.get(key) ?? null,
    write: (key: string, value: string) => values.set(key, value),
  }
}

function outcome(
  id: string,
  appointmentAction: "BOOKED" | "CANCELLED" | "RESCHEDULED",
): AiOutcomePage["items"][number] {
  return {
    id,
    locationId: "location-1",
    locationName: "Downtown",
    sourceCallId: `${id}-call`,
    phone: "+15551234567",
    startedAt: "2026-08-30T12:00:00Z",
    endedAt: "2026-08-30T12:05:00Z",
    status: "COMPLETED",
    appointmentAction,
    appointmentOutcome:
      appointmentAction === "BOOKED"
        ? "BOOKING"
        : appointmentAction === "CANCELLED"
          ? "CANCELLATION"
          : "RESCHEDULE",
    appointmentOccurredAt: "2026-08-30T12:03:00Z",
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
