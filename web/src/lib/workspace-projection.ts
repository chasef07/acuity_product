import type {
  AccessDiscovery,
  AiInteractionDetail,
  CallingCall,
  CallingDispositionResult,
  PatientWorkCounts,
  PatientWorkFilter,
  PatientWorkPage,
  PatientWorkQueryRequest,
  PatientWorkRow,
  PatientWorkScope,
  PatientWorkTotals,
  StaffTaskCloseReason,
  Task,
  TaskPage,
  TaskQueryRequest,
  WorkspaceSnapshot,
} from "./api/generated/types.gen.ts"
import type { PortalFailure } from "./clients/portal-request.ts"
import { appendUniqueByID } from "./workspace-ordering.ts"
import {
  nextAfterClose,
  offersAppointmentChecks,
  patientWorkFilterShown,
} from "./patient-work.ts"
import { canViewPracticeAnalytics } from "./booking-analytics.ts"
import {
  selectedWorkspaceLocation,
  type WorkspaceLocation,
  workspaceLocationSearch,
  type WorkspacePageView,
} from "./workspace-location.ts"
import { patientWorkFilterLabels, patientWorkScopeLabels } from "./vocabulary.ts"
import { resolveWorkspaceSearch } from "./workspace-search.ts"
import {
  createWorkspaceRequestBudget,
  type WorkspaceRequestBudget,
} from "./workspace-sync/workspace-request-budget.ts"

type WorkspaceLoadState =
  | "loading"
  | "ready"
  | "unauthenticated"
  | "unauthorized"
  | "unavailable"

type WorkspaceConnectionState =
  | "connecting"
  | "connected"
  | "degraded"

export type WorkspaceView = "none" | "engagement" | "analytics" | "operator-analytics" | "manage-agent"
type WorkspaceContextView = "task" | "call" | "ai-call" | "patient-tasks"
export type ManageAgentPage = "home" | "transcripts" | "knowledge" | "insurance"

export function isWorkView(view: WorkspaceView) {
  return view === "none" || view === "engagement"
}

type WorkspaceRailState = {
  scope: PatientWorkScope
  filter: PatientWorkFilter
  completedExpanded: boolean
  scrollTop: number
}

export type EngagementSummary = {
  phone: string
  locations: Array<{ id: string; name: string }>
}

type WorkspaceScope = {
  practiceID: string
  locationID: string
  locationScopeID: string
}

type WorkspaceQueryWindow<T> = {
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
  patients: WorkspaceQueryWindow<PatientWorkRow> & {
    counts: PatientWorkCounts
    totals: PatientWorkTotals
  }
  completedTasks: WorkspaceQueryWindow<Task>
  selection: {
    task?: Task
    taskError: string
    engagement?: EngagementSummary
    aiInteractionID: string
    aiInteraction?: AiInteractionDetail
    aiInteractionLoading: boolean
    aiInteractionError: string
    historicalCall?: CallingCall
    view: WorkspaceView
    agentPage: ManageAgentPage
    contextView: WorkspaceContextView
    contextPanelOpen: boolean
  }
  detailRevision: number
  completion: {
    pending: readonly string[]
    errors: Readonly<Record<string, string>>
  }
  rail: WorkspaceRailState
}

type WorkspaceAuthentication =
  | { status: "authenticated"; token: string }
  | { status: "unauthenticated" | "unavailable" }

export type WorkspaceAuthorityResult<T> =
  | { kind: "success"; data: T }
  | PortalFailure

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
  patients: (
    token: string,
    request: PatientWorkQueryRequest,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<PatientWorkPage>>
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
    reason: StaffTaskCloseReason,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<Task>>
}

type WorkspaceWindow = "patients" | "completedTasks"

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

type WorkspaceRealtimeController = {
  setScope: (scope?: { practiceID: string; locationID: string }) => void
  refresh: () => Promise<void>
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

export type WorkspaceNavigation = {
  read: () => WorkspaceLocation
  write: (location: WorkspaceLocation, mode: "push" | "replace") => void
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
      window: WorkspaceWindow
    }
  | { type: "set-search"; value: string }
  | { type: "submit-search" }
  | { type: "complete-task"; task: Task; reason: StaffTaskCloseReason; advance?: boolean }
  | { type: "select-task"; task: Task; rememberForCall?: boolean }
  | { type: "select-analytics" }
  | { type: "select-operator-analytics" }
  | { type: "select-manage-agent"; page?: ManageAgentPage }
  | { type: "select-work" }
  | { type: "open-ai-context"; interactionID: string }
  | { type: "open-task-context"; task: Task }
  | { type: "open-call-context"; callID: string }
  | { type: "open-patient-tasks" }
  | { type: "close-context" }
  | { type: "context-transition-ended" }
  | { type: "task-committed"; task: Task; advance?: boolean }
  | { type: "task-created"; task: Task }
  | { type: "visibility-changed" }
  | { type: "retry" }
  | { type: "call-connected"; call: CallingCall }
  | { type: "remember-return-task"; taskID: string }
  | { type: "return-to-call" }
  | { type: "call-disposition"; result: CallingDispositionResult }
  | { type: "toggle-completed" }
  | { type: "set-rail-view"; scope?: PatientWorkScope; filter?: PatientWorkFilter }
  | { type: "remember-rail-scroll"; scrollTop: number }
  | { type: "navigation-changed" }

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
  navigation,
  environment,
}: {
  authority: WorkspaceAuthorityAdapter
  realtime: WorkspaceRealtimeAdapter
  preferences: WorkspacePreferences
  navigation?: WorkspaceNavigation
  environment?: WorkspaceProjectionEnvironment
}): WorkspaceProjection {
  let state = initialState()
  let stopped = false
  let scopeGeneration = 0
  let detailGeneration = 0
  let returnTaskID = ""
  let focusedCallID = ""
  let firstLoad = true
  let railScrollTop = 0
  let navigationSearch = ""
  let navigationGeneration = 0
  let replaceNavigation = false
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
    patients: 0,
    completedTasks: 0,
  }
  const windowLoads: Partial<Record<WorkspaceWindow, { query: number; depth: number }>> = {}
  let scopeRequests = new AbortController()
  let accessController: AbortController | undefined
  const listeners = new Set<() => void>()

  function publish(next: WorkspaceProjectionState) {
    if (stopped) return
    state = next
    requestBudget?.setDetailRefreshMounted(
      next.selection.view === "engagement" &&
        Boolean(next.selection.engagement),
    )
    syncNavigation(next)
    for (const listener of listeners) listener()
  }

  function syncNavigation(next: WorkspaceProjectionState) {
    if (!navigation || next.loadState !== "ready") return
    const mode = replaceNavigation ? "replace" : "push"
    replaceNavigation = false
    const location = selectedWorkspaceLocation(next.selection)
    const search = workspaceLocationSearch(location)
    if (search === navigationSearch || (location.view === "work" && navigationSearch.startsWith("?task="))) return
    navigationSearch = search
    navigation.write(location, mode)
  }

  function patch(
    update: (current: WorkspaceProjectionState) => WorkspaceProjectionState,
  ) {
    publish(update(state))
  }

  function obsoleteScope() {
    scopeGeneration += 1
    firstLoad = true
    scopeRequests.abort()
    scopeRequests = new AbortController()
    delete windowLoads.patients
    delete windowLoads.completedTasks
  }

  function failClosed(
    loadState: "unauthenticated" | "unauthorized" | "unavailable",
  ) {
    obsoleteScope()
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
    signal = scopeRequests.signal,
  ): Promise<WorkspaceAuthorityResult<T>> {
    const authentication = await authority.authenticate()
    switch (authentication.status) {
      case "unauthenticated":
        return { kind: "unauthenticated", retryable: false }
      case "unavailable":
        return { kind: "unavailable", retryable: true }
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
  }: Parameters<WorkspaceRealtimeCallbacks["reconcile"]>[0]): Promise<Reconciliation> {
    const generation = scopeGeneration
    const patientGeneration = ++queryGenerations.patients
    const completedGeneration = ++queryGenerations.completedTasks
    const current = state
    const selectedTaskID = current.selection.task?.id
    const selectedAIInteractionID = current.selection.aiInteractionID
    const patientDepth = windowDepth("patients", current.patients.items.length)
    const [
      snapshotResult,
      requestedPatients,
      completedResult,
      selectedTaskResult,
      selectedAIResult,
    ] =
      await Promise.all([
        authority.workspace(token, scope, signal),
        loadPatientWindow(token, patientQueryRequest(current.scope, current.search.applied, current.rail), patientDepth, signal),
        loadTaskWindow(token, completedTaskQueryRequest(current.scope, current.search.applied, current.rail), windowDepth("completedTasks", current.completedTasks.items.length), signal),
        selectedTaskID
          ? authority.task(token, selectedTaskID, signal)
          : Promise.resolve(undefined),
        selectedAIInteractionID
          ? authority.aiInteraction(token, selectedAIInteractionID, signal)
          : Promise.resolve(undefined),
      ])

    const storedFilterHidden = firstLoad && !current.search.applied && requestedPatients.kind === "success" &&
      !patientWorkFilterShown(
        current.rail.filter,
        requestedPatients.data.counts,
        offersAppointmentChecks(
          current.discovery?.practices.find((practice) => practice.id === scope.practiceID)?.locations ?? [],
          current.scope.locationScopeID,
        ),
      )
    const patientResult = storedFilterHidden
      ? await loadPatientWindow(token, patientQueryRequest(current.scope, current.search.applied, { ...current.rail, filter: "all" }), patientDepth, signal)
      : requestedPatients
    if (
      stopped || signal.aborted || generation !== scopeGeneration ||
      state.scope.practiceID !== scope.practiceID ||
      state.scope.locationID !== scope.locationID
    ) {
      return { version: minimumVersion, apply: () => {} }
    }

    const authorityResults = [
      snapshotResult,
      patientResult,
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
        const selectsFirstPatient = firstLoad
        if (patientGeneration === queryGenerations.patients && patientResult.kind === "success") {
          firstLoad = false
          if (storedFilterHidden && state.rail.filter === current.rail.filter) {
            updateRail((rail) => ({ ...rail, filter: "all" }))
          }
        }
        patch((currentState) => {
          const patientWindowCurrent =
            patientGeneration === queryGenerations.patients
          const completedWindowCurrent = completedGeneration === queryGenerations.completedTasks
          const refreshedSelected =
            patientWindowCurrent && selectedTaskResult?.kind === "success" &&
            !(currentState.selection.task?.id === selectedTaskResult.data.id &&
              currentState.selection.task.version > selectedTaskResult.data.version)
              ? selectedTaskResult.data
              : undefined
          const patients = patientWindowCurrent && patientResult.kind === "success"
            ? patientResult.data.rows.map((row) =>
                refreshedSelected?.state === "OPEN" && row.top.id === refreshedSelected.id
                  ? { ...row, top: refreshedSelected } : row)
            : currentState.patients.items
          const selectionStillMatches = patientWindowCurrent &&
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
                taskError: "",
                engagement: taskEngagement(refreshedSelected),
              }
            } else if (selectedTaskResult && selectedTaskResult.kind !== "success") {
              selection = {
                ...selection,
                taskError: selectedTaskDetailError,
              }
            }
          } else if (patientWindowCurrent && selectsFirstPatient &&
            !currentState.selection.task &&
            currentState.selection.view === "none" &&
            patients[0]
          ) {
            const firstTask = patients[0].top
            selection = {
              ...selection,
              task: firstTask,
              taskError: "",
              engagement: taskEngagement(firstTask),
              view: "engagement",
              contextView: "task",
              contextPanelOpen: false,
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
          let patientWindow = currentState.patients
          if (patientWindowCurrent) {
            patientWindow = patientResult.kind === "success"
              ? {
                  items: patients,
                  nextCursor: patientResult.data.nextCursor,
                  counts: patientResult.data.counts,
                  totals: patientResult.data.totals,
                  loading: false,
                  error: "",
                }
              : { ...patientWindow, loading: false, error: patientWindowError }
          }
          return {
            ...currentState,
            loadState: "ready",
            workspace: snapshot,
            patients: patientWindow,
            completedTasks: completedWindowCurrent
              ? completedResult.kind === "success"
                ? { items: completedResult.data.items, nextCursor: completedResult.data.nextCursor, loading: false, error: "" }
                : { ...currentState.completedTasks, loading: false, error: completedWindowError }
              : currentState.completedTasks,
            selection,
          }
        })
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
    const requested = navigation?.read() ?? { view: "work" }
    navigationGeneration += 1
    navigationSearch = workspaceLocationSearch(requested)
    replaceNavigation = true
    const restored = requested.view === "task"
      ? await authority.task(authentication.token, requested.taskID, controller.signal)
      : undefined
    if (controller.signal.aborted) return
    if (restored?.kind === "unauthenticated") {
      failClosed("unauthenticated")
      return
    }
    if (restored && !settledTaskRead(restored)) {
      failClosed("unavailable")
      return
    }
    const restoredTask = restored?.kind === "success" ? restored.data : undefined
    const scope = restoreAuthorizedScope(result.data, preferences, restoredTask?.practiceId)
    if (!scope) {
      failClosed("unauthorized")
      return
    }
    const activeScopeChanged =
      scope.practiceID !== state.scope.practiceID ||
      scope.locationID !== state.scope.locationID
    obsoleteScope()
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
    if (restoredTask?.practiceId === scope.practiceID) {
      showTask(restoredTask)
    } else {
      selectLocation(requested)
    }
    realtimeController.setScope({
      practiceID: scope.practiceID,
      locationID: scope.locationID,
    })
    if (!activeScopeChanged) void realtimeController.refresh()
  }

  async function restoreNavigation() {
    if (!navigation) return
    if (state.loadState === "loading" && accessController) {
      await start()
      return
    }
    if (state.loadState !== "ready") return
    const location = navigation.read()
    const generation = ++navigationGeneration
    const departedSearch = navigationSearch
    const selected = location.view === "task" &&
      state.selection.view === "engagement" && state.selection.task?.id === location.taskID
    const result = location.view === "task" && !selected
      ? await authenticatedRequest((token, signal) => authority.task(token, location.taskID, signal))
      : undefined
    if (generation !== navigationGeneration || navigationSearch !== departedSearch || stopped) return
    if (result?.kind === "unauthenticated") {
      failClosed("unauthenticated")
      return
    }
    if (result && !settledTaskRead(result)) {
      failClosed("unavailable")
      return
    }
    navigationSearch = workspaceLocationSearch(location)
    replaceNavigation = true
    if (result?.kind === "success") showTask(result.data)
    else selectLocation(location)
    syncNavigation(state)
  }

  function showTask(task: Task) {
    const discovery = state.discovery
    if (!discovery) return
    if (task.practiceId !== state.scope.practiceID) {
      const scope = restoreAuthorizedScope(discovery, preferences, task.practiceId)
      if (scope?.practiceID !== task.practiceId) return
      selectScope({ type: "select-scope", practiceID: scope.practiceID, locationScopeID: scope.locationScopeID })
    }
    selectEngagement(taskEngagement(task), task)
  }

  function selectLocation(location: WorkspaceLocation) {
    if (location.view === "task") return
    if (location.view === "work") {
      selectWork()
      return
    }
    selectPageView(location.view, location.view === "manage-agent" ? (location.page ?? "home") : undefined)
  }

  function selectWork() {
    const first = !state.selection.engagement && state.loadState === "ready" && !state.patients.error
      ? state.patients.items[0]?.top
      : undefined
    if (first) {
      selectEngagement(taskEngagement(first), first)
      return
    }
    patch(current => ({ ...current, selection: { ...current.selection, view: current.selection.engagement ? "engagement" : "none" } }))
  }

  function selectPageView(view: WorkspacePageView, agentPage?: ManageAgentPage) {
    const discovery = state.discovery
    if (!discovery?.practices.some((practice) => practice.id === state.scope.practiceID)) return
    if (view === "analytics" && !canViewPracticeAnalytics(discovery, state.scope.practiceID)) return
    if (view === "operator-analytics" && !discovery.platformOperator) return
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        view,
        agentPage: agentPage ?? current.selection.agentPage,
        contextPanelOpen: false,
      },
    }))
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
      await completeTaskIntent(intent.task, intent.reason, intent.advance === true)
      return
    }
    if (intent.type === "select-task") {
      if (intent.rememberForCall) returnTaskID = intent.task.id
      selectEngagement(taskEngagement(intent.task), intent.task)
      return
    }
    if (intent.type === "select-work") {
      selectWork()
      return
    }
    if (intent.type === "select-manage-agent") {
      selectPageView("manage-agent", intent.page)
      return
    }
    if (intent.type === "select-analytics") {
      selectPageView("analytics")
      return
    }
    if (intent.type === "select-operator-analytics") {
      selectPageView("operator-analytics")
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
    if (intent.type === "open-patient-tasks") {
      detailGeneration += 1
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          contextView: "patient-tasks",
          contextPanelOpen: true,
        },
      }))
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
      const ownsSelection = selectedID === intent.task.id
      const before = state.patients.items
      queryGenerations.patients += 1
      projectTaskIntent(intent.task, false)
      await realtimeController.refresh()
      if (intent.advance && intent.task.state === "COMPLETED" && ownsSelection && generation === scopeGeneration && state.selection.task?.id === selectedID && !state.patients.error) {
        advanceAfterClose(before, intent.task.phone, [intent.task.id])
      }
      return
    }
    if (intent.type === "task-created") {
      queryGenerations.patients += 1
      projectTaskIntent(intent.task, false)
      await realtimeController.refresh()
      return
    }
    if (intent.type === "visibility-changed") {
      realtimeController.visibilityChanged()
      return
    }
    if (intent.type === "retry") {
      if (state.discovery && state.scope.practiceID && state.scope.locationID) {
        void realtimeController.refresh()
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
    if (intent.type === "toggle-completed") {
      updateRail((rail) => ({ ...rail, completedExpanded: !rail.completedExpanded }))
      return
    }
    if (intent.type === "set-rail-view") {
      updateRail((rail) => ({
        ...rail,
        scope: intent.scope ?? rail.scope,
        filter: intent.filter ?? rail.filter,
      }))
      await refreshTaskWindows(state.search.applied)
      return
    }
    if (intent.type === "remember-rail-scroll") {
      railScrollTop = Math.max(0, intent.scrollTop)
      persistRail({ ...state.rail, scrollTop: railScrollTop })
      return
    }
    if (intent.type === "navigation-changed") {
      await restoreNavigation()
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
      JSON.stringify({ version: railPreferencesVersion, ...rail }),
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
    obsoleteScope()
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
      : current.selection.view === "operator-analytics" && discovery.platformOperator ? "operator-analytics"
      : current.selection.view === "manage-agent" ? "manage-agent" : "none"
    publish({
      ...empty,
      selection: { ...empty.selection, view: analyticsView, agentPage: current.selection.agentPage },
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
    if (!activeScopeChanged) void realtimeController.refresh()
  }

  async function loadMore(window: WorkspaceWindow) {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const currentWindow = state[window]
    if (currentWindow.loading || !currentWindow.nextCursor) return
    const queryGeneration = ++queryGenerations[window]
    windowLoads[window] = { query: queryGeneration, depth: currentWindow.items.length + 1 }
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: true, error: "" },
    }))
    if (window === "patients") {
      const result = await authenticatedRequest((token, signal) =>
        authority.patients(
          token,
          { ...patientQueryRequest(state.scope, state.search.applied, state.rail), cursor: currentWindow.nextCursor },
          signal,
        ),
      )
      if (!settledWindowLoad(window, generation, queryGeneration, result)) return
      patch((current) => ({
        ...current,
        patients: {
          items: appendUniquePatients(current.patients.items, result.data.rows),
          nextCursor: result.data.nextCursor,
          counts: result.data.counts,
          totals: result.data.totals,
          loading: false,
          error: "",
        },
      }))
      return
    }
    const result = await authenticatedRequest((token, signal) =>
      authority.tasks(
        token,
        { ...completedTaskQueryRequest(state.scope, state.search.applied, state.rail), cursor: currentWindow.nextCursor },
        signal,
      ),
    )
    if (!settledWindowLoad(window, generation, queryGeneration, result)) return
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

  function settledWindowLoad<T>(
    window: WorkspaceWindow,
    generation: number,
    queryGeneration: number,
    result: WorkspaceAuthorityResult<T>,
  ): result is { kind: "success"; data: T } {
    if (generation !== scopeGeneration || stopped) return false
    settleWindowLoad(window, queryGeneration)
    if (queryGeneration !== queryGenerations[window]) return false
    if (failIfAccessLost(result)) return false
    if (result.kind !== "success") {
      setWindowFailure(window)
      return false
    }
    return true
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
      await refreshTaskWindows("")
      return
    }
    patch((current) => ({
      ...current,
      search: { ...current.search, applied: resolved.value, error: "" },
    }))
    await refreshTaskWindows(resolved.value)
  }

  async function completeTaskIntent(task: Task, reason: StaffTaskCloseReason, advance: boolean) {
    if (state.completion.pending.includes(task.id) || task.state !== "OPEN") return
    const generation = scopeGeneration
    const before = state.patients.items
    patch((current) => ({
      ...current,
      completion: {
        pending: [...current.completion.pending, task.id],
        errors: withoutKey(current.completion.errors, task.id),
      },
    }))
    const result = await authenticatedRequest((token, signal) =>
      authority.completeTask(token, task, reason, signal),
    )
    if (generation !== scopeGeneration || stopped) return
    if (failIfAccessLost(result)) return
    if (result.kind !== "success") {
      setCompletionFailure(
        task.id,
        result.kind === "conflict"
          ? "This Task changed elsewhere. Open it to review the latest state, then retry."
          : result.kind === "unavailable" && result.status === undefined
            ? "Task completion is temporarily unavailable. Retry in a moment."
            : "This Task could not be completed. Retry from the row or open its details.",
      )
      return
    }
    queryGenerations.patients += 1
    projectTaskIntent(result.data, false)
    await realtimeController.refresh()
    if (generation !== scopeGeneration || stopped || state.loadState !== "ready") return
    patch((current) => ({
      ...current,
      completion: {
        ...current.completion,
        pending: current.completion.pending.filter((id) => id !== task.id),
      },
    }))
    if (advance && state.selection.task?.id === task.id && !state.patients.error) {
      advanceAfterClose(before, task.phone, [task.id])
    }
  }

  function openTasks() {
    return state.patients.items.map((row) => row.top)
  }

  function advanceAfterClose(
    before: readonly PatientWorkRow[],
    phone: string,
    closedTaskIDs: readonly string[],
  ) {
    const next = nextAfterClose({ before, after: state.patients.items, phone, closedTaskIDs })
    if (next && next.phone === phone && state.selection.contextView === "patient-tasks") {
      patch((current) => ({
        ...current,
        selection: { ...current.selection, task: next, taskError: "", engagement: taskEngagement(next) },
      }))
      return
    }
    if (next) {
      selectEngagement(taskEngagement(next), next)
      return
    }
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        task: undefined,
        taskError: "",
        engagement: undefined,
        historicalCall: undefined,
        view: "none",
        contextView: "task",
        contextPanelOpen: false,
      },
    }))
  }

  function selectEngagement(engagement: EngagementSummary, task: Task) {
    patch((current) => ({
      ...current,
      selection: {
        ...current.selection,
        task,
        taskError: "",
        engagement,
        aiInteractionID: "",
        aiInteraction: undefined,
        aiInteractionLoading: false,
        aiInteractionError: "",
        historicalCall: undefined,
        view: "engagement",
        contextView: "task",
        contextPanelOpen: true,
      },
    }))
  }

  function projectTaskIntent(task: Task, select: boolean) {
    detailGeneration += 1
    patch((current) => ({
      ...current,
      search: select ? { ...current.search, input: "" } : current.search,
      selection: select
        ? {
            ...current.selection,
            task,
            taskError: "",
            engagement: taskEngagement(task),
            aiInteractionID: "",
            aiInteraction: undefined,
            aiInteractionLoading: false,
            aiInteractionError: "",
            historicalCall: undefined,
            view: "engagement",
            contextView: "task",
            contextPanelOpen: true,
          }
        : current.selection.task?.id === task.id
          ? {
              ...current.selection,
              task,
              taskError: "",
            }
          : current.selection,
      detailRevision: current.detailRevision + 1,
    }))
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
          aiInteractionID: "",
          aiInteraction: undefined,
          aiInteractionLoading: false,
          aiInteractionError: "",
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
    const before = state.patients.items
    const phone = state.selection.engagement?.phone
    const closedTaskIDs = [...(result.closedTaskIds ?? []), ...(result.alreadyClosedTaskIds ?? [])]
    const refreshed = realtimeController.refresh()
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
    const closedByWrapUp = closedTaskIDs.length > 0
    if (closedByWrapUp) {
      await refreshed
      if (generation !== scopeGeneration || stopped) return
    }
    const previous = [...openTasks(), ...state.completedTasks.items].find(
      (task) => task.id === returnTaskID && !closedTaskIDs.includes(task.id),
    )
    if (!previous && phone && closedByWrapUp && !state.patients.error) {
      advanceAfterClose(before, phone, closedTaskIDs)
      return
    }
    const nextTask = previous ?? openTasks()[0]
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
      completion: {
        pending: current.completion.pending.filter((id) => id !== taskID),
        errors: { ...current.completion.errors, [taskID]: error },
      },
      detailRevision: current.detailRevision + 1,
    }))
  }

  async function refreshTaskWindows(search: string) {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const patientGeneration = ++queryGenerations.patients
    const completedGeneration = ++queryGenerations.completedTasks
    const scope = state.scope
    const rail = state.rail
    windowLoads.patients = { query: patientGeneration, depth: 0 }
    windowLoads.completedTasks = { query: completedGeneration, depth: 0 }
    patch((current) => ({
      ...current,
      patients: {
        ...current.patients,
        items: [],
        nextCursor: "",
        loading: true,
        error: "",
      },
      completedTasks: {
        ...current.completedTasks,
        items: [],
        nextCursor: "",
        loading: true,
        error: "",
      },
    }))
    const result = await authenticatedRequest(async (token, signal) => {
      const [patients, completedTasks] = await Promise.all([
        loadPatientWindow(token, patientQueryRequest(scope, search, rail), 0, signal),
        loadTaskWindow(token, completedTaskQueryRequest(scope, search, rail), 0, signal),
      ])
      if (
        patients.kind === "unauthenticated" || patients.kind === "unauthorized"
      ) return patients
      if (
        completedTasks.kind === "unauthenticated" ||
        completedTasks.kind === "unauthorized"
      ) return completedTasks
      return {
        kind: "success" as const,
        data: { patients, completedTasks },
      }
    })
    if (generation !== scopeGeneration || stopped) return
    settleWindowLoad("patients", patientGeneration)
    settleWindowLoad("completedTasks", completedGeneration)
    if (failIfAccessLost(result)) return
    const patients = result.kind === "success"
      ? result.data.patients
      : ({ kind: "unavailable", retryable: true } as const)
    const completedTasks = result.kind === "success"
      ? result.data.completedTasks
      : ({ kind: "unavailable", retryable: true } as const)
    patch((current) => ({
      ...current,
      patients:
        patientGeneration !== queryGenerations.patients
          ? current.patients
          : patients.kind === "success"
            ? {
                items: patients.data.rows,
                nextCursor: patients.data.nextCursor,
                counts: patients.data.counts,
                totals: patients.data.totals,
                loading: false,
                error: "",
              }
            : { ...current.patients, loading: false, error: patientWindowError },
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

  function windowDepth(window: WorkspaceWindow, loadedCount: number) {
    return Math.max(loadedCount, windowLoads[window]?.depth ?? 0)
  }

  function settleWindowLoad(window: WorkspaceWindow, query: number) {
    if (windowLoads[window]?.query !== query) return
    delete windowLoads[window]
    if (query === queryGenerations[window] || !state[window].loading) return
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: false },
    }))
  }

  function obsoleteAllQueries() {
    queryGenerations.patients += 1
    queryGenerations.completedTasks += 1
  }

  function setWindowFailure(window: WorkspaceWindow) {
    const error = window === "patients" ? patientWindowError : completedWindowError
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: false, error },
    }))
  }

  async function loadPatientWindow(
    token: string,
    request: PatientWorkQueryRequest,
    loadedCount: number,
    signal: AbortSignal,
  ): Promise<WorkspaceAuthorityResult<PatientWorkPage>> {
    const target = refreshLoadedWindowTarget(loadedCount)
    const first = await authority.patients(token, request, signal)
    if (first.kind !== "success") return first
    let rows = first.data.rows
    let cursor = first.data.nextCursor
    while (cursor && rows.length < target) {
      const result = await authority.patients(token, { ...request, cursor }, signal)
      if (result.kind !== "success") return result
      rows = appendUniquePatients(rows, result.data.rows)
      cursor = result.data.nextCursor
    }
    return { kind: "success", data: { ...first.data, rows, nextCursor: cursor } }
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
    do {
      const result = await authority.tasks(
        token,
        { ...request, ...(cursor ? { cursor } : {}) },
        signal,
      )
      if (result.kind !== "success") return result
      items.push(...appendUniqueByID(items, result.data.items).slice(items.length))
      cursor = result.data.nextCursor
    } while (cursor && items.length < target)
    return { kind: "success", data: { items, nextCursor: cursor } }
  }

  function stop() {
    stopped = true
    scopeRequests.abort()
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

const railPreferencesVersion = 2

function emptyRailState(): WorkspaceRailState {
  return {
    scope: "mine",
    filter: "all",
    completedExpanded: false,
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
      value.version !== railPreferencesVersion ||
      !Object.hasOwn(patientWorkScopeLabels, String(value.scope)) ||
      !Object.hasOwn(patientWorkFilterLabels, String(value.filter)) ||
      !Number.isFinite(value.scrollTop)
    ) {
      return emptyRailState()
    }
    return {
      scope: value.scope as PatientWorkScope,
      filter: value.filter as PatientWorkFilter,
      completedExpanded: value.completedExpanded === true,
      scrollTop: Math.max(0, value.scrollTop ?? 0),
    }
  } catch {
    return emptyRailState()
  }
}

function withoutKey(record: Readonly<Record<string, string>>, key: string) {
  return Object.fromEntries(Object.entries(record).filter(([name]) => name !== key))
}

function settledTaskRead(result: WorkspaceAuthorityResult<Task>) {
  return result.kind === "success" || result.kind === "missing" || result.kind === "unauthorized"
}

function restoreAuthorizedScope(
  discovery: AccessDiscovery,
  preferences: WorkspacePreferences,
  requestedPracticeID?: string,
): WorkspaceScope | undefined {
  const storedPractice = preferences.read(practiceStorageKey)
  const practice =
    discovery.practices.find((item) => item.id === requestedPracticeID) ??
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

function patientQueryRequest(
  scope: WorkspaceScope,
  search: string,
  rail: WorkspaceRailState,
): PatientWorkQueryRequest {
  return {
    practiceId: scope.practiceID,
    ...(scope.locationScopeID ? { locationId: scope.locationScopeID } : {}),
    scope: rail.scope,
    filter: rail.filter,
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
    practiceId: scope.practiceID,
    ...(scope.locationScopeID ? { locationId: scope.locationScopeID } : {}),
    state: "COMPLETED",
    ordering: "recent",
    responsibility: rail.scope === "mine" ? "mine" : "all",
    includeCounts: false,
    ...(search ? { search } : {}),
    limit: 10,
  }
}

function appendUniquePatients(current: PatientWorkRow[], incoming: PatientWorkRow[]) {
  const phones = new Set(current.map((row) => row.phone))
  return [...current, ...incoming.filter((row) => !phones.has(row.phone))]
}

export function currentPractice(projection: WorkspaceProjectionState) {
  const discovery = projection.discovery!
  return (
    discovery.practices.find(
      (item) => item.id === projection.scope.practiceID,
    ) ?? discovery.practices[0]!
  )
}

function taskEngagement(task: Task): EngagementSummary {
  return {
    phone: task.phone,
    locations: [{ id: task.locationId, name: task.locationName }],
  }
}

function callEngagement(call: CallingCall): EngagementSummary {
  return {
    phone: call.phone,
    locations: [{ id: call.locationId, name: call.locationName }],
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
  }
}

function initialState(): WorkspaceProjectionState {
  return {
    loadState: "loading",
    connection: "connecting",
    scope: { practiceID: "", locationID: "", locationScopeID: "" },
    search: { input: "", applied: "", error: "" },
    patients: emptyPatientWindow(),
    completedTasks: { items: [], nextCursor: "", loading: false, error: "" },
    selection: {
      taskError: "",
      aiInteractionID: "",
      aiInteractionLoading: false,
      aiInteractionError: "",
      view: "none",
      agentPage: "home",
      contextView: "task",
      contextPanelOpen: false,
    },
    detailRevision: 0,
    completion: { pending: [], errors: {} },
    rail: emptyRailState(),
  }
}

function emptyPatientWindow(): WorkspaceProjectionState["patients"] {
  return {
    items: [],
    nextCursor: "",
    counts: { all: 0, followUp: 0, callBack: 0, texts: 0, appointments: 0, unowned: 0 },
    totals: { openTasks: 0, patients: 0, patientsOutsideScope: 0 },
    loading: false,
    error: "",
  }
}

function refreshLoadedWindowTarget(loadedCount: number) {
  return Math.max(1, loadedCount)
}

const patientWindowError = "Open work is temporarily unavailable."
const selectedTaskDetailError = "Task details are temporarily unavailable."
const completedWindowError = "Recently completed Tasks are temporarily unavailable."
const aiInteractionDetailError = "This AI call could not be loaded."
