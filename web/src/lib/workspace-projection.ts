import type {
  AccessDiscovery,
  AiOutcomeCounts,
  AiInteractionDetail,
  AiOutcomeItem,
  AiOutcomePage,
  AiOutcomeQueryRequest,
  CallingCall,
  CallingDispositionResult,
  EngagementSummary,
  MessageThreadPage,
  MessageThreadQueryRequest,
  MessageThreadSummary,
  Task,
  TaskFolderCounts,
  TaskPage,
  TaskQueryRequest,
  WorkspaceSnapshot,
} from "./api/generated/types.gen.ts"
import {
  applyOutcomePages,
  appointmentActionForFolder,
  categorizeAIOutcomes,
  appointmentOutcomeFolderKeys,
  type AppointmentOutcomeCursors,
  type AppointmentOutcomeFolder,
  decrementOutcomeCount,
  emptyAppointmentOutcomeCursors,
} from "./ai-outcome-attention.ts"
import { appendUniqueByID } from "./workspace-ordering.ts"
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

export type WorkspaceView = "none" | "engagement" | "analytics"
export type WorkspaceContextView = "task" | "call" | "ai-call"
export type WorkspaceRailSection =
  | "tasks"
  | "calls"
  | "appointments"
  | "texts"

export type WorkspaceRailState = {
  expanded: WorkspaceRailSection[]
  expandedAppointments: AppointmentOutcomeFolder[]
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
  recoveryTasks: WorkspaceQueryWindow<Task>
  messages: WorkspaceQueryWindow<MessageThreadSummary>
  aiOutcomes: WorkspaceQueryWindow<AiOutcomeItem> & {
    counts: AiOutcomeCounts
    nextCursors: AppointmentOutcomeCursors
  }
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
  messageThreads: (
    token: string,
    request: MessageThreadQueryRequest,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<MessageThreadPage>>
  aiOutcomes: (
    token: string,
    request: AiOutcomeQueryRequest,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<AiOutcomePage>>
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
  reviewAIOutcome: (
    token: string,
    interactionID: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<unknown>>
  markMessageThreadRead: (
    token: string,
    threadID: string,
    signal: AbortSignal,
  ) => Promise<WorkspaceAuthorityResult<unknown>>
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
  isHidden: () => boolean
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
  reviewAIOutcome: (interactionID: string) => Promise<boolean>
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
      window: "tasks" | "recoveryTasks" | "messages"
    }
  | {
      type: "load-more-outcomes"
      folder: AppointmentOutcomeFolder
    }
  | { type: "set-search"; value: string }
  | { type: "submit-search" }
  | { type: "complete-task"; task: Task }
  | { type: "select-engagement"; engagement: EngagementSummary }
  | { type: "select-task"; task: Task; rememberForCall?: boolean }
  | { type: "select-ai-interaction"; interaction: AiOutcomeItem }
  | { type: "select-analytics" }
  | { type: "open-ai-context"; interactionID: string }
  | { type: "open-task-context"; task: Task }
  | { type: "open-call-context"; callID: string }
  | { type: "close-context" }
  | { type: "context-transition-ended" }
  | { type: "task-committed"; task: Task }
  | { type: "task-created"; task: Task }
  | { type: "visibility-changed" }
  | { type: "retry" }
  | { type: "call-connected"; call: CallingCall }
  | { type: "remember-return-task"; taskID: string }
  | { type: "return-to-call" }
  | { type: "call-disposition"; result: CallingDispositionResult }
  | { type: "toggle-rail-section"; section: WorkspaceRailSection }
  | {
      type: "toggle-appointment-section"
      section: AppointmentOutcomeFolder
    }
  | { type: "set-task-category"; category: TaskCategoryFilter }
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
        isHidden: environment.isHidden,
        refreshDetails: () =>
          patch((current) => ({
            ...current,
            detailRevision: current.detailRevision + 1,
          })),
      })
    : undefined
  const queryGenerations = {
    tasks: 0,
    recoveryTasks: 0,
    messages: 0,
    aiOutcomes: 0,
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
    requestBudget?.setAIRefresh(
      next.loadState === "ready" && next.scope.practiceID
        ? `${next.scope.practiceID}:${next.scope.locationScopeID}`
        : "",
      refreshAIOutcomes,
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
    const authentication = await authority.authenticate()
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
    const taskGeneration = ++queryGenerations.tasks
    const recoveryGeneration = ++queryGenerations.recoveryTasks
    const messageGeneration = ++queryGenerations.messages
    const outcomeGeneration = ++queryGenerations.aiOutcomes
    const current = state
    const taskRequest = taskQueryRequest(current.scope, current.search.applied)
    const recoveryRequest = recoveryTaskQueryRequest(
      current.scope,
      current.search.applied,
    )
    const messageRequest = messageQueryRequest(current.scope)
    const selectedTaskID = current.selection.task?.id
    const selectedAIInteractionID = current.selection.aiInteractionID
    const [
      snapshotResult,
      taskResult,
      recoveryResult,
      messageResult,
      outcomeResult,
      selectedTaskResult,
      selectedAIResult,
    ] =
      await Promise.all([
        authority.workspace(token, scope, signal),
        loadTaskWindow(token, taskRequest, current.tasks.items.length, signal),
        loadTaskWindow(
          token,
          recoveryRequest,
          current.recoveryTasks.items.length,
          signal,
        ),
        loadMessageWindow(
          token,
          messageRequest,
          current.messages.items.length,
          signal,
        ),
        loadOutcomeWindows(
          token,
          current.aiOutcomes,
          current.scope,
          signal,
        ),
        selectedTaskID
          ? authority.task(token, selectedTaskID, signal)
          : Promise.resolve(undefined),
        selectedAIInteractionID
          ? authority.aiInteraction(token, selectedAIInteractionID, signal)
          : Promise.resolve(undefined),
      ])

    const authorityResults = [
      snapshotResult,
      taskResult,
      recoveryResult,
      messageResult,
      outcomeResult,
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
          const recoveryWindowCurrent =
            recoveryGeneration === queryGenerations.recoveryTasks
          const messageWindowCurrent =
            messageGeneration === queryGenerations.messages
          const outcomeWindowCurrent =
            outcomeGeneration === queryGenerations.aiOutcomes
          const refreshedSelected =
            taskWindowCurrent && selectedTaskResult?.kind === "success"
              ? selectedTaskResult.data
              : undefined
          const tasks =
            taskWindowCurrent && taskResult.kind === "success"
              ? taskResult.data.items
              : currentState.tasks.items
          const recoveryTasks =
            recoveryWindowCurrent && recoveryResult.kind === "success"
              ? recoveryResult.data.items
              : currentState.recoveryTasks.items
          const selectedIsRecovery = refreshedSelected
            ? isRecoveryTask(refreshedSelected)
            : false
          const tasksWithSelection =
            refreshedSelected?.state === "OPEN" && !selectedIsRecovery
            ? tasks.map((task) =>
                task.id === refreshedSelected.id ? refreshedSelected : task,
              )
            : tasks
          const recoveryTasksWithSelection =
            refreshedSelected?.state === "OPEN" && selectedIsRecovery
              ? recoveryTasks.map((task) =>
                  task.id === refreshedSelected.id ? refreshedSelected : task,
                )
              : recoveryTasks
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
            } else if (selectedTaskResult) {
              selection = {
                ...selection,
                taskError: selectedTaskDetailError,
              }
            }
          } else if (taskWindowCurrent &&
            !currentState.selection.task &&
            currentState.selection.view === "none" &&
            tasksWithSelection[0]
          ) {
            const firstTask = tasksWithSelection[0]
            selection = {
              ...selection,
              task: firstTask,
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
          return {
            ...currentState,
            loadState: "ready",
            workspace: snapshot,
            tasks: taskWindowCurrent && taskResult.kind === "success"
              ? {
                  items: tasksWithSelection,
                  nextCursor: taskResult.data.nextCursor,
                  counts: taskResult.data.counts,
                  loading: false,
                  error: "",
                }
              : taskWindowCurrent
                ? {
                  ...currentState.tasks,
                  loading: false,
                  error: taskWindowError,
                  }
                : currentState.tasks,
            recoveryTasks:
              recoveryWindowCurrent && recoveryResult.kind === "success"
              ? {
                  items: recoveryTasksWithSelection,
                  nextCursor: recoveryResult.data.nextCursor,
                  loading: false,
                  error: "",
                }
              : recoveryWindowCurrent
                ? {
                  ...currentState.recoveryTasks,
                  loading: false,
                  error: recoveryWindowError,
                  }
                : currentState.recoveryTasks,
            messages: messageWindowCurrent && messageResult.kind === "success"
              ? {
                  items: messageResult.data.items,
                  nextCursor: messageResult.data.nextCursor,
                  loading: false,
                  error: "",
                }
              : messageWindowCurrent
                ? {
                  ...currentState.messages,
                  loading: false,
                  error: messageWindowError,
                  }
                : currentState.messages,
            aiOutcomes:
              outcomeWindowCurrent && outcomeResult.kind === "success"
              ? {
                  items: outcomeResult.data.items,
                  nextCursor: "",
                  nextCursors: outcomeResult.data.nextCursors,
                  counts: outcomeResult.data.counts,
                  loading: false,
                  error: "",
                }
              : outcomeWindowCurrent
                ? {
                  ...currentState.aiOutcomes,
                  loading: false,
                  error: outcomeWindowError,
                  }
                : currentState.aiOutcomes,
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
    if (intent.type === "load-more-outcomes") {
      await loadMoreOutcomes(intent.folder)
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
      await markEngagementRead(intent.engagement.phone)
      return
    }
    if (intent.type === "select-task") {
      if (intent.rememberForCall) returnTaskID = intent.task.id
      selectEngagement(taskEngagement(intent.task), intent.task)
      await markEngagementRead(intent.task.phone)
      return
    }
    if (intent.type === "select-ai-interaction") {
      const interaction = intent.interaction
      const engagement = aiOutcomeEngagement(interaction)
      selectEngagement(engagement)
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          aiInteractionID: interaction.id,
          aiInteraction: undefined,
          aiInteractionLoading: true,
          aiInteractionError: "",
          contextView: "ai-call",
          contextPanelOpen: true,
        },
      }))
      await Promise.all([
        markEngagementRead(interaction.phone),
        loadAIInteractionDetail(interaction.id),
      ])
      return
    }
    if (intent.type === "select-analytics") {
      patch((current) => ({
        ...current,
        selection: {
          ...current.selection,
          view: "analytics",
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
      projectTaskIntent(intent.task, false)
      realtimeController.refresh()
      return
    }
    if (intent.type === "task-created") {
      projectTaskIntent(intent.task, false)
      realtimeController.refresh()
      return
    }
    if (intent.type === "visibility-changed") {
      realtimeController.visibilityChanged()
      requestBudget?.visibilityChanged()
      return
    }
    if (intent.type === "retry") {
      if (state.discovery && state.scope.practiceID && state.scope.locationID) {
        patch((current) => ({ ...current, loadState: "loading" }))
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
    if (intent.type === "toggle-appointment-section") {
      updateRail((rail) => ({
        ...rail,
        expandedAppointments: rail.expandedAppointments.includes(
          intent.section,
        )
          ? rail.expandedAppointments.filter(
              (section) => section !== intent.section,
            )
          : [...rail.expandedAppointments, intent.section],
      }))
      return
    }
    if (intent.type === "set-task-category") {
      updateRail((rail) => ({ ...rail, taskCategory: intent.category }))
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
    publish({
      ...initialState(),
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
    const queryGeneration = ++queryGenerations[window]
    const currentWindow = state[window]
    if (currentWindow.loading || !currentWindow.nextCursor) return
    patch((current) => ({
      ...current,
      [window]: { ...current[window], loading: true, error: "" },
    }))
    if (window === "messages") {
      const result = await authenticatedRequest((token, signal) =>
        authority.messageThreads(
          token,
          {
            ...messageQueryRequest(state.scope),
            cursor: currentWindow.nextCursor,
          },
          signal,
        ),
      )
      if (
        generation !== scopeGeneration ||
        queryGeneration !== queryGenerations.messages ||
        stopped
      ) return
      if (failIfAccessLost(result)) return
      if (result.kind !== "success") {
        setWindowFailure(window)
        return
      }
      patch((current) => ({
        ...current,
        messages: {
          items: appendUniqueByID(current.messages.items, result.data.items),
          nextCursor: result.data.nextCursor,
          loading: false,
          error: "",
        },
      }))
      return
    }
    const result = await authenticatedRequest((token, signal) =>
      authority.tasks(
        token,
        {
          ...(window === "tasks"
            ? taskQueryRequest(state.scope, state.search.applied)
            : recoveryTaskQueryRequest(state.scope, state.search.applied)),
          cursor: currentWindow.nextCursor,
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
          counts: result.data.counts,
          loading: false,
          error: "",
        },
      }))
      return
    }
    patch((current) => ({
      ...current,
      recoveryTasks: {
        items: appendUniqueByID(current.recoveryTasks.items, result.data.items),
        nextCursor: result.data.nextCursor,
        loading: false,
        error: "",
      },
    }))
  }

  async function loadMoreOutcomes(folder: AppointmentOutcomeFolder) {
    if (state.loadState !== "ready" || state.aiOutcomes.loading) return
    const cursor = state.aiOutcomes.nextCursors[folder]
    if (!cursor) return
    const generation = scopeGeneration
    const queryGeneration = ++queryGenerations.aiOutcomes
    patch((current) => ({
      ...current,
      aiOutcomes: { ...current.aiOutcomes, loading: true, error: "" },
    }))
    const result = await authenticatedRequest((token, signal) =>
      authority.aiOutcomes(
        token,
        {
          practiceId: state.scope.practiceID,
          ...(state.scope.locationScopeID
            ? { locationId: state.scope.locationScopeID }
            : {}),
          appointmentAction: appointmentActionForFolder(folder),
          cursor,
          limit: 10,
        },
        signal,
      ),
    )
    if (
      generation !== scopeGeneration ||
      queryGeneration !== queryGenerations.aiOutcomes ||
      stopped
    ) return
    if (failIfAccessLost(result)) return
    if (result.kind !== "success") {
      setOutcomeFailure()
      return
    }
    patch((current) => {
      const applied = applyOutcomePages(
        current.aiOutcomes.items,
        [
          {
            folder,
            items: result.data.items,
            nextCursor: result.data.nextCursor,
          },
        ],
        true,
      )
      return {
        ...current,
        aiOutcomes: {
          ...current.aiOutcomes,
          items: applied.items,
          nextCursors: {
            ...current.aiOutcomes.nextCursors,
            ...applied.nextCursors,
          },
          loading: false,
          error: "",
        },
      }
    })
  }

  async function refreshAIOutcomes() {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const queryGeneration = ++queryGenerations.aiOutcomes
    const current = state.aiOutcomes
    const scope = state.scope
    const result = await authenticatedRequest((token, signal) =>
      loadOutcomeWindows(token, current, scope, signal),
    )
    if (
      generation !== scopeGeneration ||
      queryGeneration !== queryGenerations.aiOutcomes ||
      stopped
    ) return
    if (failIfAccessLost(result)) return
    if (result.kind !== "success") {
      setOutcomeFailure()
      return
    }
    patch((currentState) => ({
      ...currentState,
      aiOutcomes: {
        items: result.data.items,
        nextCursor: "",
        nextCursors: result.data.nextCursors,
        counts: result.data.counts,
        loading: false,
        error: "",
      },
    }))
  }

  function setOutcomeFailure() {
    patch((current) => ({
      ...current,
      aiOutcomes: {
        ...current.aiOutcomes,
        loading: false,
        error: outcomeWindowError,
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
    const committed = result.data
    const recovery = isRecoveryTask(committed)
    patch((current) => {
      const selected = current.selection.task?.id === committed.id
      return {
        ...current,
        tasks: {
          ...current.tasks,
          items: recovery
            ? current.tasks.items
            : projectCommittedTask(current.tasks.items, committed),
          counts: decrementTaskCounts(current.tasks.counts, committed),
        },
        recoveryTasks: recovery
          ? {
              ...current.recoveryTasks,
              items: projectCommittedTask(
                current.recoveryTasks.items,
                committed,
              ),
            }
          : current.recoveryTasks,
        selection: selected
          ? {
              ...current.selection,
              task: undefined,
              taskError: "",
              engagement: current.selection.engagement
                ? {
                    ...current.selection.engagement,
                    openTaskCount: Math.max(
                      0,
                      current.selection.engagement.openTaskCount - 1,
                    ),
                  }
                : undefined,
              contextPanelOpen:
                current.selection.contextView === "task"
                  ? false
                  : current.selection.contextPanelOpen,
            }
          : current.selection,
        detailRevision: current.detailRevision + 1,
        completion: { pendingTaskID: "", errorTaskID: "", error: "" },
      }
    })
    realtimeController.refresh()
  }

  function selectEngagement(engagement: EngagementSummary, task?: Task) {
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
        contextView: task ? "task" : current.selection.contextView,
        contextPanelOpen: Boolean(task),
      },
    }))
  }

  async function markEngagementRead(phone: string) {
    const unreadThreadIDs = state.messages.items
      .filter((thread) => thread.externalPhone === phone && thread.unread)
      .map((thread) => thread.id)
    if (unreadThreadIDs.length === 0) return
    const generation = scopeGeneration
    const result = await authenticatedRequest(async (token, signal) => {
      const results = await Promise.all(
        unreadThreadIDs.map((threadID) =>
          authority.markMessageThreadRead(token, threadID, signal),
        ),
      )
      const accessLoss = results.find(
        (item) =>
          item.kind === "unauthenticated" || item.kind === "unauthorized",
      )
      if (accessLoss) return accessLoss
      return {
        kind: "success" as const,
        data: unreadThreadIDs.filter(
          (_threadID, index) => results[index]?.kind === "success",
        ),
      }
    })
    if (generation !== scopeGeneration || stopped) return
    if (failIfAccessLost(result) || result.kind !== "success") return
    const readThreadIDs = new Set(result.data)
    if (readThreadIDs.size === 0) return
    patch((current) => ({
      ...current,
      messages: {
        ...current.messages,
        items: current.messages.items.map((thread) =>
          readThreadIDs.has(thread.id) ? { ...thread, unread: false } : thread,
        ),
      },
      tasks: {
        ...current.tasks,
        items: current.tasks.items.map((task) =>
          (task.conversationThreadId &&
            readThreadIDs.has(task.conversationThreadId)) ||
          (task.messageThreadId && readThreadIDs.has(task.messageThreadId))
            ? { ...task, unread: false }
            : task,
        ),
      },
      selection:
        current.selection.engagement?.phone === phone
          ? {
              ...current.selection,
              engagement: { ...current.selection.engagement, unread: false },
              task: current.selection.task
                ? { ...current.selection.task, unread: false }
                : undefined,
            }
          : current.selection,
    }))
  }

  function projectTaskIntent(task: Task, select: boolean) {
    detailGeneration += 1
    const recovery = isRecoveryTask(task)
    patch((current) => {
      const window = recovery ? current.recoveryTasks : current.tasks
      const existed = window.items.some((item) => item.id === task.id)
      const selected = current.selection.task?.id === task.id
      const counts = adjustTaskCountsForProjection(
        current.tasks.counts,
        task,
        existed,
      )
      return {
        ...current,
        search: select ? { ...current.search, input: "" } : current.search,
        tasks: recovery
          ? { ...current.tasks, counts }
          : {
              ...current.tasks,
              items: projectCommittedTask(current.tasks.items, task),
              counts,
            },
        recoveryTasks: recovery
          ? {
              ...current.recoveryTasks,
              items: projectCommittedTask(current.recoveryTasks.items, task),
            }
          : current.recoveryTasks,
        selection:
          select || selected
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
                contextPanelOpen: select
                  ? true
                  : current.selection.contextPanelOpen,
              }
            : current.selection,
        detailRevision: current.detailRevision + 1,
      }
    })
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

  async function reviewAIOutcome(interactionID: string) {
    const generation = scopeGeneration
    const result = await authenticatedRequest((token, signal) =>
      authority.reviewAIOutcome(token, interactionID, signal),
    )
    if (generation !== scopeGeneration || stopped) return false
    if (failIfAccessLost(result)) return false
    if (result.kind !== "success") return false
    patch((current) => {
      const reviewed = current.aiOutcomes.items.find(
        (outcome) => outcome.id === interactionID,
      )
      return {
        ...current,
        aiOutcomes: {
          ...current.aiOutcomes,
          items: current.aiOutcomes.items.filter(
            (outcome) => outcome.id !== interactionID,
          ),
          counts: reviewed
            ? decrementOutcomeCount(
                current.aiOutcomes.counts,
                reviewed.appointmentAction,
              )
            : current.aiOutcomes.counts,
        },
      }
    })
    return true
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
    const previous = [...state.tasks.items, ...state.recoveryTasks.items].find(
      (task) => task.id === returnTaskID,
    )
    const nextTask = previous ?? state.tasks.items[0] ?? state.recoveryTasks.items[0]
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

  async function refreshTaskWindows(search: string) {
    if (state.loadState !== "ready") return
    const generation = scopeGeneration
    const taskGeneration = ++queryGenerations.tasks
    const recoveryGeneration = ++queryGenerations.recoveryTasks
    const scope = state.scope
    patch((current) => ({
      ...current,
      tasks: {
        ...current.tasks,
        items: [],
        nextCursor: "",
        counts: emptyTaskFolderCounts(),
        loading: true,
        error: "",
      },
      recoveryTasks: {
        ...current.recoveryTasks,
        items: [],
        nextCursor: "",
        loading: true,
        error: "",
      },
    }))
    const result = await authenticatedRequest(async (token, signal) => {
      const [tasks, recoveryTasks] = await Promise.all([
        authority.tasks(token, taskQueryRequest(scope, search), signal),
        authority.tasks(
          token,
          recoveryTaskQueryRequest(scope, search),
          signal,
        ),
      ])
      if (
        tasks.kind === "unauthenticated" || tasks.kind === "unauthorized"
      ) return tasks
      if (
        recoveryTasks.kind === "unauthenticated" ||
        recoveryTasks.kind === "unauthorized"
      ) return recoveryTasks
      return {
        kind: "success" as const,
        data: { tasks, recoveryTasks },
      }
    })
    if (generation !== scopeGeneration || stopped) return
    if (failIfAccessLost(result)) return
    const tasks = result.kind === "success"
      ? result.data.tasks
      : ({ kind: "unavailable" } as const)
    const recoveryTasks = result.kind === "success"
      ? result.data.recoveryTasks
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
                counts: tasks.data.counts,
                loading: false,
                error: "",
              }
            : { ...current.tasks, loading: false, error: taskWindowError },
      recoveryTasks:
        recoveryGeneration !== queryGenerations.recoveryTasks
          ? current.recoveryTasks
          : recoveryTasks.kind === "success"
            ? {
                items: recoveryTasks.data.items,
                nextCursor: recoveryTasks.data.nextCursor,
                loading: false,
                error: "",
              }
            : {
                ...current.recoveryTasks,
                loading: false,
                error: recoveryWindowError,
              },
    }))
  }

  function obsoleteAllQueries() {
    queryGenerations.tasks += 1
    queryGenerations.recoveryTasks += 1
    queryGenerations.messages += 1
    queryGenerations.aiOutcomes += 1
  }

  function setWindowFailure(
    window: "tasks" | "recoveryTasks" | "messages",
  ) {
    const error =
      window === "tasks"
        ? taskWindowError
        : window === "recoveryTasks"
          ? recoveryWindowError
          : messageWindowError
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
    let counts = emptyTaskFolderCounts()
    do {
      const result = await authority.tasks(
        token,
        { ...request, ...(cursor ? { cursor } : {}) },
        signal,
      )
      if (result.kind !== "success") return result
      if (items.length === 0) counts = result.data.counts
      items.push(...appendUniqueByID(items, result.data.items).slice(items.length))
      cursor = result.data.nextCursor
    } while (cursor && items.length < target)
    return { kind: "success", data: { items, nextCursor: cursor, counts } }
  }

  async function loadMessageWindow(
    token: string,
    request: MessageThreadQueryRequest,
    loadedCount: number,
    signal: AbortSignal,
  ): Promise<WorkspaceAuthorityResult<MessageThreadPage>> {
    const target = refreshLoadedWindowTarget(loadedCount)
    const items: MessageThreadSummary[] = []
    let cursor = ""
    do {
      const result = await authority.messageThreads(
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

  async function loadOutcomeWindows(
    token: string,
    current: WorkspaceProjectionState["aiOutcomes"],
    scope: WorkspaceScope,
    signal: AbortSignal,
  ): Promise<
    WorkspaceAuthorityResult<{
      items: AiOutcomeItem[]
      nextCursors: AppointmentOutcomeCursors
      counts: AiOutcomeCounts
    }>
  > {
    const categorized = categorizeAIOutcomes(current.items)
    const pages = await Promise.all(
      appointmentOutcomeFolderKeys.map((folder, index) =>
        loadOutcomeWindow(
          token,
          folder,
          categorized[folder].length,
          index === 0,
          scope,
          signal,
        ),
      ),
    )
    const dataPages: Array<{
      folder: AppointmentOutcomeFolder
      items: AiOutcomeItem[]
      nextCursor: string
      counts?: AiOutcomeCounts
    }> = []
    for (const page of pages) {
      if (page.result.kind !== "success") return page.result
      dataPages.push({ folder: page.folder, ...page.result.data })
    }
    const counts = dataPages[0]?.counts ?? current.counts
    return {
      kind: "success",
      data: {
        items: dataPages.flatMap((page) => page.items),
        nextCursors: Object.fromEntries(
          dataPages.map((page) => [page.folder, page.nextCursor]),
        ) as AppointmentOutcomeCursors,
        counts,
      },
    }
  }

  async function loadOutcomeWindow(
    token: string,
    folder: AppointmentOutcomeFolder,
    loadedCount: number,
    includeCounts: boolean,
    scope: WorkspaceScope,
    signal: AbortSignal,
  ) {
    const target = refreshLoadedWindowTarget(loadedCount)
    const items: AiOutcomeItem[] = []
    let cursor = ""
    let counts: AiOutcomeCounts | undefined
    do {
      const result = await authority.aiOutcomes(
        token,
        {
          practiceId: scope.practiceID,
          ...(scope.locationScopeID
            ? { locationId: scope.locationScopeID }
            : {}),
          appointmentAction: appointmentActionForFolder(folder),
          includeCounts,
          ...(cursor ? { cursor } : {}),
          limit: 10,
        },
        signal,
      )
      if (result.kind !== "success") return { folder, result }
      counts ??= result.data.counts
      items.push(...appendUniqueByID(items, result.data.items).slice(items.length))
      cursor = result.data.nextCursor
    } while (cursor && items.length < target)
    return {
      folder,
      result: {
        kind: "success" as const,
        data: { items, nextCursor: cursor, counts },
      },
    }
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
    reviewAIOutcome,
    stop,
  }
}

const railSections: WorkspaceRailSection[] = [
  "tasks",
  "calls",
  "appointments",
  "texts",
]

const taskCategories: TaskCategoryFilter[] = [
  "all",
  "billing",
  "appointments",
  "documentation",
  "optical",
  "medication",
  "referrals",
  "other",
]

function emptyRailState(): WorkspaceRailState {
  return {
    expanded: [],
    expandedAppointments: [],
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
    return {
      expanded: Array.isArray(value.expanded)
        ? value.expanded.filter((section): section is WorkspaceRailSection =>
            railSections.includes(section as WorkspaceRailSection),
          )
        : [],
      expandedAppointments: Array.isArray(value.expandedAppointments)
        ? value.expandedAppointments.filter(
            (section): section is AppointmentOutcomeFolder =>
              appointmentOutcomeFolderKeys.includes(
                section as AppointmentOutcomeFolder,
              ),
          )
        : [],
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
): TaskQueryRequest {
  return {
    practiceId: scope.practiceID,
    ...(scope.locationScopeID ? { locationId: scope.locationScopeID } : {}),
    state: "OPEN",
    ordering: "recent",
    folder: "work",
    ...(search ? { search } : {}),
    limit: 50,
  }
}

function recoveryTaskQueryRequest(
  scope: WorkspaceScope,
  search: string,
): TaskQueryRequest {
  return {
    ...taskQueryRequest(scope, search),
    folder: "missed_calls",
  }
}

function messageQueryRequest(scope: WorkspaceScope): MessageThreadQueryRequest {
  return {
    practiceId: scope.practiceID,
    ...(scope.locationScopeID ? { locationId: scope.locationScopeID } : {}),
    limit: 50,
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

function aiOutcomeEngagement(interaction: AiOutcomeItem): EngagementSummary {
  return {
    phone: interaction.phone,
    locations: [
      { id: interaction.locationId, name: interaction.locationName },
    ],
    latestActivity:
      interaction.appointmentOccurredAt ??
      interaction.endedAt ??
      interaction.startedAt,
    openTaskCount: 0,
    unread: false,
  }
}

function isRecoveryTask(task: Task) {
  return (
    task.origin === "MISSED_CALL_RECOVERY" ||
    task.origin === "VOICEMAIL_RECOVERY"
  )
}

function projectCommittedTask(tasks: Task[], committed: Task) {
  if (committed.state !== "OPEN") {
    return tasks.filter((task) => task.id !== committed.id)
  }
  return tasks.some((task) => task.id === committed.id)
    ? tasks.map((task) => (task.id === committed.id ? committed : task))
    : [committed, ...tasks]
}

function decrementTaskCounts(counts: TaskFolderCounts, committed: Task) {
  if (committed.state === "OPEN") return counts
  if (isRecoveryTask(committed)) {
    return { ...counts, missedCalls: Math.max(0, counts.missedCalls - 1) }
  }
  const category = committed.category ?? "other"
  return {
    ...counts,
    tasks: Math.max(0, counts.tasks - 1),
    categories: {
      ...counts.categories,
      [category]: Math.max(0, counts.categories[category] - 1),
    },
  }
}

function adjustTaskCountsForProjection(
  counts: TaskFolderCounts,
  task: Task,
  existed: boolean,
) {
  if (task.state === "COMPLETED") {
    return existed ? decrementTaskCounts(counts, task) : counts
  }
  if (existed) return counts
  if (isRecoveryTask(task)) {
    return { ...counts, missedCalls: counts.missedCalls + 1 }
  }
  const category = task.category ?? "other"
  return {
    ...counts,
    tasks: counts.tasks + 1,
    categories: {
      ...counts.categories,
      [category]: counts.categories[category] + 1,
    },
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
    recoveryTasks: { items: [], nextCursor: "", loading: false, error: "" },
    messages: { items: [], nextCursor: "", loading: false, error: "" },
    aiOutcomes: {
      items: [],
      nextCursor: "",
      nextCursors: emptyAppointmentOutcomeCursors(),
      counts: emptyAIOutcomeCounts(),
      loading: false,
      error: "",
    },
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

function emptyAIOutcomeCounts(): AiOutcomeCounts {
  return { tasks: 0, bookings: 0, cancellations: 0, reschedules: 0 }
}

function refreshLoadedWindowTarget(loadedCount: number) {
  return Math.max(1, loadedCount)
}

const taskWindowError = "Tasks are temporarily unavailable."
const selectedTaskDetailError = "Task details are temporarily unavailable."
const recoveryWindowError = "Missed Calls are temporarily unavailable."
const messageWindowError = "Texts are temporarily unavailable."
const outcomeWindowError = "AI appointment updates are unavailable."
const aiInteractionDetailError = "This AI call could not be loaded."
