import type {
  AccessDiscovery,
  AiInteractionDetail,
  CallingCall,
  CallingDispositionResult,
  EngagementSummary,
  Task,
  TaskFolderCounts,
  TaskPage,
  TaskQueryRequest,
  WorkspaceSnapshot,
} from "./api/generated/types.gen.ts"
import { appendUniqueByID } from "./workspace-ordering.ts"
import { canViewPracticeAnalytics } from "./booking-analytics.ts"
import { resolveWorkspaceSearch } from "./workspace-search.ts"
import {
  createWorkspaceRequestBudget,
  type WorkspaceRequestBudget,
} from "./workspace-sync/workspace-request-budget.ts"
import type { TaskCategoryFilter } from "./workspace-triage.ts"

export type WorkspaceLoadState =
  | "loading"
  | "ready"
  | "unauthenticated"
  | "unauthorized"
  | "unavailable"

export type WorkspaceConnectionState =
  | "connecting"
  | "connected"
  | "degraded"

export type WorkspaceView = "none" | "engagement" | "analytics" | "operator-analytics"
export type WorkspaceContextView = "task" | "call" | "ai-call"
export type WorkspaceRailSection = "tasks" | "calls" | "appointments" | "texts" | "completed"

export type WorkspaceRailState = {
  expanded: WorkspaceRailSection[]
  taskResponsibility?: "mine" | "all"
  taskCategory: TaskCategoryFilter
  scrollTop: number
}

export type WorkspaceScope = {
  practiceID: string
  locationID: string
  locationScopeID: string
}

export type WorkspaceQueryWindow<T> = {
  items: T[]
  nextCursor: string
  loading: boolean
  error: string
}

export type WorkspaceProjectionState = {
  loadState: WorkspaceLoadState
  connection: WorkspaceConnectionState
  discovery?: AccessDiscovery
  workspace?: WorkspaceSnapshot
  scope: WorkspaceScope
  search: {
    input: string
    applied: string
    error: string
  }
  tasks: WorkspaceQueryWindow<Task> & { counts: TaskFolderCounts }
  completedTasks: WorkspaceQueryWindow<Task>
  selection: {
    task?: Task
    taskGroup?: Task
    taskError: string
    engagement?: EngagementSummary
    aiInteractionID: string
    aiInteraction?: AiInteractionDetail
    aiInteractionLoading: boolean
    aiInteractionError: string
    historicalCall?: CallingCall
    view: WorkspaceView
    contextView: WorkspaceContextView
    contextPanelOpen: boolean
  }
  detailRevision: number
  completion: {
    pendingTaskID: string
    errorTaskID: string
    error: string
  }
  rail: WorkspaceRailState
}

export type WorkspaceAuthentication =
  | { status: "authenticated"; token: string }
  | { status: "unauthenticated" | "unavailable" }

export type WorkspaceAuthorityFailure =
  | { kind: "authentication-unavailable" }
  | { kind: "unauthenticated" }
  | { kind: "unauthorized" }
  | { kind: "missing" }
  | { kind: "conflict" }
  | { kind: "unavailable" }

export type WorkspaceAuthorityResult<T> =
  | { kind: "success"; data: T }
  | WorkspaceAuthorityFailure

export type WorkspaceAuthorityAdapter = {
  authenticate: () => Promise<WorkspaceAuthentication>
  discover: (
    token: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<AccessDiscovery>>
  workspace: (
    token: string,
    scope: { practiceID: string; locationID: string },
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<WorkspaceSnapshot>>
  tasks: (
    token: string,
    request: TaskQueryRequest,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<TaskPage>>
  aiInteraction: (
    token: string,
    interactionID: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<AiInteractionDetail>>
  task: (
    token: string,
    taskID: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<Task>>
  call: (
    token: string,
    callID: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<CallingCall>>
  completeTask: (
    token: string,
    task: Pick<Task, "id" | "version">,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<Task>>
}

type Reconciliation = {
  version: number
  apply: () => void
}

export type WorkspaceRealtimeCallbacks = {
  getToken: () => Promise<string | null | undefined>
  reconcile: (input: {
    scope: { practiceID: string; locationID: string }
    token: string
    signal: AbortSignal
    minimumVersion: number
  }) => Promise<Reconciliation>
  onStateChange: (state: WorkspaceConnectionState) => void
  onUnauthorized: () => void
}

export type WorkspaceRealtimeController = {
  setScope: (scope?: { practiceID: string; locationID: string }) => void
  refresh: () => void
  visibilityChanged: () => void
  stop: () => void
}

export type WorkspaceRealtimeAdapter = {
  connect: (
    callbacks: WorkspaceRealtimeCallbacks,
  ) => WorkspaceRealtimeController
}

export type WorkspacePreferences = {
  read: (key: string) => string | null
  write: (key: string, value: string) => void
}

export type WorkspaceProjectionEnvironment = {
  clock: {
    setTimeout: (callback: () => void, milliseconds: number) => number
    clearTimeout: (id: number) => void
  }
}

export type WorkspaceProjection = {
  getSnapshot: () => WorkspaceProjectionState
  subscribe: (listener: () => void) => () => void
  start: () => Promise<void>
  dispatch: (intent: WorkspaceProjectionIntent) => Promise<void>
  stop: () => void
}

export type WorkspaceProjectionIntent =
  | {
      type: "select-scope"
      practiceID: string
      locationScopeID: string
    }
  | {
      type: "load-more"
      window: "tasks" | "completedTasks"
    }
  | { type: "set-search"; value: string }
  | { type: "submit-search" }
  | { type: "complete-task"; task: Task }
  | { type: "select-engagement"; engagement: EngagementSummary }
  | { type: "select-task"; task: Task; rememberForCall?: boolean }
  | { type: "select-analytics" }
  | { type: "select-operator-analytics" }
  | { type: "open-ai-context"; interactionID: string }
  | { type: "open-task-context"; task: Task }
  | { type: "open-call-context"; callID: string }
  | { type: "close-context" }
  | { type: "context-transition-ended" }
  | { type: "task-committed"; task: Task; advance?: boolean }
  | { type: "task-created"; task: Task }
  | { type: "visibility-changed" }
  | { type: "retry" }
  | { type: "refresh-text-attention" }
  | { type: "call-connected"; call: CallingCall }
  | { type: "remember-return-task"; taskID: string }
  | { type: "return-to-call" }
  | { type: "call-disposition"; result: CallingDispositionResult }
  | { type: "toggle-rail-section"; section: WorkspaceRailSection }
  | { type: "set-task-category"; category: TaskCategoryFilter }
  | { type: "set-task-filters"; responsibility?: "mine" | "all"; category?: TaskCategoryFilter }
  | { type: "remember-rail-scroll"; scrollTop: number }

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"
const taskScopeStorageKey = "acuity.taskLocationScope"

export class WorkspaceProjectionAccessError extends Error {
  readonly loadState: "unauthenticated" | "unauthorized"

  constructor(loadState: "unauthenticated" | "unauthorized") {
    super(`workspace access lost: ${loadState}`)
    this.loadState = loadState
  }
}

export function createWorkspaceProjection({
  authority,
  realtime,
  preferences,
  environment,
}: {
  authority: WorkspaceAuthorityAdapter
  realtime: WorkspaceRealtimeAdapter
  preferences: WorkspacePreferences
  environment?: WorkspaceProjectionEnvironment
}): WorkspaceProjection {
  let state = initialState()
  let stopped = false
  let scopeGeneration = 0
  let detailGeneration = 0
  let returnTaskID = ""
  let focusedCallID = ""
  let railScrollTop = 0
  const requestBudget: WorkspaceRequestBudget | undefined = environment
    ? createWorkspaceRequestBudget({
        clock: environment.clock,
        refreshDetails: () =>
          patch((current) => ({
            ...current,
            detailRevision: current.detailRevision + 1,
          })),
      })
    : undefined
  const queryGenerations = {
    tasks: 0,
    taskCounts: 0,
    completedTasks: 0,
  }
  let accessController: AbortController | undefined
  const listeners = new Set<() => void>()

  function publish(next: WorkspaceProjectionState) {
    if (stopped) return
    state = next
    requestBudget?.setDetailRefreshMounted(
      next.selection.view === "engagement" &&
        Boolean(next.selection.engagement),
    )
    for (const listener of listeners) listener()
  }

  function patch(
    update: (current: WorkspaceProjectionState) => WorkspaceProjectionState,
  ) {
    publish(update(state))
  }

  function failClosed(
    loadState: "unauthenticated" | "unauthorized" | "unavailable",
  ) {
    patch((current) => ({
      ...initialState(),
      loadState,
      connection: current.connection,
    }))
    realtimeController.setScope()
  }

  async function getToken() {
    const generation = scopeGeneration
    const authentication = await authority.authenticate()
    if (stopped || generation !== scopeGeneration) {
      throw new Error("workspace authentication request is obsolete")
    }
    if (authentication.status === "authenticated") {
      return authentication.token
    }
    if (authentication.status === "unauthenticated") {
      failClosed("unauthenticated")
      return undefined
    }
    throw new Error("access token is temporarily unavailable")
  }

  async function authenticatedRequest<T>(
    request: (
      token: string,
      signal: AbortSignal,
    ) => Promise<WorkspaceAuthorityResult<T>>,
    signal = new AbortController().signal,
  ): Promise<WorkspaceAuthorityResult<T>> {
    const authentication = await authority.authenticate()
    switch (authentication.status) {
      case "unauthenticated":
        return { kind: "unauthenticated" }
      case "unavailable":
        return { kind: "authentication-unavailable" }
      case "authenticated":
        return request(authentication.token, signal)
    }
  }

  function failIfAccessLost(result: WorkspaceAuthorityResult<unknown>) {
    if (
      result.kind !== "unauthenticated" &&
      result.kind !== "unauthorized"
    ) {
      return false
    }
    failClosed(result.kind)
    return true
  }

  async function reconcile({
    scope,
    token,
    signal,
    minimumVersion,
  }: Parameters<WorkspaceRealtimeCallbacks["reconcile"]>[0]) {
    const generation = scopeGeneration
    const countGeneration = ++queryGenerations.taskCounts
    const taskGeneration = ++queryGenerations.tasks
    const completedGeneration = ++queryGenerations.completedTasks
    const current = state
    const taskRequest = taskQueryRequest(current.scope, current.search.applied, current.rail)
    const selectedTaskID = current.selection.task?.id
    const selectedAIInteractionID = current.selection.aiInteractionID
    const [
      snapshotResult,
      taskPageResult,
      completedResult,
      selectedTaskResult,
      selectedAIResult,
    ] =
      await Promise.all([
        authority.workspace(token, scope, signal),
        loadTaskWindow(token, taskRequest, current.tasks.items.length, signal),
        loadTaskWindow(token, completedTaskQueryRequest(current.scope, current.search.applied, current.rail), current.completedTasks.items.length, signal),
        selectedTaskID
          ? authority.task(token, selectedTaskID, signal)
          : Promise.resolve(undefined),
        selectedAIInteractionID
          ? authority.aiInteraction(token, selectedAIInteractionID, signal)
          : Promise.resolve(undefined),
      ])

    if (
      stopped || signal.aborted || generation !== scopeGeneration ||
      state.scope.practiceID !== scope.practiceID ||
      state.scope.locationID !== scope.locationID
    ) {
      return { version: minimumVersion, apply: () => {} }
    }

    const taskResult = requireTaskCounts(taskPageResult)
    const authorityResults = [
      snapshotResult,
      taskResult,
      completedResult,
      selectedTaskResult,
      selectedAIResult,
    ]
    const accessLoss = authorityResults.find(
      (result) =>
        result?.kind === "unauthenticated" || result?.kind === "unauthorized",
    )
    if (
      accessLoss?.kind === "unauthenticated" ||
      accessLoss?.kind === "unauthorized"
    ) {
      failClosed(accessLoss.kind)
      throw new WorkspaceProjectionAccessError(accessLoss.kind)
    }
    if (snapshotResult.kind !== "success") {
      throw new Error("workspace authority is unavailable")
    }
    if (snapshotResult.data.version < minimumVersion) {
      throw new Error("workspace authority has not reached the hinted version")
    }

    const snapshot = snapshotResult.data
    return {
      version: snapshot.version,
      apply: () => {
        if (
          stopped ||
          signal.aborted ||
          generation !== scopeGeneration ||
          state.scope.practiceID !== scope.practiceID ||
          state.scope.locationID !== scope.locationID ||
          (state.workspace && snapshot.version < state.workspace.version)
        ) {
          return
        }
        patch((currentState) => {
          const taskWindowCurrent =
            taskGeneration === queryGenerations.tasks
          const completedWindowCurrent = completedGeneration === queryGenerations.completedTasks
          const refreshedSelected =
            taskWindowCurrent && selectedTaskResult?.kind === "success"
              ? selectedTaskResult.data
              : undefined
          const tasks = taskWindowCurrent && taskResult.kind === "success"
            ? taskResult.data.items.map((task) =>
                refreshedSelected?.state === "OPEN" && task.id === refreshedSelected.id && !task.groupMembers
                  ? refreshedSelected : task)
            : currentState.tasks.items
          const selectionStillMatches = taskWindowCurrent &&
            Boolean(selectedTaskID) &&
            currentState.selection.task?.id === selectedTaskID
          const selectionMissing =
            selectionStillMatches &&
            selectedTaskResult?.kind === "missing"
          let selection = currentState.selection
          if (selectionMissing) {
            selection = {
              ...selection,
              task: undefined,
              taskGroup: undefined,
              taskError: "",
              contextPanelOpen:
                selection.contextView === "task"
                  ? false
                  : selection.contextPanelOpen,
            }
          } else if (selectionStillMatches) {
            if (refreshedSelected) {
              selection = {
                ...selection,
                task: refreshedSelected,
                taskGroup: refreshedSelected.state === "COMPLETED" ? undefined : selection.taskGroup,
                taskError: "",
                engagement: taskEngagement(refreshedSelected),
              }
            } else if (selectedTaskResult) {
              selection = {
                ...selection,
                taskError: selectedTaskDetailError,
              }
            }
          } else if (taskWindowCurrent &&
            !currentState.selection.task &&
            currentState.selection.view === "none" &&
            tasks[0]
          ) {
            const firstTask = tasks[0]
            selection = {
              ...selection,
              task: firstTask,
              taskGroup: firstTask.groupMembers && firstTask.groupMembers.length > 1 ? firstTask : undefined,
              taskError: "",
              engagement: taskEngagement(firstTask),
              view: "engagement",
              contextView: "task",
              contextPanelOpen: true,
            }
          }
          const aiSelectionStillMatches =
            Boolean(selectedAIInteractionID) &&
            selection.aiInteractionID === selectedAIInteractionID
          if (aiSelectionStillMatches && selectedAIResult?.kind === "missing") {
            selection = {
              ...selection,
              aiInteractionID: "",
              aiInteraction: undefined,
              aiInteractionLoading: false,
              aiInteractionError: "",
              contextPanelOpen:
                selection.contextView === "ai-call"
                  ? false
                  : selection.contextPanelOpen,
            }
          } else if (
            aiSelectionStillMatches &&
            selectedAIResult?.kind === "success"
          ) {
            selection = {
              ...selection,
              aiInteraction: selectedAIResult.data,
              aiInteractionLoading: false,
              aiInteractionError: "",
            }
          } else if (aiSelectionStillMatches && selectedAIResult) {
            selection = {
              ...selection,
              aiInteractionLoading: false,
              aiInteractionError: aiInteractionDetailError,
            }
          }
          let taskWindow = currentState.tasks
          if (taskWindowCurrent) {
            taskWindow = taskResult.kind === "success"
              ? {
                  items: tasks,
                  nextCursor: taskResult.data.nextCursor,
                  counts: currentState.tasks.counts,
                  loading: false,
                  error: "",
                }
              : { ...taskWindow, loading: false, error: taskWindowError }
          }
          // Cursor requests only replace rows; they must not discard a newer
          // authoritative count for the same scope and search.
          if (countGeneration === queryGenerations.taskCounts) {
            taskWindow = taskResult.kind === "success"
              ? { ...taskWindow, counts: taskResult.data.counts }
              : { ...taskWindow, error: taskWindowError }
          }
          return {
            ...currentState,
            loadState: "ready",
            workspace: snapshot,
            tasks: taskWindow,
            completedTasks: completedWindowCurrent
              ? completedResult.kind === "success"
                ? { items: completedResult.data.items, nextCursor: completedResult.data.nextCursor, loading: false, error: "" }
                : { ...currentState.completedTasks, loading: false, error: completedWindowError }
              : currentState.completedTasks,
            selection,
          }
        })
        loadSelectedAppointmentEligibility()
        requestBudget?.signalDetailRefresh()
      },
    }
  }

  const realtimeController = realtime.connect({
    getToken,
    reconcile,
    onStateChange(connection) {
      patch((current) => ({ ...current, connection }))
      if (connection === "degraded" && !state.workspace) {
        patch((current) => ({ ...current, loadState: "unavailable" }))
      }
    },
    onUnauthorized() {
      if (
        state.loadState !== "unauthenticated" &&
        state.loadState !== "unauthorized"
      ) {
        failClosed("unauthorized")
      }
    },
  })

  async function start() {
    stopped = false
    accessController?.abort()
    const controller = new AbortController()
    accessController = controller
    patch((current) => ({ ...current, loadState: "loading" }))
    const authentication = await authority.authenticate()
    if (controller.signal.aborted) return
    if (authentication.status !== "authenticated") {
      failClosed(
        authentication.status === "unauthenticated"
          ? "unauthenticated"
          : "unavailable",
      )
      return
    }
    const result = await authority.discover(
      authentication.token,
      controller.signal,
    )
    if (controller.signal.aborted) return
    if (result.kind !== "success") {
      if (!failIfAccessLost(result)) failClosed("unavailable")
      return
    }
    const scope = restoreAuthorizedScope(result.data, preferences)
    if (!scope) {
      failClosed("unauthorized")
      return
    }
    scopeGeneration += 1
    const rail = restoreRailPreferences(
      preferences,
      result.data.actor.subject,
      scope.practiceID,
    )
    railScrollTop = rail.scrollTop
    publish({
      ...initialState(),
      loadState: "loading",
      connection: state.connection,
      discovery: result.data,
      scope,
      rail,
    })
    realtimeController.setScope({
      practiceID: scope.practiceID,
      locationID: scope.locationID,
    })
  }

  async function dispatch(intent: WorkspaceProjectionIntent): Promise<void> {
    if (intent.type === "select-scope") {
      selectScope(intent)
      return
    }
    if (intent.type === "load-more") {
      await loadMore(intent.window)
      return
    }
    if (intent.type === "set-search") {
      patch((current) => ({
        ...current,
        search: { ...current.search, input: intent.value, error: "" },
      }))
      return
    }
    if (intent.type === "submit-search") {
      await submitSearch()
      return
    }
    if (intent.type === "complete-task") {
      await completeTaskIntent(intent.task)
      return
    }
    if (intent.type === "select-engagement") {
      selectEngagement(intent.engagement)
      return
    }
    if (intent.type === "select-task") {
      if (intent.rememberForCall) returnTaskID = intent.task.id
      selectEngagement(taskEngagement(intent.task), intent.task)
      return
    }
    if (intent.type === "select-analytics" || intent.type === "select-operator-analytics") {
      const operator = state.discovery?.platformOperator
      if (intent.type === "select-operator-analytics" ? !operator : !canViewPracticeAnalytics(state.discovery, state.scope.practiceID)) return
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          view: intent.type === "select-analytics" ? "analytics" : "operator-analytics",
          contextPanelOpen: false,
        },
      }))
      return
    }
    if (intent.type === "open-ai-context") {
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          aiInteractionID: intent.interactionID,
          aiInteraction:
            current.selection.aiInteractionID === intent.interactionID
              ? current.selection.aiInteraction
              : undefined,
          aiInteractionLoading: true,
          aiInteractionError: "",
          contextView: "ai-call",
          contextPanelOpen: true,
        },
      }))
      await loadAIInteractionDetail(intent.interactionID)
      return
    }
    if (intent.type === "open-task-context") {
      projectTaskIntent(intent.task, true)
      return
    }
    if (intent.type === "open-call-context") {
      await openCallContext(intent.callID)
      return
    }
    if (intent.type === "close-context") {
      detailGeneration += 1
      patch((current) => ({
        ...current,
        selection: { ...current.selection, contextPanelOpen: false },
      }))
      return
    }
    if (intent.type === "context-transition-ended") {
      if (!state.selection.contextPanelOpen) {
        patch((current) => ({
          ...current,
          selection: { ...current.selection, historicalCall: undefined },
        }))
      }
      return
    }
    if (intent.type === "task-committed") {
      const selectedID = state.selection.task?.id
      const generation = scopeGeneration
      const ownsSelection = selectedID === intent.task.id || state.selection.taskGroup?.groupMembers?.some((member) => member.id === intent.task.id)
      queryGenerations.taskCounts += 1
      projectTaskIntent(intent.task, false)
      await refreshTaskWindows(state.search.applied)
      if (intent.advance && intent.task.state === "COMPLETED" && ownsSelection && generation === scopeGeneration && state.selection.task?.id === selectedID && !state.tasks.error) {
        const next = state.tasks.items.find((task) => task.id !== intent.task.id)
        if (next) selectEngagement(taskEngagement(next), next)
      }
      return
    }
    if (intent.type === "task-created") {
      queryGenerations.taskCounts += 1
      projectTaskIntent(intent.task, false)
      await refreshTaskWindows(state.search.applied)
      return
    }
    if (intent.type === "visibility-changed") {
      realtimeController.visibilityChanged()
      return
    }
    if (intent.type === "refresh-text-attention") {
      if (state.loadState === "ready") await refreshTaskWindows(state.search.applied)
      return
    }
    if (intent.type === "retry") {
      if (state.discovery && state.scope.practiceID && state.scope.locationID) {
        realtimeController.refresh()
      } else {
        await start()
      }
      return
    }
    if (intent.type === "call-connected") {
      handleCallConnected(intent.call)
      return
    }
    if (intent.type === "remember-return-task") {
      returnTaskID = intent.taskID
      return
    }
    if (intent.type === "return-to-call") {
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          contextView: "call",
          contextPanelOpen: true,
        },
      }))
      return
    }
    if (intent.type === "call-disposition") {
      await handleDisposition(intent.result)
      return
    }
    if (intent.type === "toggle-rail-section") {
      updateRail((rail) => ({
        ...rail,
        expanded: rail.expanded.includes(intent.section)
          ? rail.expanded.filter((section) => section !== intent.section)
          : [...rail.expanded, intent.section],
      }))
      return
    }
    if (intent.type === "set-task-category") {
      updateRail((rail) => ({ ...rail, taskCategory: intent.category }))
      await refreshTaskWindows(state.search.applied, true)
      selectFirstVisibleTask()
      return
    }
    if (intent.type === "set-task-filters") {
      updateRail((rail) => ({ ...rail,
        taskResponsibility: intent.responsibility ?? rail.taskResponsibility ?? "mine",
        taskCategory: intent.category ?? rail.taskCategory,
      }))
      await refreshTaskWindows(state.search.applied, true)
      selectFirstVisibleTask()
      return
    }
    if (intent.type === "remember-rail-scroll") {
      railScrollTop = Math.max(0, intent.scrollTop)
      persistRail({ ...state.rail, scrollTop: railScrollTop })
    }
  }

  function updateRail(update: (rail: WorkspaceRailState) => WorkspaceRailState) {
    const next = update({ ...state.rail, scrollTop: railScrollTop })
    railScrollTop = next.scrollTop
    patch((current) => ({ ...current, rail: next }))
    persistRail(next)
  }

  function persistRail(rail: WorkspaceRailState) {
    const discovery = state.discovery
    if (!discovery || !state.scope.practiceID) return
    preferences.write(
      railStorageKey(discovery.actor.subject, state.scope.practiceID),
      JSON.stringify({ version: 1, ...rail }),
    )
  }

  function selectScope({
    practiceID,
    locationScopeID,
  }: Extract<WorkspaceProjectionIntent, { type: "select-scope" }>) {
    const discovery = state.discovery
    if (!discovery) return
    const practice = discovery.practices.find((item) => item.id === practiceID)
    if (!practice) return
    const requestedLocation = locationScopeID
      ? practice.locations.find((location) => location.id === locationScopeID)
      : undefined
    if (locationScopeID && !requestedLocation) return
    const location =
      requestedLocation ??
      (practice.id === state.scope.practiceID
        ? practice.locations.find(
            (candidate) => candidate.id === state.scope.locationID,
          )
        : undefined) ??
      practice.locations[0]
    if (!location) return

    const nextScope = {
      practiceID: practice.id,
      locationID: location.id,
      locationScopeID:
        practice.locations.length === 1 ? location.id : locationScopeID,
    }
    if (
      nextScope.practiceID === state.scope.practiceID &&
      nextScope.locationID === state.scope.locationID &&
      nextScope.locationScopeID === state.scope.locationScopeID
    ) {
      return
    }

    const activeScopeChanged =
      nextScope.practiceID !== state.scope.practiceID ||
      nextScope.locationID !== state.scope.locationID
    scopeGeneration += 1
    obsoleteAllQueries()
    preferences.write(practiceStorageKey, nextScope.practiceID)
    preferences.write(locationStorageKey, nextScope.locationID)
    preferences.write(
      `${taskScopeStorageKey}.${nextScope.practiceID}`,
      nextScope.locationScopeID,
    )
    const current = state
    const rail = restoreRailPreferences(
      preferences,
      discovery.actor.subject,
      nextScope.practiceID,
    )
    railScrollTop = rail.scrollTop
    const empty = initialState()
    const analyticsView = current.selection.view === "analytics" && canViewPracticeAnalytics(discovery, nextScope.practiceID)
      ? "analytics"
      : current.selection.view === "operator-analytics" && discovery.platformOperator ? "operator-analytics" : "none"
    publish({
      ...empty,
      selection: { ...empty.selection, view: analyticsView },
      loadState: activeScopeChanged ? "loading" : current.loadState,
      connection: current.connection,
      discovery,
      workspace: activeScopeChanged ? undefined : current.workspace,
      scope: nextScope,
      search: current.search,
      detailRevision: current.detailRevision,
      rail,
    })
    realtimeController.setScope({
      practiceID: nextScope.practiceID,
      locationID: nextScope.locationID,
    })
    if (!activeScopeChanged) realtimeController.refresh()
  }

  async function loadMore(
    window: Extract<
      WorkspaceProjectionIntent,
      { type: "load-more" }
    >["window"],
  ) {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const currentWindow = state[window]
    if (currentWindow.loading || !currentWindow.nextCursor) return
    const queryGeneration = ++queryGenerations[window]
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: true, error: "" },
    }))
    const result = await authenticatedRequest((token, signal) =>
      authority.tasks(
        token,
        {
          ...(window === "tasks"
            ? taskQueryRequest(state.scope, state.search.applied, state.rail)
            : completedTaskQueryRequest(state.scope, state.search.applied, state.rail)),
          cursor: currentWindow.nextCursor,
          includeCounts: false,
        },
        signal,
      ),
    )
    if (
      generation !== scopeGeneration ||
      queryGeneration !== queryGenerations[window] ||
      stopped
    ) return
    if (failIfAccessLost(result)) return
    if (result.kind !== "success") {
      setWindowFailure(window)
      return
    }
    if (window === "tasks") {
      patch((current) => ({
        ...current,
        tasks: {
          items: appendUniqueByID(current.tasks.items, result.data.items),
          nextCursor: result.data.nextCursor,
          counts: current.tasks.counts,
          loading: false,
          error: "",
        },
      }))
      return
    }
    patch((current) => ({
      ...current,
      completedTasks: {
        items: appendUniqueByID(current.completedTasks.items, result.data.items),
        nextCursor: result.data.nextCursor,
        loading: false,
        error: "",
      },
    }))
  }

  async function submitSearch() {
    if (!state.discovery || !state.scope.practiceID) return
    const resolved = resolveWorkspaceSearch(state.search.input)
    if (resolved.kind === "phone") {
      const practice = state.discovery.practices.find(
        (item) => item.id === state.scope.practiceID,
      )
      if (!practice) return
      patch((current) => ({
        ...current,
        search: { input: "", applied: "", error: "" },
        selection: {
          ...current.selection,
          task: undefined,
          taskGroup: undefined,
          taskError: "",
          aiInteractionID: "",
          aiInteraction: undefined,
          aiInteractionLoading: false,
          aiInteractionError: "",
          historicalCall: undefined,
          engagement: newNumberEngagement(
            resolved.value,
            practice.locations,
            current.scope.locationScopeID,
          ),
          view: "engagement",
          contextPanelOpen: false,
        },
      }))
      await refreshTaskWindows("", true)
      return
    }
    patch((current) => ({
      ...current,
      search: { ...current.search, applied: resolved.value, error: "" },
    }))
    await refreshTaskWindows(resolved.value, true)
  }

  async function completeTaskIntent(task: Task) {
    if (state.completion.pendingTaskID || task.state !== "OPEN") return
    const generation = scopeGeneration
    patch((current) => ({
      ...current,
      completion: { pendingTaskID: task.id, errorTaskID: "", error: "" },
    }))
    const result = await authenticatedRequest((token, signal) =>
      authority.completeTask(token, task, signal),
    )
    if (generation !== scopeGeneration || stopped) return
    if (failIfAccessLost(result)) return
    if (result.kind !== "success") {
      setCompletionFailure(
        task.id,
        result.kind === "conflict"
          ? "This Task changed elsewhere. Open it to review the latest state, then retry."
          : result.kind === "authentication-unavailable"
            ? "Task completion is temporarily unavailable. Retry in a moment."
            : "This Task could not be completed. Retry from the row or open its details.",
      )
      return
    }
    queryGenerations.taskCounts += 1
    projectTaskIntent(result.data, false)
    patch((current) => ({
      ...current,
      detailRevision: current.detailRevision + 1,
    }))
    await refreshTaskWindows(state.search.applied)
    if (generation !== scopeGeneration || stopped || state.loadState !== "ready") return
    patch((current) => ({
      ...current,
      completion: { pendingTaskID: "", errorTaskID: "", error: "" },
    }))
  }

  function selectFirstVisibleTask() {
    if (state.loadState !== "ready" || state.tasks.error || state.selection.contextView === "call") return
    const selectedID = state.selection.task?.id
    if (state.tasks.items.some((task) => task.id === selectedID || task.groupMembers?.some((member) => member.id === selectedID))) return
    const first = state.tasks.items[0]
    if (first) selectEngagement(taskEngagement(first), first)
    else patch((current) => ({ ...current, selection: { ...current.selection, task: undefined, taskGroup: undefined, contextPanelOpen: false } }))
  }

  function selectEngagement(engagement: EngagementSummary, task?: Task) {
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        task,
        taskGroup: task?.groupMembers && task.groupMembers.length > 1 ? task : undefined,
        taskError: "",
        engagement,
        aiInteractionID: "",
        aiInteraction: undefined,
        aiInteractionLoading: false,
        aiInteractionError: "",
        historicalCall: undefined,
        view: "engagement",
        contextView: task ? "task" : current.selection.contextView,
        contextPanelOpen: Boolean(task),
      },
    }))
    loadSelectedAppointmentEligibility()
  }

  function loadSelectedAppointmentEligibility() {
    const task = state.selection.task
    const id = task?.origin === "APPOINTMENT_REVIEW" ? task.sourceInteractionId : undefined
    if (
      !id || state.selection.contextView !== "task" ||
      !state.selection.contextPanelOpen || state.selection.aiInteractionID === id
    ) return
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        aiInteractionID: id,
        aiInteraction: undefined,
        aiInteractionLoading: true,
        aiInteractionError: "",
      },
    }))
    void loadAIInteractionDetail(id)
  }

  function projectTaskIntent(task: Task, select: boolean) {
    detailGeneration += 1
    patch((current) => {
      const selected = current.selection.task?.id === task.id
      // Detail commands do not prove membership in the active filtered query.
      return {
        ...current,
        search: select ? { ...current.search, input: "" } : current.search,
        selection:
          select || selected
            ? {
                ...current.selection,
                task,
                taskGroup: select
                  ? (task.groupMembers && task.groupMembers.length > 1 ? task : undefined)
                  : task.state === "COMPLETED" ? undefined : current.selection.taskGroup,
                taskError: "",
                engagement: taskEngagement(task),
                aiInteractionID: "",
                aiInteraction: undefined,
                aiInteractionLoading: false,
                aiInteractionError: "",
                historicalCall: undefined,
                view: "engagement",
                contextView: "task",
                contextPanelOpen: select
                  ? true
                  : current.selection.contextPanelOpen,
              }
            : current.selection,
        detailRevision: current.detailRevision + 1,
      }
    })
    loadSelectedAppointmentEligibility()
  }

  async function openCallContext(callID: string) {
    const requestGeneration = ++detailGeneration
    const generation = scopeGeneration
    const result = await authenticatedRequest((token, signal) =>
      authority.call(token, callID, signal),
    )
    if (
      requestGeneration !== detailGeneration ||
      generation !== scopeGeneration ||
      stopped
    ) return
    if (failIfAccessLost(result) || result.kind !== "success") return
    focusedCallID = callID
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        task: undefined,
        taskError: "",
        aiInteractionID: "",
        aiInteraction: undefined,
        aiInteractionLoading: false,
        aiInteractionError: "",
        historicalCall: result.data,
        contextView: "call",
        contextPanelOpen: true,
      },
    }))
  }

  async function loadAIInteractionDetail(interactionID: string) {
    const requestGeneration = ++detailGeneration
    const generation = scopeGeneration
    const result = await authenticatedRequest((token, signal) =>
      authority.aiInteraction(token, interactionID, signal),
    )
    if (
      requestGeneration !== detailGeneration ||
      generation !== scopeGeneration ||
      stopped ||
      state.selection.aiInteractionID !== interactionID
    ) return
    if (failIfAccessLost(result)) return
    if (result.kind === "missing") {
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          aiInteractionID: current.selection.contextView === "task" ? interactionID : "",
          aiInteraction: undefined,
          aiInteractionLoading: false,
          aiInteractionError: current.selection.contextView === "task" ? aiInteractionDetailError : "",
          contextPanelOpen:
            current.selection.contextView === "ai-call"
              ? false
              : current.selection.contextPanelOpen,
        },
      }))
      return
    }
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        aiInteraction:
          result.kind === "success"
            ? result.data
            : current.selection.aiInteraction,
        aiInteractionLoading: false,
        aiInteractionError:
          result.kind === "success" ? "" : aiInteractionDetailError,
      },
    }))
  }

  function handleCallConnected(call: CallingCall) {
    if (call.id === focusedCallID) return
    detailGeneration += 1
    focusedCallID = call.id
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        task:
          current.selection.task?.phone === call.phone
            ? current.selection.task
            : undefined,
        engagement: callEngagement(call),
        aiInteractionID: "",
        aiInteraction: undefined,
        aiInteractionLoading: false,
        aiInteractionError: "",
        historicalCall: undefined,
        view: "engagement",
        contextView: "call",
        contextPanelOpen: true,
      },
    }))
  }

  async function handleDisposition(result: CallingDispositionResult) {
    focusedCallID = ""
    realtimeController.refresh()
    const generation = scopeGeneration
    if (result.taskId) {
      const taskResult = await authenticatedRequest((token, signal) =>
        authority.task(token, result.taskId!, signal),
      )
      if (generation !== scopeGeneration || stopped) return
      if (failIfAccessLost(taskResult)) return
      if (taskResult.kind === "success") {
        projectTaskIntent(taskResult.data, true)
        return
      }
    }
    const previous = [...state.tasks.items, ...state.completedTasks.items].find(
      (task) => task.id === returnTaskID,
    )
    const nextTask = previous ?? state.tasks.items[0]
    if (nextTask) {
      selectEngagement(taskEngagement(nextTask), nextTask)
      return
    }
    patch((current) => ({
      ...current,
      selection: { ...current.selection, contextPanelOpen: false },
    }))
  }

  function setCompletionFailure(taskID: string, error: string) {
    patch((current) => ({
      ...current,
      completion: { pendingTaskID: "", errorTaskID: taskID, error },
    }))
  }

  async function refreshTaskWindows(search: string, reset = false) {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const countGeneration = ++queryGenerations.taskCounts
    const taskGeneration = ++queryGenerations.tasks
    const completedGeneration = ++queryGenerations.completedTasks
    const scope = state.scope
    const rail = state.rail
    const taskDepth = reset ? 0 : state.tasks.items.length
    const completedDepth = reset ? 0 : state.completedTasks.items.length
    patch((current) => ({
      ...current,
      tasks: {
        ...current.tasks,
        ...(reset ? { items: [], nextCursor: "", counts: emptyTaskFolderCounts() } : {}),
        loading: true,
        error: "",
      },
      completedTasks: {
        ...current.completedTasks,
        ...(reset ? { items: [], nextCursor: "" } : {}),
        loading: true,
        error: "",
      },
    }))
    const result = await authenticatedRequest(async (token, signal) => {
      const [tasks, completedTasks] = await Promise.all([
        loadTaskWindow(token, taskQueryRequest(scope, search, rail), taskDepth, signal),
        loadTaskWindow(token, completedTaskQueryRequest(scope, search, rail), completedDepth, signal),
      ])
      if (
        tasks.kind === "unauthenticated" || tasks.kind === "unauthorized"
      ) return tasks
      if (
        completedTasks.kind === "unauthenticated" ||
        completedTasks.kind === "unauthorized"
      ) return completedTasks
      return {
        kind: "success" as const,
        data: { tasks, completedTasks },
      }
    })
    if (generation !== scopeGeneration || stopped) return
    if (failIfAccessLost(result)) return
    const tasks = requireTaskCounts(result.kind === "success"
      ? result.data.tasks
      : ({ kind: "unavailable" } as const))
    const completedTasks = result.kind === "success"
      ? result.data.completedTasks
      : ({ kind: "unavailable" } as const)
    patch((current) => ({
      ...current,
      tasks:
        taskGeneration !== queryGenerations.tasks
          ? current.tasks
          : tasks.kind === "success"
            ? {
                items: tasks.data.items,
                nextCursor: tasks.data.nextCursor,
                counts: countGeneration === queryGenerations.taskCounts
                  ? tasks.data.counts
                  : current.tasks.counts,
                loading: false,
                error: "",
              }
            : { ...current.tasks, loading: false, error: taskWindowError },
      completedTasks:
        completedGeneration !== queryGenerations.completedTasks
          ? current.completedTasks
          : completedTasks.kind === "success"
            ? {
                items: completedTasks.data.items,
                nextCursor: completedTasks.data.nextCursor,
                loading: false,
                error: "",
              }
            : {
                ...current.completedTasks,
                loading: false,
                error: completedWindowError,
              },
    }))
  }

  function obsoleteAllQueries() {
    queryGenerations.tasks += 1
    queryGenerations.taskCounts += 1
    queryGenerations.completedTasks += 1
  }

  function setWindowFailure(
    window: "tasks" | "completedTasks",
  ) {
    const error = window === "tasks" ? taskWindowError : completedWindowError
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: false, error },
    }))
  }

  async function loadTaskWindow(
    token: string,
    request: TaskQueryRequest,
    loadedCount: number,
    signal: AbortSignal,
  ): Promise<WorkspaceAuthorityResult<TaskPage>> {
    const target = refreshLoadedWindowTarget(loadedCount)
    const items: Task[] = []
    let cursor = ""
    let counts: TaskFolderCounts | undefined
    do {
      const result = await authority.tasks(
        token,
        { ...request, includeCounts: request.includeCounts !== false && !cursor, ...(cursor ? { cursor } : {}) },
        signal,
      )
      if (result.kind !== "success") return result
      if (!cursor) counts = result.data.counts
      items.push(...appendUniqueByID(items, result.data.items).slice(items.length))
      cursor = result.data.nextCursor
    } while (cursor && items.length < target)
    return { kind: "success", data: { items, nextCursor: cursor, counts } }
  }

  function stop() {
    stopped = true
    accessController?.abort()
    accessController = undefined
    realtimeController.stop()
    requestBudget?.stop()
    listeners.clear()
  }

  return {
    getSnapshot: () => state,
    subscribe(listener) {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    start,
    dispatch,
    stop,
  }
}

const railSections: WorkspaceRailSection[] = ["tasks", "calls", "appointments", "texts", "completed"]

const taskCategories: TaskCategoryFilter[] = [
  "all",
  "insurance", "pre_op", "post_op",
  "appointments",
  "documentation",
  "optical",
  "medication",
  "referrals",
  "other",
]

function emptyRailState(): WorkspaceRailState {
  return {
    expanded: ["tasks"],
    taskCategory: "all",
    scrollTop: 0,
  }
}

function railStorageKey(userSubject: string, practiceID: string) {
  return `acuity.attentionRail.${userSubject}.${practiceID}`
}

function restoreRailPreferences(
  preferences: WorkspacePreferences,
  userSubject: string,
  practiceID: string,
): WorkspaceRailState {
  try {
    const value = JSON.parse(
      preferences.read(railStorageKey(userSubject, practiceID)) ?? "",
    ) as Partial<WorkspaceRailState> & { version?: unknown }
    if (
      (value.version !== undefined && value.version !== 1) ||
      !Number.isFinite(value.scrollTop)
    ) {
      return emptyRailState()
    }
    const expanded = Array.isArray(value.expanded)
      ? value.expanded.filter((section): section is WorkspaceRailSection => railSections.includes(section))
      : []
    // Preserve the open channel when upgrading the former single-view sidebar.
    const formerCategory = String(value.taskCategory)
    if ((formerCategory === "texts" || formerCategory === "calls") && !expanded.includes(formerCategory)) {
      expanded.push(formerCategory)
    }
    return {
      expanded,
      ...(value.taskResponsibility === "mine" || value.taskResponsibility === "all" ? {taskResponsibility:value.taskResponsibility} : {}),
      taskCategory: taskCategories.includes(
        value.taskCategory as TaskCategoryFilter,
      )
        ? (value.taskCategory as TaskCategoryFilter)
        : "all",
      scrollTop: Math.max(0, value.scrollTop ?? 0),
    }
  } catch {
    return emptyRailState()
  }
}

function restoreAuthorizedScope(
  discovery: AccessDiscovery,
  preferences: WorkspacePreferences,
): WorkspaceScope | undefined {
  const storedPractice = preferences.read(practiceStorageKey)
  const practice =
    discovery.practices.find((item) => item.id === storedPractice) ??
    discovery.practices[0]
  if (!practice) return undefined
  const storedLocation = preferences.read(locationStorageKey)
  const location =
    practice.locations.find((item) => item.id === storedLocation) ??
    practice.locations[0]
  if (!location) return undefined
  const storedScope = preferences.read(`${taskScopeStorageKey}.${practice.id}`)
  const locationScopeID =
    practice.locations.length === 1
      ? location.id
      : practice.locations.some((item) => item.id === storedScope)
        ? (storedScope ?? "")
        : ""
  preferences.write(practiceStorageKey, practice.id)
  preferences.write(locationStorageKey, location.id)
  preferences.write(`${taskScopeStorageKey}.${practice.id}`, locationScopeID)
  return {
    practiceID: practice.id,
    locationID: location.id,
    locationScopeID,
  }
}

function taskQueryRequest(
  scope: WorkspaceScope,
  search: string,
  rail?: WorkspaceRailState,
): TaskQueryRequest {
  return {
    practiceId: scope.practiceID,
    ...(scope.locationScopeID ? { locationId: scope.locationScopeID } : {}),
    state: "OPEN",
    ordering: "recent",
    responsibility: rail?.taskResponsibility ?? "mine",
    grouped: true,
    kind: "follow_up",
    ...(rail?.taskCategory && rail.taskCategory !== "all" ? { category: rail.taskCategory } : {}),
    includeCounts: true,
    ...(search ? { search } : {}),
    limit: 50,
  }
}

function completedTaskQueryRequest(
  scope: WorkspaceScope,
  search: string,
  rail: WorkspaceRailState,
): TaskQueryRequest {
  return {
    ...taskQueryRequest(scope, search, rail),
    state: "COMPLETED",
    kind: undefined,
    ordering: "recent",
    grouped: false,
    includeCounts: false,
    limit: 10,
  }
}

function taskEngagement(task: Task): EngagementSummary {
  return {
    phone: task.phone,
    ...(task.callerName ? { displayName: task.callerName } : {}),
    locations: [{ id: task.locationId, name: task.locationName }],
    latestActivity: task.updatedAt,
    openTaskCount: task.state === "OPEN" ? 1 : 0,
    unread: task.unread,
  }
}

function callEngagement(call: CallingCall): EngagementSummary {
  return {
    phone: call.phone,
    ...(call.displayName ? { displayName: call.displayName } : {}),
    locations: [{ id: call.locationId, name: call.locationName }],
    latestActivity: call.connectedAt ?? new Date().toISOString(),
    openTaskCount: call.recoveryTask?.state === "OPEN" ? 1 : 0,
    unread: false,
  }
}

function newNumberEngagement(
  phone: string,
  locations: Array<{ id: string; name: string }>,
  locationScopeID: string,
): EngagementSummary {
  return {
    phone,
    locations: locationScopeID
      ? locations.filter((location) => location.id === locationScopeID)
      : locations,
    latestActivity: new Date().toISOString(),
    openTaskCount: 0,
    unread: false,
  }
}

function initialState(): WorkspaceProjectionState {
  return {
    loadState: "loading",
    connection: "connecting",
    scope: { practiceID: "", locationID: "", locationScopeID: "" },
    search: { input: "", applied: "", error: "" },
    tasks: {
      items: [],
      nextCursor: "",
      counts: emptyTaskFolderCounts(),
      loading: false,
      error: "",
    },
    completedTasks: { items: [], nextCursor: "", loading: false, error: "" },
    selection: {
      taskError: "",
      aiInteractionID: "",
      aiInteractionLoading: false,
      aiInteractionError: "",
      view: "none",
      contextView: "task",
      contextPanelOpen: false,
    },
    detailRevision: 0,
    completion: { pendingTaskID: "", errorTaskID: "", error: "" },
    rail: emptyRailState(),
  }
}

function emptyTaskFolderCounts(): TaskFolderCounts {
  return {
    tasks: 0,
    missedCalls: 0,
    categories: {
      billing: 0,
      appointments: 0,
      documentation: 0,
      optical: 0,
      medication: 0,
      referrals: 0,
      other: 0,
    },
  }
}

function refreshLoadedWindowTarget(loadedCount: number) {
  return Math.max(1, loadedCount)
}

const taskWindowError = "Tasks are temporarily unavailable."
const selectedTaskDetailError = "Task details are temporarily unavailable."
const completedWindowError = "Recently completed Tasks are temporarily unavailable."
const aiInteractionDetailError = "This AI call could not be loaded."

function requireTaskCounts(
  result: WorkspaceAuthorityResult<TaskPage>,
): WorkspaceAuthorityResult<TaskPage & { counts: TaskFolderCounts }> {
  if (result.kind !== "success") return result
  if (!result.data.counts) return { kind: "unavailable" }
  return { kind: "success", data: { ...result.data, counts: result.data.counts } }
}
