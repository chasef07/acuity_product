"use client"

import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react"
import dynamic from "next/dynamic"
import { useRouter } from "next/navigation"
import {
  CheckIcon,
  ChevronsUpDownIcon,
  PanelRightCloseIcon,
  WifiOffIcon,
} from "lucide-react"

import { AcuityMark } from "@/components/acuity-mark"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import {
  CallingAvailabilityControl,
  CallingDock,
} from "@/components/workspace/calling-dock"
import { AIInteractionContext } from "@/components/workspace/ai-interaction-detail"
import { InteractionWorkspace } from "@/components/workspace/interaction-workspace"
import { EngagementWorkspace } from "@/components/workspace/message-workspace"
import {
  type ConnectionState,
  TaskRail,
} from "@/components/workspace/task-rail"
import { portalClient, realtimeURL } from "@/lib/api/client"
import {
  discoverAccess,
  getCallingCall,
  getWorkspace,
  markMessageThreadRead,
  queryAiInteractionOutcomes,
  queryMessageThreads,
  queryTasks,
  readTask,
  reviewAiInteractionOutcome,
} from "@/lib/api/generated/sdk.gen"
import type {
  AccessDiscovery,
  AiOutcomeCounts,
  AiOutcomeItem,
  CallingCall,
  CallingDispositionResult,
  EngagementSummary,
  MessageThreadSummary,
  Task,
  TaskFolderCounts,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"
import {
  appendOutcomePage,
  decrementOutcomeCount,
  mergeOutcomePages,
} from "@/lib/ai-outcome-attention"
import { cn } from "@/lib/utils"
import { resolveWorkspaceSearch } from "@/lib/workspace-search"
import {
  projectTaskUpdate,
  refreshTaskWindowTarget,
} from "@/lib/workspace-triage"
import {
  createWorkspaceRequestBudget,
  type WorkspaceRequestBudget,
} from "@/lib/workspace-sync/workspace-request-budget"
import {
  createWorkspaceSync,
  type WorkspaceSync,
  WorkspaceSyncUnauthorizedError,
} from "@/lib/workspace-sync/workspace-sync"

const OperatorAnalytics = dynamic(
  () =>
    import("@/components/workspace/operator-analytics").then(
      (module) => module.OperatorAnalytics,
    ),
  {
    loading: () => (
      <div
        aria-label="Loading analytics workspace"
        aria-busy="true"
        className="flex min-h-0 flex-1 bg-muted/20 p-4 sm:p-6 lg:p-8"
      >
        <Skeleton className="h-40 w-full rounded-xl" />
      </div>
    ),
  },
)

type LoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type View = "none" | "engagement" | "analytics"
type ContextView = "task" | "call" | "appointment"

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"
const taskScopeStorageKey = "acuity.taskLocationScope"

export function TaskWorkspaceShell() {
  const router = useRouter()
  const session = authClient.useSession()
  const [loadState, setLoadState] = useState<LoadState>("loading")
  const [connection, setConnection] = useState<ConnectionState>("connecting")
  const [discovery, setDiscovery] = useState<AccessDiscovery>()
  const [workspace, setWorkspace] = useState<WorkspaceSnapshot>()
  const [practiceID, setPracticeID] = useState("")
  const [locationID, setLocationID] = useState("")
  const [locationScopeID, setLocationScopeID] = useState("")
  const [search, setSearch] = useState("")
  const [taskSearch, setTaskSearch] = useState("")
  const [engagementError, setEngagementError] = useState("")
  const [selectedEngagement, setSelectedEngagement] = useState<EngagementSummary>()
  const [tasks, setTasks] = useState<Task[]>([])
  const [taskCounts, setTaskCounts] = useState<TaskFolderCounts>(() =>
    emptyTaskFolderCounts(),
  )
  const [nextCursor, setNextCursor] = useState("")
  const [tasksLoading, setTasksLoading] = useState(false)
  const [recoveryTasks, setRecoveryTasks] = useState<Task[]>([])
  const [recoveryNextCursor, setRecoveryNextCursor] = useState("")
  const [recoveryTasksLoading, setRecoveryTasksLoading] = useState(false)
  const [messageThreads, setMessageThreads] = useState<MessageThreadSummary[]>(
    [],
  )
  const [messageNextCursor, setMessageNextCursor] = useState("")
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [aiOutcomes, setAIOutcomes] = useState<AiOutcomeItem[]>([])
  const [aiOutcomeCounts, setAIOutcomeCounts] = useState<AiOutcomeCounts>(() =>
    emptyAIOutcomeCounts(),
  )
  const [aiOutcomeNextCursor, setAIOutcomeNextCursor] = useState("")
  const [aiOutcomesLoading, setAIOutcomesLoading] = useState(false)
  const [aiOutcomesError, setAIOutcomesError] = useState("")
  const [selectedAIInteractionID, setSelectedAIInteractionID] = useState("")
  const [selectedTask, setSelectedTask] = useState<Task>()
  const [view, setView] = useState<View>("none")
  const [contextView, setContextView] = useState<ContextView>("task")
  const [contextPanelOpen, setContextPanelOpen] = useState(false)
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [historicalCall, setHistoricalCall] = useState<CallingCall>()
  const [workspaceRevision, setWorkspaceRevision] = useState(0)
  const [requestBudget] = useState<WorkspaceRequestBudget>(() =>
    createWorkspaceRequestBudget({
      refreshDetails: () => setWorkspaceRevision((current) => current + 1),
      isHidden: () => document.hidden,
    }),
  )
  const [taskCallRequest, setTaskCallRequest] = useState<{
    id: string
    taskID: string
  }>()
  const [taskCallError, setTaskCallError] = useState("")
  const selectedTaskRef = useRef<Task | undefined>(undefined)
  const workspaceRef = useRef<WorkspaceSnapshot | undefined>(undefined)
  const tasksRef = useRef<Task[]>([])
  const recoveryTasksRef = useRef<Task[]>([])
  const nextCursorRef = useRef("")
  const recoveryNextCursorRef = useRef("")
  const messageThreadsRef = useRef<MessageThreadSummary[]>([])
  const aiOutcomesRef = useRef<AiOutcomeItem[]>([])
  const hasLoadedTasksRef = useRef(false)
  const hasLoadedThreadsRef = useRef(false)
  const taskQueryGenerationRef = useRef(0)
  const recoveryTaskQueryGenerationRef = useRef(0)
  const messageQueryGenerationRef = useRef(0)
  const aiOutcomeQueryGenerationRef = useRef(0)
  const taskQueryKeyRef = useRef("")
  const recoveryTaskQueryKeyRef = useRef("")
  const messageQueryKeyRef = useRef("")
  const snapshotGenerationRef = useRef(0)
  const snapshotScopeRef = useRef("")
  const viewRef = useRef<View>("none")
  const locationScopeRef = useRef("")
  const taskSearchRef = useRef("")
  const workspaceSyncRef = useRef<WorkspaceSync | undefined>(undefined)
  const returnTaskIDRef = useRef("")
  const focusedCallIDRef = useRef("")
  const activeCallIDRef = useRef("")
  const callDetailGenerationRef = useRef(0)

  useEffect(() => {
    workspaceRef.current = workspace
  }, [workspace])
  useEffect(() => {
    viewRef.current = view
  }, [view])
  useEffect(() => {
    requestBudget.setDetailRefreshMounted(
      view === "engagement" && Boolean(selectedEngagement),
    )
  }, [requestBudget, selectedEngagement, view])
  useEffect(() => {
    locationScopeRef.current = locationScopeID
  }, [locationScopeID])
  useEffect(() => {
    taskSearchRef.current = taskSearch
  }, [taskSearch])
  const loadTasks = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID) return
      const queryKey = workspaceTaskQueryKey(
        practiceID,
        locationScopeID,
        taskSearch,
      )
      const requestGeneration = ++taskQueryGenerationRef.current
      setTasksLoading(true)
      const token = await getAccessToken()
      if (!token) {
        setTasksLoading(false)
        setLoadState("unauthorized")
        return
      }
      const result = await queryTasks({
        client: portalClient(token),
        body: {
          practiceId: practiceID,
          ...(locationScopeID ? { locationId: locationScopeID } : {}),
          state: "OPEN",
          ordering: "priority",
          folder: "work",
          ...(taskSearch ? { search: taskSearch } : {}),
          ...(cursor ? { cursor } : {}),
          limit: 50,
        },
      }).catch(() => undefined)
      if (requestGeneration !== taskQueryGenerationRef.current) return
      setTasksLoading(false)
      if (!result?.data) {
        if (
          result?.response?.status === 401 ||
          result?.response?.status === 403
        ) {
          tasksRef.current = []
          setTasks([])
          nextCursorRef.current = ""
          setNextCursor("")
          updateSelectedTask(undefined)
          setView("none")
          setLoadState("unauthorized")
        }
        return
      }
      const firstLoad = !hasLoadedTasksRef.current
      hasLoadedTasksRef.current = true
      taskQueryKeyRef.current = queryKey
      const next = append
        ? [...tasksRef.current, ...result.data.items]
        : result.data.items
      tasksRef.current = next
      setTasks(next)
      nextCursorRef.current = result.data.nextCursor
      setNextCursor(result.data.nextCursor)
      setTaskCounts(result.data.counts)

      const selected = selectedTaskRef.current
      if (selected) {
        const current = next.find((task) => task.id === selected.id)
        if (current) updateSelectedTask(current)
      } else if (firstLoad && next[0] && viewRef.current === "none") {
        const engagement = taskEngagement(next[0])
        updateSelectedTask(next[0])
        setSelectedEngagement(engagement)
        setContextView("task")
        setContextPanelOpen(true)
        setView("engagement")
      }
    },
    [locationScopeID, practiceID, taskSearch],
  )
  const loadRecoveryTasks = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID) return
      const queryKey = workspaceRecoveryTaskQueryKey(
        practiceID,
        locationScopeID,
        taskSearch,
      )
      const requestGeneration = ++recoveryTaskQueryGenerationRef.current
      setRecoveryTasksLoading(true)
      const token = await getAccessToken()
      if (!token) {
        setRecoveryTasksLoading(false)
        setLoadState("unauthorized")
        return
      }
      const result = await queryTasks({
        client: portalClient(token),
        body: {
          practiceId: practiceID,
          ...(locationScopeID ? { locationId: locationScopeID } : {}),
          state: "OPEN",
          ordering: "time",
          folder: "missed_calls",
          ...(taskSearch ? { search: taskSearch } : {}),
          ...(cursor ? { cursor } : {}),
          limit: 50,
        },
      }).catch(() => undefined)
      if (requestGeneration !== recoveryTaskQueryGenerationRef.current) return
      setRecoveryTasksLoading(false)
      if (!result?.data) {
        if (
          result?.response?.status === 401 ||
          result?.response?.status === 403
        ) {
          recoveryTasksRef.current = []
          setRecoveryTasks([])
          recoveryNextCursorRef.current = ""
          setRecoveryNextCursor("")
          setLoadState("unauthorized")
        }
        return
      }
      recoveryTaskQueryKeyRef.current = queryKey
      const next = append
        ? [...recoveryTasksRef.current, ...result.data.items]
        : result.data.items
      recoveryTasksRef.current = next
      setRecoveryTasks(next)
      recoveryNextCursorRef.current = result.data.nextCursor
      setRecoveryNextCursor(result.data.nextCursor)

      const selected = selectedTaskRef.current
      if (selected) {
        const current = next.find((task) => task.id === selected.id)
        if (current) updateSelectedTask(current)
      }
    },
    [locationScopeID, practiceID, taskSearch],
  )
  const loadMessageThreads = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID) return
      const queryKey = workspaceMessageQueryKey(
        practiceID,
        locationScopeID,
      )
      const requestGeneration = ++messageQueryGenerationRef.current
      setMessagesLoading(true)
      const token = await getAccessToken()
      if (!token) {
        setMessagesLoading(false)
        setLoadState("unauthorized")
        return
      }
      const result = await queryMessageThreads({
        client: portalClient(token),
        body: {
          practiceId: practiceID,
          ...(locationScopeID ? { locationId: locationScopeID } : {}),
          ...(cursor ? { cursor } : {}),
          limit: 50,
        },
      }).catch(() => undefined)
      if (requestGeneration !== messageQueryGenerationRef.current) return
      setMessagesLoading(false)
      if (!result?.data) {
        if (
          result?.response?.status === 401 ||
          result?.response?.status === 403
        ) {
          messageThreadsRef.current = []
          setMessageThreads([])
          setLoadState("unauthorized")
        }
        return
      }
      hasLoadedThreadsRef.current = true
      messageQueryKeyRef.current = queryKey
      const next = append
        ? [...messageThreadsRef.current, ...result.data.items]
        : result.data.items
      messageThreadsRef.current = next
      setMessageThreads(next)
      setMessageNextCursor(result.data.nextCursor)
    },
    [locationScopeID, practiceID],
  )
  const loadAIOutcomes = useCallback(async (cursor = "", append = false) => {
    if (!practiceID) return
    const requestGeneration = ++aiOutcomeQueryGenerationRef.current
    setAIOutcomesLoading(true)
    setAIOutcomesError("")
    const token = await getAccessToken()
    if (!token) {
      setAIOutcomesLoading(false)
      setLoadState("unauthorized")
      return
    }
    const result = await queryAiInteractionOutcomes({
      client: portalClient(token),
      body: {
        practiceId: practiceID,
        ...(locationScopeID ? { locationId: locationScopeID } : {}),
        ...(cursor ? { cursor } : {}),
        limit: 50,
      },
    }).catch(() => undefined)
    if (requestGeneration !== aiOutcomeQueryGenerationRef.current) return
    setAIOutcomesLoading(false)
    if (!result?.data) {
      if (
        result?.response?.status === 401 ||
        result?.response?.status === 403
      ) {
        aiOutcomesRef.current = []
        setAIOutcomes([])
        setAIOutcomeCounts(emptyAIOutcomeCounts())
        setAIOutcomeNextCursor("")
        setSelectedAIInteractionID("")
        setLoadState("unauthorized")
        return
      }
      setAIOutcomesError("AI appointment updates are unavailable.")
      return
    }
    const loaded = aiOutcomesRef.current
    const refreshing = !append && loaded.length > 0
    const next = append
      ? appendOutcomePage(loaded, result.data.items)
      : refreshing
        ? mergeOutcomePages(loaded, result.data.items)
        : result.data.items
    aiOutcomesRef.current = next
    setAIOutcomes(next)
    setAIOutcomeCounts(result.data.counts)
    if (!refreshing || append) {
      setAIOutcomeNextCursor(result.data.nextCursor)
    }
  }, [locationScopeID, practiceID])
  const reviewAIOutcome = useCallback(async (interactionID: string) => {
    const token = await getAccessToken()
    if (!token) return false
    const result = await reviewAiInteractionOutcome({
      client: portalClient(token),
      path: { interactionId: interactionID },
    }).catch(() => undefined)
    if (!result?.response?.ok) return false
    const reviewed = aiOutcomesRef.current.find(
      (outcome) => outcome.id === interactionID,
    )
    const next = aiOutcomesRef.current.filter(
      (outcome) => outcome.id !== interactionID,
    )
    aiOutcomesRef.current = next
    setAIOutcomes(next)
    if (reviewed) {
      setAIOutcomeCounts((counts) =>
        decrementOutcomeCount(counts, reviewed.appointmentAction),
      )
    }
    return true
  }, [])
  const reconcileWorkspace = useCallback(
    async ({
      scope,
      token,
      signal,
      minimumVersion,
    }: {
      scope: { practiceID: string; locationID: string }
      token: string
      signal: AbortSignal
      minimumVersion: number
    }) => {
      const taskGeneration = ++taskQueryGenerationRef.current
      const recoveryTaskGeneration =
        ++recoveryTaskQueryGenerationRef.current
      const messageGeneration = ++messageQueryGenerationRef.current
      const taskLocationID = locationScopeRef.current
      const currentTaskSearch = taskSearchRef.current
      const selectedTaskID = selectedTaskRef.current?.id
      const taskQueryKey = workspaceTaskQueryKey(
        scope.practiceID,
        taskLocationID,
        currentTaskSearch,
      )
      const recoveryTaskQueryKey = workspaceRecoveryTaskQueryKey(
        scope.practiceID,
        taskLocationID,
        currentTaskSearch,
      )
      const messageQueryKey = workspaceMessageQueryKey(
        scope.practiceID,
        taskLocationID,
      )
      const client = portalClient(token)
      const loadTaskWindow = async (
        folder: "work" | "missed_calls",
        ordering: "priority" | "recent" | "time",
        loadedCount: number,
      ) => {
        const target = refreshTaskWindowTarget(loadedCount)
        const items: Task[] = []
        const seen = new Set<string>()
        let cursor = ""
        let result: Awaited<ReturnType<typeof queryTasks>> | undefined
        do {
          result = await queryTasks({
            client,
            body: {
              practiceId: scope.practiceID,
              ...(taskLocationID ? { locationId: taskLocationID } : {}),
              state: "OPEN",
              ordering,
              folder,
              ...(currentTaskSearch ? { search: currentTaskSearch } : {}),
              ...(cursor ? { cursor } : {}),
              limit: 50,
            },
            signal,
          }).catch(() => undefined)
          if (!result?.data) return result
          for (const task of result.data.items) {
            if (seen.has(task.id)) continue
            seen.add(task.id)
            items.push(task)
          }
          cursor = result.data.nextCursor
        } while (cursor && items.length < target)
        return {
          ...result,
          data: {
            ...result.data,
            items,
            nextCursor: cursor,
          },
        }
      }
      const taskLoadedCount =
        taskQueryKeyRef.current === taskQueryKey ? tasksRef.current.length : 0
      const recoveryLoadedCount =
        recoveryTaskQueryKeyRef.current === recoveryTaskQueryKey
          ? recoveryTasksRef.current.length
          : 0
      const [
        snapshotResult,
        taskResult,
        recoveryTaskResult,
        messageResult,
        selectedResult,
      ] =
        await Promise.all([
          getWorkspace({
            client,
            query: {
              practiceId: scope.practiceID,
              locationId: scope.locationID,
            },
            signal,
          }).catch(() => undefined),
          loadTaskWindow("work", "priority", taskLoadedCount),
          loadTaskWindow("missed_calls", "time", recoveryLoadedCount),
          queryMessageThreads({
            client,
            body: {
              practiceId: scope.practiceID,
              ...(taskLocationID ? { locationId: taskLocationID } : {}),
              limit: 50,
            },
            signal,
          }).catch(() => undefined),
          selectedTaskID
            ? readTask({
                client,
                path: { taskId: selectedTaskID },
                signal,
              }).catch(() => undefined)
            : Promise.resolve(undefined),
        ])
      if (
        [snapshotResult, taskResult, recoveryTaskResult, messageResult].some(
          (result) =>
            result?.response?.status === 401 ||
            result?.response?.status === 403,
        )
      ) {
        throw new WorkspaceSyncUnauthorizedError()
      }
      if (
        !snapshotResult?.data ||
        !taskResult?.data ||
        !recoveryTaskResult?.data ||
        !messageResult?.data
      ) {
        throw new Error("workspace authority is unavailable")
      }
      if (snapshotResult.data.version < minimumVersion) {
        throw new Error("workspace authority has not reached the hinted version")
      }

      const snapshot = snapshotResult.data
      const nextTasks = taskResult.data.items
      const nextRecoveryTasks = recoveryTaskResult.data.items
      const nextMessages = messageResult.data.items
      return {
        version: snapshot.version,
        apply: () => {
          if (
            signal.aborted ||
            snapshotScopeRef.current !==
              `${scope.practiceID}:${scope.locationID}` ||
            (workspaceRef.current &&
              snapshot.version < workspaceRef.current.version)
          ) {
            return
          }
          workspaceRef.current = snapshot
          setWorkspace(snapshot)
          setLoadState("ready")

          if (taskGeneration === taskQueryGenerationRef.current) {
            setTasksLoading(false)
            const firstLoad = !hasLoadedTasksRef.current
            hasLoadedTasksRef.current = true
            taskQueryKeyRef.current = taskQueryKey
            const refreshed = selectedResult?.data
            const tasksWithSelection = refreshed
              ? nextTasks.map((task) =>
                  task.id === refreshed.id ? refreshed : task,
                )
              : nextTasks
            const taskWindow = {
              items: tasksWithSelection,
              cursor: taskResult.data.nextCursor,
            }
            tasksRef.current = taskWindow.items
            setTasks(taskWindow.items)
            nextCursorRef.current = taskWindow.cursor
            setNextCursor(taskWindow.cursor)
            setTaskCounts(taskResult.data.counts)
            const selected = selectedTaskRef.current
            if (selected) {
              const current =
                refreshed?.id === selected.id
                  ? refreshed
                  : taskWindow.items.find((task) => task.id === selected.id)
              if (current) updateSelectedTask(current)
              else if (
                selectedTaskID === selected.id &&
                (selectedResult?.response?.status === 401 ||
                  selectedResult?.response?.status === 403)
              ) {
                updateSelectedTask(undefined)
                setContextPanelOpen(false)
              }
            } else if (
              firstLoad &&
              taskWindow.items[0] &&
              viewRef.current === "none"
            ) {
              const engagement = taskEngagement(taskWindow.items[0])
              updateSelectedTask(taskWindow.items[0])
              setSelectedEngagement(engagement)
              setContextView("task")
              setContextPanelOpen(true)
              setView("engagement")
            }
          }

          if (
            recoveryTaskGeneration === recoveryTaskQueryGenerationRef.current
          ) {
            setRecoveryTasksLoading(false)
            recoveryTaskQueryKeyRef.current = recoveryTaskQueryKey
            const recoveryWindow = {
              items: nextRecoveryTasks,
              cursor: recoveryTaskResult.data.nextCursor,
            }
            recoveryTasksRef.current = recoveryWindow.items
            setRecoveryTasks(recoveryWindow.items)
            recoveryNextCursorRef.current = recoveryWindow.cursor
            setRecoveryNextCursor(recoveryWindow.cursor)
          }

          if (messageGeneration === messageQueryGenerationRef.current) {
            setMessagesLoading(false)
            hasLoadedThreadsRef.current = true
            messageQueryKeyRef.current = messageQueryKey
            messageThreadsRef.current = nextMessages
            setMessageThreads(nextMessages)
            setMessageNextCursor(messageResult.data.nextCursor)
          }
          requestBudget.signalDetailRefresh()
        },
      }
    },
    [requestBudget],
  )
  const reconcileWorkspaceRef = useRef(reconcileWorkspace)
  useEffect(() => {
    reconcileWorkspaceRef.current = reconcileWorkspace
  }, [reconcileWorkspace])

  const loadAuthority = useCallback(async () => {
    if (!session.data) return
    const token = await getAccessToken()
    if (!token) {
      setLoadState("unauthorized")
      return
    }
    const result = await discoverAccess({
      client: portalClient(token),
    }).catch(() => undefined)
    if (!result?.data) {
      const status = result?.response?.status
      setLoadState(
        status === 401 || status === 403 ? "unauthorized" : "unavailable",
      )
      return
    }
    const storedPractice = window.localStorage.getItem(practiceStorageKey)
    const practice =
      result.data.practices.find((item) => item.id === storedPractice) ??
      result.data.practices[0]
    const storedLocation = window.localStorage.getItem(locationStorageKey)
    const location =
      practice?.locations.find((item) => item.id === storedLocation) ??
      practice?.locations[0]
    if (!practice || !location) {
      setLoadState("unauthorized")
      return
    }
    const storedScope = window.localStorage.getItem(
      `${taskScopeStorageKey}.${practice.id}`,
    )
    const scope =
      practice.locations.length === 1
        ? location.id
        : practice.locations.some((item) => item.id === storedScope)
          ? (storedScope ?? "")
          : ""
    locationScopeRef.current = scope
    setDiscovery(result.data)
    snapshotScopeRef.current = `${practice.id}:${location.id}`
    setPracticeID(practice.id)
    setLocationID(location.id)
    setLocationScopeID(scope)
    window.localStorage.setItem(practiceStorageKey, practice.id)
    window.localStorage.setItem(locationStorageKey, location.id)
    setLoadState("loading")
  }, [session.data])

  useEffect(() => {
    if (session.isPending) return
    if (!session.data) {
      router.replace("/sign-in?next=%2Fworkspace")
      return
    }
    const timeout = window.setTimeout(() => void loadAuthority(), 0)
    return () => window.clearTimeout(timeout)
  }, [loadAuthority, router, session.data, session.isPending])

  useEffect(() => {
    if (!practiceID || loadState !== "ready") return
    const queryKey = workspaceTaskQueryKey(
      practiceID,
      locationScopeID,
      taskSearch,
    )
    if (taskQueryKeyRef.current === queryKey) return
    const timeout = window.setTimeout(() => void loadTasks(), 0)
    return () => window.clearTimeout(timeout)
  }, [
    loadState,
    loadTasks,
    locationScopeID,
    practiceID,
    taskSearch,
  ])

  useEffect(() => {
    if (!practiceID || loadState !== "ready") return
    const queryKey = workspaceRecoveryTaskQueryKey(
      practiceID,
      locationScopeID,
      taskSearch,
    )
    if (recoveryTaskQueryKeyRef.current === queryKey) return
    const timeout = window.setTimeout(() => void loadRecoveryTasks(), 0)
    return () => window.clearTimeout(timeout)
  }, [
    loadRecoveryTasks,
    loadState,
    locationScopeID,
    practiceID,
    taskSearch,
  ])

  useEffect(() => {
    if (
      !practiceID ||
      !locationID ||
      loadState !== "ready"
    ) {
      return
    }
    const queryKey = workspaceMessageQueryKey(
      practiceID,
      locationScopeID,
    )
    if (messageQueryKeyRef.current === queryKey) return
    const timeout = window.setTimeout(() => void loadMessageThreads(), 0)
    return () => window.clearTimeout(timeout)
  }, [
    loadMessageThreads,
    loadState,
    locationID,
    locationScopeID,
    practiceID,
  ])

  useEffect(() => {
    const scopeKey =
      practiceID && loadState === "ready"
        ? `${practiceID}:${locationScopeID}`
        : ""
    requestBudget.setAIRefresh(scopeKey, () => loadAIOutcomes())
  }, [loadAIOutcomes, loadState, locationScopeID, practiceID, requestBudget])

  useEffect(() => () => requestBudget.stop(), [requestBudget])

  useEffect(() => {
    const sync = createWorkspaceSync({
      realtimeURL: realtimeURL(),
      getToken: getAccessToken,
      reconcile: (input) => reconcileWorkspaceRef.current(input),
      onStateChange: setConnection,
      onUnauthorized: () => setLoadState("unauthorized"),
      isHidden: () => document.hidden,
    })
    workspaceSyncRef.current = sync
    const handleVisibility = () => {
      sync.visibilityChanged()
      requestBudget.visibilityChanged()
    }
    document.addEventListener("visibilitychange", handleVisibility)
    return () => {
      workspaceSyncRef.current = undefined
      document.removeEventListener("visibilitychange", handleVisibility)
      sync.stop()
    }
  }, [requestBudget])

  useEffect(() => {
    const sync = workspaceSyncRef.current
    if (!sync) return
    if (
      !discovery ||
      !practiceID ||
      !locationID ||
      loadState === "unauthorized"
    ) {
      sync.setScope()
      return
    }
    sync.setScope({ practiceID, locationID })
  }, [discovery, loadState, locationID, practiceID])

  useEffect(() => {
    if (connection === "degraded" && !workspaceRef.current) {
      setLoadState("unavailable")
    }
  }, [connection])

  function selectLocationScope(nextLocationID: string) {
    callDetailGenerationRef.current += 1
    taskQueryGenerationRef.current += 1
    recoveryTaskQueryGenerationRef.current += 1
    messageQueryGenerationRef.current += 1
    aiOutcomeQueryGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    taskQueryKeyRef.current = ""
    recoveryTaskQueryKeyRef.current = ""
    messageQueryKeyRef.current = ""
    tasksRef.current = []
    recoveryTasksRef.current = []
    nextCursorRef.current = ""
    recoveryNextCursorRef.current = ""
    messageThreadsRef.current = []
    aiOutcomesRef.current = []
    setTasks([])
    setRecoveryTasks([])
    setNextCursor("")
    setRecoveryNextCursor("")
    setTaskCounts(emptyTaskFolderCounts())
    setMessageThreads([])
    setAIOutcomes([])
    setAIOutcomeCounts(emptyAIOutcomeCounts())
    setAIOutcomeNextCursor("")
    setAIOutcomesError("")
    setSelectedAIInteractionID("")
    updateSelectedTask(undefined)
    setHistoricalCall(undefined)
    setContextPanelOpen(false)
    setView("none")
    locationScopeRef.current = nextLocationID
    setLocationScopeID(nextLocationID)
    window.localStorage.setItem(
      `${taskScopeStorageKey}.${practiceID}`,
      nextLocationID,
    )
    if (nextLocationID && nextLocationID !== locationID) {
      workspaceRef.current = undefined
      setWorkspace(undefined)
      setLoadState("loading")
      snapshotScopeRef.current = `${practiceID}:${nextLocationID}`
      setLocationID(nextLocationID)
      window.localStorage.setItem(locationStorageKey, nextLocationID)
    }
  }

  function selectWorkspaceScope(
    nextPracticeID: string,
    nextLocationScopeID: string,
  ) {
    if (!discovery) return
    const nextPractice = discovery.practices.find(
      (item) => item.id === nextPracticeID,
    )
    if (!nextPractice) return
    if (nextPracticeID === practiceID) {
      selectLocationScope(nextLocationScopeID)
      return
    }
    const nextLocation =
      nextPractice.locations.find(
        (location) => location.id === nextLocationScopeID,
      ) ?? nextPractice.locations[0]
    if (!nextLocation) return

    callDetailGenerationRef.current += 1
    taskQueryGenerationRef.current += 1
    recoveryTaskQueryGenerationRef.current += 1
    messageQueryGenerationRef.current += 1
    aiOutcomeQueryGenerationRef.current += 1
    snapshotGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    taskQueryKeyRef.current = ""
    recoveryTaskQueryKeyRef.current = ""
    messageQueryKeyRef.current = ""
    tasksRef.current = []
    recoveryTasksRef.current = []
    nextCursorRef.current = ""
    recoveryNextCursorRef.current = ""
    messageThreadsRef.current = []
    aiOutcomesRef.current = []
    setTasks([])
    setRecoveryTasks([])
    setNextCursor("")
    setRecoveryNextCursor("")
    setTaskCounts(emptyTaskFolderCounts())
    setMessageThreads([])
    setAIOutcomes([])
    setAIOutcomeCounts(emptyAIOutcomeCounts())
    setAIOutcomeNextCursor("")
    setAIOutcomesError("")
    setSelectedAIInteractionID("")
    updateSelectedTask(undefined)
    setHistoricalCall(undefined)
    setContextPanelOpen(false)
    setView("none")

    const nextScope = nextLocationScopeID
    locationScopeRef.current = nextScope
    workspaceRef.current = undefined
    setWorkspace(undefined)
    setPracticeID(nextPractice.id)
    setLocationID(nextLocation.id)
    setLocationScopeID(nextScope)
    snapshotScopeRef.current = `${nextPractice.id}:${nextLocation.id}`
    window.localStorage.setItem(practiceStorageKey, nextPractice.id)
    window.localStorage.setItem(locationStorageKey, nextLocation.id)
    window.localStorage.setItem(
      `${taskScopeStorageKey}.${nextPractice.id}`,
      nextScope,
    )
    setLoadState("loading")
  }

  function updateSelectedTask(task?: Task) {
    selectedTaskRef.current = task
    setSelectedTask(task)
  }

  function selectTask(task: Task) {
    if (activeCall) returnTaskIDRef.current = task.id
    selectEngagement(taskEngagement(task), task)
    setContextView("task")
    setContextPanelOpen(true)
  }

  function selectEngagement(engagement: EngagementSummary, focusedTask?: Task) {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    setSelectedAIInteractionID("")
    setContextPanelOpen(false)
    updateSelectedTask(focusedTask)
    setSelectedEngagement(engagement)
    setView("engagement")
    void markEngagementRead(engagement.phone)
  }

  function selectAIInteraction(interaction: AiOutcomeItem) {
    selectEngagement({
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
    })
    openAIInteractionContext(interaction.id)
  }

  function openAIInteractionContext(interactionID: string) {
    callDetailGenerationRef.current += 1
    setSelectedAIInteractionID(interactionID)
    setContextView("appointment")
    setContextPanelOpen(true)
  }

  async function markEngagementRead(phone: string) {
    const unreadThreadIDs = messageThreadsRef.current
      .filter((thread) => thread.externalPhone === phone && thread.unread)
      .map((thread) => thread.id)
    if (unreadThreadIDs.length === 0) return
    const token = await getAccessToken()
    if (!token) return
    const results = await Promise.all(
      unreadThreadIDs.map(async (threadID) => {
        const result = await markMessageThreadRead({
          client: portalClient(token),
          path: { threadId: threadID },
          body: {},
        }).catch(() => undefined)
        return result?.response?.ok ? threadID : ""
      }),
    )
    const readThreadIDs = new Set(results.filter(Boolean))
    if (readThreadIDs.size === 0) return
    projectThreadsRead(readThreadIDs)
    setSelectedEngagement((current) =>
      current?.phone === phone ? { ...current, unread: false } : current,
    )
  }

  function projectThreadsRead(readThreadIDs: Set<string>) {
    const nextThreads = messageThreadsRef.current.map((thread) =>
      readThreadIDs.has(thread.id) ? { ...thread, unread: false } : thread,
    )
    messageThreadsRef.current = nextThreads
    setMessageThreads(nextThreads)
    const nextTasks = tasksRef.current.map((task) =>
      (task.conversationThreadId && readThreadIDs.has(task.conversationThreadId)) ||
      (task.messageThreadId && readThreadIDs.has(task.messageThreadId))
        ? { ...task, unread: false }
        : task,
    )
    tasksRef.current = nextTasks
    setTasks(nextTasks)
  }

  function submitSearch() {
    if (!practiceID) return
    const resolved = resolveWorkspaceSearch(search)
    if (resolved.kind === "tasks") {
      taskSearchRef.current = resolved.value
      setTaskSearch(resolved.value)
      setEngagementError("")
      return
    }
    taskSearchRef.current = ""
    setTaskSearch("")
    setEngagementError("")
    setSearch("")
    selectEngagement(
      newNumberEngagement(resolved.value, practice.locations, locationScopeID),
    )
  }

  function updateTaskProjection(task: Task, select = true) {
    const recovery =
      task.origin === "MISSED_CALL_RECOVERY" ||
      task.origin === "VOICEMAIL_RECOVERY"
    if (recovery) {
      const next = projectTaskUpdate(recoveryTasksRef.current, task)
      recoveryTasksRef.current = next
      setRecoveryTasks(next)
    } else {
      const next = projectTaskUpdate(tasksRef.current, task)
      tasksRef.current = next
      setTasks(next)
    }
    if (select) {
      selectTask(task)
    }
  }

  function openTaskContext(task: Task) {
    callDetailGenerationRef.current += 1
    setSearch("")
    setHistoricalCall(undefined)
    setSelectedAIInteractionID("")
    updateTaskProjection(task, false)
    updateSelectedTask(task)
    setContextView("task")
    setContextPanelOpen(true)
  }

  async function openCallContext(callID: string) {
    const requestGeneration = ++callDetailGenerationRef.current
    const requestScope = snapshotScopeRef.current
    const token = await getAccessToken()
    if (!token) return
    const result = await getCallingCall({
      client: portalClient(token),
      path: { callId: callID },
    }).catch(() => undefined)
    if (
      !result?.data ||
      requestGeneration !== callDetailGenerationRef.current ||
      requestScope !== snapshotScopeRef.current
    ) {
      return
    }
    focusedCallIDRef.current = callID
    setSelectedAIInteractionID("")
    updateSelectedTask(undefined)
    setHistoricalCall(result.data)
    setContextView("call")
    setContextPanelOpen(true)
  }

  function closeContextPanel() {
    callDetailGenerationRef.current += 1
    setContextPanelOpen(false)
  }

  const handleCallChanged = useCallback((call: CallingCall | undefined) => {
    setActiveCall(call)
    const previousCallID = activeCallIDRef.current
    activeCallIDRef.current = call?.id ?? ""
    if (!call) return
    if (call.state !== "CONNECTED" || call.id === focusedCallIDRef.current) return
    if (call.id !== previousCallID) {
      callDetailGenerationRef.current += 1
      setHistoricalCall(undefined)
    }
    focusedCallIDRef.current = call.id
    setSelectedAIInteractionID("")
    const returnTask = selectedTaskRef.current
    if (returnTask?.phone !== call.phone) updateSelectedTask(undefined)
    setSelectedEngagement(callEngagement(call))
    setContextView("call")
    setContextPanelOpen(true)
    setView("engagement")
  }, [])

  async function handleDisposition(result: CallingDispositionResult) {
    focusedCallIDRef.current = ""
    await loadTasks()
    if (result.taskId) {
      const token = await getAccessToken()
      if (!token) return
      const task = await readTask({
        client: portalClient(token),
        path: { taskId: result.taskId },
      }).catch(() => undefined)
      if (task?.data) {
        setSearch("")
        updateTaskProjection(task.data)
        return
      }
    }
    const previous = tasksRef.current.find(
      (task) => task.id === returnTaskIDRef.current,
    )
    if (previous) {
      selectTask(previous)
    } else {
      const nextTask = tasksRef.current[0]
      if (nextTask) selectTask(nextTask)
      else setContextPanelOpen(false)
    }
  }

  if (session.isPending || (loadState === "loading" && !discovery)) {
    return <WorkspaceLoading />
  }
  if (loadState === "unauthorized") {
    return (
      <WorkspaceFailure
        title="Workspace access unavailable"
        description="Your identity is valid, but current Practice or Location authority is not available."
        action="Return to sign in"
        onAction={() =>
          void authClient.signOut().then((result) => {
            if (!result.error) router.push("/sign-in")
          })
        }
      />
    )
  }
  if (!discovery) {
    return (
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => {
          void loadAuthority()
        }}
      />
    )
  }

  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  const callingEnabled = practice.callingEnabled
  const contextPanelLabel =
    contextView === "task"
      ? "Task context"
      : contextView === "call"
        ? "Call context"
        : "Appointment context"
  const contextPanelTitle =
    contextView === "appointment" ? "Appointment details" : contextPanelLabel
  const callingShell = (children: ReactNode) => (
    <SidebarProvider>
      <CallingDock
        callingEnabled={callingEnabled}
        practiceID={practiceID}
        taskCallRequest={taskCallRequest}
        onTaskCallHandled={(requestID, requestError) => {
          setTaskCallRequest((current) =>
            current?.id === requestID ? undefined : current,
          )
          setTaskCallError(requestError ?? "")
        }}
        onCallChanged={handleCallChanged}
        onDisposition={(result) => void handleDisposition(result)}
      >
        {children}
      </CallingDock>
    </SidebarProvider>
  )
  if (loadState === "loading" && !workspace) {
    return callingShell(<WorkspaceLoading />)
  }
  if (loadState === "unavailable" || !workspace) {
    return callingShell(
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => {
          if (practiceID && locationID) {
            setLoadState("loading")
            workspaceSyncRef.current?.refresh()
            return
          }
          void loadAuthority()
        }}
      />,
    )
  }
  return callingShell(
    <>
        <TaskRail
          discovery={discovery}
          practice={practice}
          workspaceControl={
            <WorkspaceSelector
              discovery={discovery}
              practiceID={practiceID}
              locationScopeID={locationScopeID}
              onSelect={selectWorkspaceScope}
            />
          }
          availabilityControl={<CallingAvailabilityControl />}
          locationScopeID={locationScopeID}
          tasks={tasks}
          recoveryTasks={recoveryTasks}
          taskCounts={taskCounts}
          messages={messageThreads}
          aiOutcomes={aiOutcomes}
          outcomeCounts={aiOutcomeCounts}
          selectedTaskID={selectedTask?.id ?? ""}
          selectedAIInteractionID={selectedAIInteractionID}
          selectedPhone={selectedEngagement?.phone ?? ""}
          search={search}
          engagementError={engagementError}
          loading={tasksLoading}
          recoveryLoading={recoveryTasksLoading}
          messageLoading={messagesLoading}
          outcomesLoading={aiOutcomesLoading}
          outcomesError={aiOutcomesError}
          outcomeNextCursor={aiOutcomeNextCursor}
          nextCursor={nextCursor}
          recoveryNextCursor={recoveryNextCursor}
          messageNextCursor={messageNextCursor}
          connection={connection}
          analyticsActive={view === "analytics"}
          onSearchChange={(value) => {
            setSearch(value)
            setEngagementError("")
          }}
          onSearchSubmit={submitSearch}
          onAnalyticsSelect={() => {
            setContextPanelOpen(false)
            setView("analytics")
          }}
          onEngagementSelect={selectEngagement}
          onTaskSelect={selectTask}
          onTaskUpdated={(task) => {
            updateTaskProjection(task, false)
            if (selectedTaskRef.current?.id === task.id) {
              updateSelectedTask(task)
            }
            void loadTasks()
          }}
          onAIInteractionSelect={selectAIInteraction}
          onLoadMore={() => void loadTasks(nextCursor, true)}
          onRecoveryLoadMore={() =>
            void loadRecoveryTasks(recoveryNextCursor, true)
          }
          onMessageLoadMore={() =>
            void loadMessageThreads(messageNextCursor, true)
          }
          onOutcomeLoadMore={() =>
            void loadAIOutcomes(aiOutcomeNextCursor, true)
          }
        />
        <SidebarInset
          data-testid="mounted-workspace"
          data-workspace-version={workspace.version}
          className="h-svh min-h-0 min-w-0 overflow-hidden"
        >
          {view !== "engagement" && (
            <header className="flex h-12 shrink-0 items-center gap-3 border-b px-3">
              <SidebarTrigger />
              <div className="flex-1" />
            </header>
          )}
          {view === "analytics" ? (
            <OperatorAnalytics
              practiceID={practiceID}
              locationScopeID={locationScopeID}
            />
          ) : view === "engagement" && selectedEngagement ? (
            <div className="relative flex min-h-0 flex-1 bg-muted/20">
              <div className="flex min-h-0 min-w-0 flex-1 bg-background">
                <EngagementWorkspace
                  key={selectedEngagement.phone}
                  engagement={selectedEngagement}
                  practiceID={practiceID}
                  canMutate
                  revision={workspaceRevision}
                  headerLeading={<SidebarTrigger />}
                  onTaskCreated={(task) => updateTaskProjection(task, false)}
                  onTaskOpen={openTaskContext}
                  onCallOpen={(callID) => void openCallContext(callID)}
                  onAIInteractionOpen={openAIInteractionContext}
                />
              </div>
              <aside
                aria-label={contextPanelLabel}
                aria-hidden={!contextPanelOpen}
                data-state={contextPanelOpen ? "open" : "closed"}
                data-testid="context-panel"
                inert={!contextPanelOpen}
                className={cn(
                  "absolute top-3 right-3 flex h-fit max-h-[calc(100%-1.5rem)] w-[calc(100%-1.5rem)] max-w-[20rem] self-start flex-col overflow-hidden rounded-xl border bg-popover shadow-lg transition-[width,margin,opacity,transform,border-color,box-shadow] duration-200 ease-out motion-reduce:transition-none lg:relative lg:inset-auto lg:my-3 lg:max-w-none lg:shrink-0",
                  contextPanelOpen
                    ? "translate-x-0 opacity-100 lg:mr-3 lg:w-72"
                    : "pointer-events-none translate-x-4 border-transparent opacity-0 shadow-none lg:mr-0 lg:w-0",
                )}
                onTransitionEnd={(event) => {
                  if (event.currentTarget === event.target && !contextPanelOpen) {
                    setHistoricalCall(undefined)
                  }
                }}
              >
                <div className="flex h-12 shrink-0 items-center border-b px-4">
                  <p className="text-sm font-medium">
                    {contextPanelTitle}
                  </p>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className="ml-auto"
                    aria-label="Close context panel"
                    onClick={closeContextPanel}
                  >
                    <PanelRightCloseIcon />
                  </Button>
                </div>
                <div className="flex min-h-0 flex-1">
                  {contextView === "appointment" ? (
                    <AIInteractionContext
                      interactionID={selectedAIInteractionID}
                      onReview={reviewAIOutcome}
                    />
                  ) : (
                    <InteractionWorkspace
                      task={selectedTask}
                      activeCall={historicalCall ?? activeCall}
                      view={contextView}
                      canMutate
                      canCall={callingEnabled}
                      historyHint={workspaceRevision}
                      taskCallPending={Boolean(taskCallRequest)}
                      taskCallError={taskCallError}
                      onTaskUpdated={(task) => {
                        const recovery =
                          task.origin === "MISSED_CALL_RECOVERY" ||
                          task.origin === "VOICEMAIL_RECOVERY"
                        updateTaskProjection(task, false)
                        updateSelectedTask(task)
                        setContextView("task")
                        setContextPanelOpen(true)
                        void (recovery ? loadRecoveryTasks() : loadTasks())
                      }}
                      onStartTaskCall={(task) => {
                        setTaskCallError("")
                        returnTaskIDRef.current = task.id
                        setTaskCallRequest({
                          id: window.crypto.randomUUID(),
                          taskID: task.id,
                        })
                      }}
                      onReturnToCall={() => {
                        if (!activeCall) return
                        setContextView("call")
                        setContextPanelOpen(true)
                      }}
                    />
                  )}
                </div>
              </aside>
            </div>
          ) : (
            <section aria-label="No number selected" className="min-h-0 flex-1" />
          )}
        </SidebarInset>
    </>,
  )
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
  return {
    tasks: 0,
    bookings: 0,
    cancellations: 0,
    reschedules: 0,
  }
}

function WorkspaceSelector({
  discovery,
  practiceID,
  locationScopeID,
  onSelect,
}: {
  discovery: AccessDiscovery
  practiceID: string
  locationScopeID: string
  onSelect: (practiceID: string, locationID: string) => void
}) {
  const [open, setOpen] = useState(false)
  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  if (!practice) return null
  const selectedLocationID = locationScopeID
  const locationLabel = selectedLocationID
    ? (practice.locations.find((item) => item.id === selectedLocationID)?.name ??
      "Office")
    : "All offices"

  function select(nextPracticeID: string, nextLocationID: string) {
    onSelect(nextPracticeID, nextLocationID)
    setOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            aria-label="Workspace selector"
            variant="ghost"
            size="sm"
            className="h-auto w-full min-w-0 justify-start gap-2 px-1 py-1 text-left"
          />
        }
      >
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-semibold tracking-[-0.01em]">
            {practice.name}
          </span>{" "}
          <span className="block truncate text-[0.6875rem] font-normal text-muted-foreground">
            {locationLabel}
          </span>
        </span>
        <ChevronsUpDownIcon data-icon="inline-end" />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80">
        <PopoverHeader>
          <PopoverTitle>Workspace</PopoverTitle>
          <PopoverDescription>
            Choose an authorized Practice and Location.
          </PopoverDescription>
        </PopoverHeader>
        <div className="flex max-h-80 flex-col gap-3 overflow-y-auto">
          {discovery.practices.map((item) => (
            <div key={item.id} className="flex flex-col gap-1">
              <p className="px-2 font-medium">{item.name}</p>
              {item.locations.length > 1 && (
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  className="w-full justify-start"
                  aria-current={
                    item.id === practiceID && !locationScopeID
                      ? "page"
                      : undefined
                  }
                  onClick={() => select(item.id, "")}
                >
                  {item.id === practiceID && !locationScopeID && (
                    <CheckIcon data-icon="inline-start" />
                  )}
                  All offices
                </Button>
              )}
              {item.locations.map((location) => {
                const selected =
                  item.id === practiceID && location.id === selectedLocationID
                return (
                  <Button
                    key={location.id}
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="w-full justify-start"
                    aria-current={selected ? "page" : undefined}
                    onClick={() => select(item.id, location.id)}
                  >
                    {selected && <CheckIcon data-icon="inline-start" />}
                    {location.name}
                  </Button>
                )
              })}
            </div>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function workspaceTaskQueryKey(
  practiceID: string,
  locationID: string,
  search: string,
) {
  return `${practiceID}:${locationID}:OPEN:priority:work:${search}`
}

function workspaceRecoveryTaskQueryKey(
  practiceID: string,
  locationID: string,
  search: string,
) {
  return `${practiceID}:${locationID}:OPEN:recent:missed_calls:${search}`
}

function workspaceMessageQueryKey(
  practiceID: string,
  locationID: string,
) {
  return `${practiceID}:${locationID}`
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
  const authorizedLocations = locationScopeID
    ? locations.filter((location) => location.id === locationScopeID)
    : locations
  return {
    phone,
    locations: authorizedLocations,
    latestActivity: new Date().toISOString(),
    openTaskCount: 0,
    unread: false,
  }
}

function WorkspaceLoading() {
  return (
    <div className="flex min-h-svh w-full" aria-busy="true">
      <aside className="hidden w-64 border-r p-4 md:block">
        <Skeleton className="h-9 w-40" />
        <Skeleton className="mt-5 h-8 w-full" />
        <Skeleton className="mt-3 h-64 w-full" />
      </aside>
      <main className="flex flex-1 items-center justify-center">
        <Skeleton className="p-3" role="status">
          <AcuityMark className="size-12" />
          <span className="sr-only">Loading Acuity workspace</span>
        </Skeleton>
      </main>
    </div>
  )
}

function WorkspaceFailure({
  title,
  description,
  action,
  onAction,
}: {
  title: string
  description: string
  action: string
  onAction: () => void
}) {
  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/40 p-6">
      <Alert className="max-w-md" variant="destructive">
        <WifiOffIcon />
        <AlertTitle>{title}</AlertTitle>
        <AlertDescription>
          <p>{description}</p>
          <Button className="mt-4" variant="outline" onClick={onAction}>
            {action}
          </Button>
        </AlertDescription>
      </Alert>
    </main>
  )
}
