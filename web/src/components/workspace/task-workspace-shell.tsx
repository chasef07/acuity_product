"use client"

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  CheckIcon,
  ChevronsUpDownIcon,
  ShieldCheckIcon,
  WifiOffIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
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
import { Spinner } from "@/components/ui/spinner"
import {
  CallingAvailabilityControl,
  CallingDock,
} from "@/components/workspace/calling-dock"
import { InteractionWorkspace } from "@/components/workspace/interaction-workspace"
import { MessageWorkspace } from "@/components/workspace/message-workspace"
import {
  type ConnectionState,
  type RailMode,
  TaskRail,
} from "@/components/workspace/task-rail"
import { portalClient, realtimeURL } from "@/lib/api/client"
import {
  discoverAccess,
  enterSupportMode,
  getCallingCall,
  getWorkspace,
  queryMessageThreads,
  queryTasks,
  readTask,
  revokeSupportMode,
} from "@/lib/api/generated/sdk.gen"
import type {
  AccessDiscovery,
  CallingCall,
  CallingDispositionResult,
  Message,
  MessageThreadSummary,
  Task,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"
import {
  createWorkspaceSync,
  type WorkspaceSync,
  WorkspaceSyncUnauthorizedError,
} from "@/lib/workspace-sync/workspace-sync"

type LoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type View = "none" | "task" | "call" | "message"

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"
const taskScopeStorageKey = "acuity.taskLocationScope"
const taskOrderingStorageKey = "acuity.taskOrdering"
type TaskOrdering = "time" | "priority"

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
  const [settledSearch, setSettledSearch] = useState("")
  const [ordering, setOrdering] = useState<TaskOrdering>("time")
  const [railMode, setRailMode] = useState<RailMode>("tasks")
  const [tasks, setTasks] = useState<Task[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [tasksLoading, setTasksLoading] = useState(false)
  const [messageThreads, setMessageThreads] = useState<MessageThreadSummary[]>(
    [],
  )
  const [messageNextCursor, setMessageNextCursor] = useState("")
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [selectedTask, setSelectedTask] = useState<Task>()
  const [selectedThread, setSelectedThread] = useState<MessageThreadSummary>()
  const [committedMessage, setCommittedMessage] = useState<Message>()
  const [composingNew, setComposingNew] = useState(false)
  const [view, setView] = useState<View>("none")
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [historicalCall, setHistoricalCall] = useState<CallingCall>()
  const [callRefreshRevision, setCallRefreshRevision] = useState(0)
  const [workspaceRevision, setWorkspaceRevision] = useState(0)
  const [taskCallRequest, setTaskCallRequest] = useState<{
    id: string
    taskID: string
  }>()
  const [taskCallError, setTaskCallError] = useState("")
  const selectedTaskRef = useRef<Task | undefined>(undefined)
  const selectedThreadRef = useRef<MessageThreadSummary | undefined>(undefined)
  const workspaceRef = useRef<WorkspaceSnapshot | undefined>(undefined)
  const composingNewRef = useRef(false)
  const tasksRef = useRef<Task[]>([])
  const messageThreadsRef = useRef<MessageThreadSummary[]>([])
  const hasLoadedTasksRef = useRef(false)
  const hasLoadedThreadsRef = useRef(false)
  const taskQueryGenerationRef = useRef(0)
  const messageQueryGenerationRef = useRef(0)
  const taskQueryKeyRef = useRef("")
  const messageQueryKeyRef = useRef("")
  const snapshotGenerationRef = useRef(0)
  const snapshotScopeRef = useRef("")
  const viewRef = useRef<View>("none")
  const railModeRef = useRef<RailMode>("tasks")
  const settledSearchRef = useRef("")
  const orderingRef = useRef<TaskOrdering>("time")
  const locationScopeRef = useRef("")
  const workspaceSyncRef = useRef<WorkspaceSync | undefined>(undefined)
  const returnTaskIDRef = useRef("")
  const focusedCallIDRef = useRef("")
  const activeCallIDRef = useRef("")
  const callDetailGenerationRef = useRef(0)

  useEffect(() => {
    selectedThreadRef.current = selectedThread
  }, [selectedThread])
  useEffect(() => {
    workspaceRef.current = workspace
  }, [workspace])
  useEffect(() => {
    composingNewRef.current = composingNew
  }, [composingNew])
  useEffect(() => {
    viewRef.current = view
  }, [view])
  useEffect(() => {
    settledSearchRef.current = settledSearch
  }, [settledSearch])
  useEffect(() => {
    orderingRef.current = ordering
  }, [ordering])
  useEffect(() => {
    locationScopeRef.current = locationScopeID
  }, [locationScopeID])
  useEffect(() => {
    const timeout = window.setTimeout(
      () => setSettledSearch(search.trim()),
      200,
    )
    return () => window.clearTimeout(timeout)
  }, [search])

  const loadSnapshot = useCallback(
    async (
      selectedPractice: string,
      selectedLocation: string,
      showLoading = true,
    ) => {
      const scope = `${selectedPractice}:${selectedLocation}`
      if (scope !== snapshotScopeRef.current) return false
      const requestGeneration = ++snapshotGenerationRef.current
      if (showLoading && !workspaceRef.current) setLoadState("loading")
      const token = await getAccessToken()
      if (
        requestGeneration !== snapshotGenerationRef.current ||
        scope !== snapshotScopeRef.current
      ) {
        return false
      }
      if (!token) {
        setLoadState("unauthorized")
        return false
      }
      const result = await getWorkspace({
        client: portalClient(token),
        query: {
          practiceId: selectedPractice,
          locationId: selectedLocation,
        },
      }).catch(() => undefined)
      if (
        requestGeneration !== snapshotGenerationRef.current ||
        scope !== snapshotScopeRef.current
      ) {
        return false
      }
      if (!result?.data) {
        const status = result?.response?.status
        if (status === 401 || status === 403) {
          setLoadState("unauthorized")
        } else if (!workspaceRef.current) {
          setLoadState("unavailable")
        }
        return false
      }
      if (
        workspaceRef.current &&
        result.data.version < workspaceRef.current.version
      ) {
        return true
      }
      workspaceRef.current = result.data
      setWorkspace(result.data)
      setLoadState("ready")
      return true
    },
    [],
  )

  const loadTasks = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID) return
      const queryKey = workspaceTaskQueryKey(
        practiceID,
        locationScopeID,
        settledSearch,
        ordering,
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
          ...(settledSearch ? { search: settledSearch } : {}),
          ordering,
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
      setNextCursor(result.data.nextCursor)

      const selected = selectedTaskRef.current
      if (selected) {
        const current = next.find((task) => task.id === selected.id)
        if (current) updateSelectedTask(current)
      } else if (firstLoad && next[0] && viewRef.current !== "call") {
        updateSelectedTask(next[0])
        setView("task")
      } else if (!next[0] && viewRef.current !== "call") {
        setView("none")
      }
    },
    [locationScopeID, ordering, practiceID, settledSearch],
  )
  const loadMessageThreads = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID || !locationID) return
      const queryKey = workspaceMessageQueryKey(
        practiceID,
        locationID,
        settledSearch,
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
          locationId: locationID,
          ...(settledSearch ? { search: settledSearch } : {}),
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
          setSelectedThread(undefined)
          setComposingNew(false)
          if (viewRef.current === "message") setView("none")
          setLoadState("unauthorized")
        }
        return
      }
      const firstLoad = !hasLoadedThreadsRef.current
      hasLoadedThreadsRef.current = true
      messageQueryKeyRef.current = queryKey
      const next = append
        ? [...messageThreadsRef.current, ...result.data.items]
        : result.data.items
      messageThreadsRef.current = next
      setMessageThreads(next)
      setMessageNextCursor(result.data.nextCursor)

      const selected = selectedThreadRef.current
      if (selected) {
        const current = next.find((item) => item.id === selected.id)
        if (current) setSelectedThread(current)
      } else if (
        firstLoad &&
        !composingNewRef.current &&
        next[0] &&
        viewRef.current !== "call"
      ) {
        setSelectedThread(next[0])
        setView("message")
      } else if (
        !next[0] &&
        !composingNewRef.current &&
        viewRef.current === "message"
      ) {
        setView("none")
      }
    },
    [locationID, practiceID, settledSearch],
  )
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
      const messageGeneration = ++messageQueryGenerationRef.current
      const taskLocationID = locationScopeRef.current
      const taskSearch = settledSearchRef.current
      const taskOrdering = orderingRef.current
      const selectedTaskID = selectedTaskRef.current?.id
      const taskQueryKey = workspaceTaskQueryKey(
        scope.practiceID,
        taskLocationID,
        taskSearch,
        taskOrdering,
      )
      const messageQueryKey = workspaceMessageQueryKey(
        scope.practiceID,
        scope.locationID,
        taskSearch,
      )
      const client = portalClient(token)
      const [snapshotResult, taskResult, messageResult, selectedResult] =
        await Promise.all([
          getWorkspace({
            client,
            query: {
              practiceId: scope.practiceID,
              locationId: scope.locationID,
            },
            signal,
          }).catch(() => undefined),
          queryTasks({
            client,
            body: {
              practiceId: scope.practiceID,
              ...(taskLocationID ? { locationId: taskLocationID } : {}),
              ...(taskSearch ? { search: taskSearch } : {}),
              ordering: taskOrdering,
              limit: 50,
            },
            signal,
          }).catch(() => undefined),
          queryMessageThreads({
            client,
            body: {
              practiceId: scope.practiceID,
              locationId: scope.locationID,
              ...(taskSearch ? { search: taskSearch } : {}),
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
        [snapshotResult, taskResult, messageResult].some(
          (result) =>
            result?.response?.status === 401 ||
            result?.response?.status === 403,
        )
      ) {
        throw new WorkspaceSyncUnauthorizedError()
      }
      if (!snapshotResult?.data || !taskResult?.data || !messageResult?.data) {
        throw new Error("workspace authority is unavailable")
      }
      if (snapshotResult.data.version < minimumVersion) {
        throw new Error("workspace authority has not reached the hinted version")
      }

      const snapshot = snapshotResult.data
      const nextTasks = taskResult.data.items
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
            const firstLoad = !hasLoadedTasksRef.current
            hasLoadedTasksRef.current = true
            taskQueryKeyRef.current = taskQueryKey
            const refreshed = selectedResult?.data
            const tasksWithSelection = refreshed
              ? nextTasks.map((task) =>
                  task.id === refreshed.id ? refreshed : task,
                )
              : nextTasks
            tasksRef.current = tasksWithSelection
            setTasks(tasksWithSelection)
            setNextCursor(taskResult.data.nextCursor)
            const selected = selectedTaskRef.current
            if (selected) {
              const current =
                refreshed?.id === selected.id
                  ? refreshed
                  : tasksWithSelection.find((task) => task.id === selected.id)
              if (current) updateSelectedTask(current)
              else if (
                selectedTaskID === selected.id &&
                (selectedResult?.response?.status === 401 ||
                  selectedResult?.response?.status === 403)
              ) {
                updateSelectedTask(undefined)
                if (viewRef.current !== "call") setView("none")
              }
            } else if (
              firstLoad &&
              tasksWithSelection[0] &&
              viewRef.current !== "call"
            ) {
              updateSelectedTask(tasksWithSelection[0])
              setView("task")
            } else if (!tasksWithSelection[0] && viewRef.current !== "call") {
              setView("none")
            }
          }

          if (messageGeneration === messageQueryGenerationRef.current) {
            const firstLoad = !hasLoadedThreadsRef.current
            hasLoadedThreadsRef.current = true
            messageQueryKeyRef.current = messageQueryKey
            messageThreadsRef.current = nextMessages
            setMessageThreads(nextMessages)
            setMessageNextCursor(messageResult.data.nextCursor)
            const selected = selectedThreadRef.current
            if (selected) {
              const current = nextMessages.find(
                (thread) => thread.id === selected.id,
              )
              if (current) setSelectedThread(current)
            } else if (
              firstLoad &&
              railModeRef.current === "messages" &&
              !composingNewRef.current &&
              nextMessages[0] &&
              viewRef.current !== "call"
            ) {
              setSelectedThread(nextMessages[0])
              setView("message")
            } else if (
              !nextMessages[0] &&
              !composingNewRef.current &&
              viewRef.current === "message"
            ) {
              setView("none")
            }
          }
          setWorkspaceRevision((current) => current + 1)
        },
      }
    },
    [],
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
    const initialOrdering = readTaskOrdering(
      result.data.actor.subject,
      practice.id,
    )
    orderingRef.current = initialOrdering
    locationScopeRef.current = scope
    setOrdering(initialOrdering)
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
      settledSearch,
      ordering,
    )
    if (taskQueryKeyRef.current === queryKey) return
    const timeout = window.setTimeout(() => void loadTasks(), 0)
    return () => window.clearTimeout(timeout)
  }, [
    loadState,
    loadTasks,
    locationScopeID,
    ordering,
    practiceID,
    settledSearch,
  ])

  useEffect(() => {
    if (
      railMode !== "messages" ||
      !practiceID ||
      !locationID ||
      loadState !== "ready"
    ) {
      return
    }
    const queryKey = workspaceMessageQueryKey(
      practiceID,
      locationID,
      settledSearch,
    )
    if (messageQueryKeyRef.current === queryKey) return
    const timeout = window.setTimeout(() => void loadMessageThreads(), 0)
    return () => window.clearTimeout(timeout)
  }, [
    loadMessageThreads,
    loadState,
    locationID,
    practiceID,
    railMode,
    settledSearch,
  ])

  useEffect(() => {
    const sync = createWorkspaceSync({
      realtimeURL: realtimeURL(),
      getToken: getAccessToken,
      reconcile: (input) => reconcileWorkspaceRef.current(input),
      onValidatedHint: () =>
        setCallRefreshRevision((current) => current + 1),
      onStateChange: setConnection,
      onUnauthorized: () => setLoadState("unauthorized"),
    })
    workspaceSyncRef.current = sync
    return () => {
      workspaceSyncRef.current = undefined
      sync.stop()
    }
  }, [])

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
    messageQueryGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    taskQueryKeyRef.current = ""
    messageQueryKeyRef.current = ""
    tasksRef.current = []
    messageThreadsRef.current = []
    setTasks([])
    setMessageThreads([])
    updateSelectedTask(undefined)
    setSelectedThread(undefined)
    setHistoricalCall(undefined)
    setComposingNew(false)
    if (viewRef.current !== "call") setView("none")
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
    messageQueryGenerationRef.current += 1
    snapshotGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    taskQueryKeyRef.current = ""
    messageQueryKeyRef.current = ""
    tasksRef.current = []
    messageThreadsRef.current = []
    setTasks([])
    setMessageThreads([])
    updateSelectedTask(undefined)
    setSelectedThread(undefined)
    setHistoricalCall(undefined)
    setComposingNew(false)
    if (viewRef.current !== "call") setView("none")

    const nextOrdering = readTaskOrdering(
      discovery.actor.subject,
      nextPractice.id,
    )
    const nextScope =
      railModeRef.current === "messages"
        ? nextLocation.id
        : nextLocationScopeID
    orderingRef.current = nextOrdering
    locationScopeRef.current = nextScope
    workspaceRef.current = undefined
    setWorkspace(undefined)
    setOrdering(nextOrdering)
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

  function updateRailMode(mode: RailMode) {
    railModeRef.current = mode
    setRailMode(mode)
  }

  function updateSelectedTask(task?: Task) {
    selectedTaskRef.current = task
    setSelectedTask(task)
  }

  function selectRailMode(mode: RailMode) {
    if (mode === railMode) return
    updateRailMode(mode)
    setSearch("")
    setSettledSearch("")
    if (mode === "messages") {
      const messageLocationID = locationScopeID || locationID
      if (messageLocationID !== locationScopeID) {
        locationScopeRef.current = messageLocationID
        taskQueryKeyRef.current = ""
        setLocationScopeID(messageLocationID)
        window.localStorage.setItem(
          `${taskScopeStorageKey}.${practiceID}`,
          messageLocationID,
        )
      }
      hasLoadedThreadsRef.current = false
      setView(
        activeCall
          ? "call"
          : selectedThreadRef.current || composingNewRef.current
            ? "message"
            : "none",
      )
      return
    }
    setView(
      activeCall
        ? "call"
        : selectedTaskRef.current
          ? "task"
          : tasksRef.current[0]
            ? "task"
            : "none",
    )
    if (!selectedTaskRef.current && tasksRef.current[0]) {
      updateSelectedTask(tasksRef.current[0])
    }
  }

  function selectTask(task: Task) {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    updateSelectedTask(task)
    if (activeCall) returnTaskIDRef.current = task.id
    setView("task")
  }

  function selectMessageThread(thread: MessageThreadSummary) {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    setCommittedMessage(undefined)
    setSelectedThread(thread)
    setComposingNew(false)
    setView("message")
  }

  function composeNewMessage() {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    setCommittedMessage(undefined)
    setSelectedThread(undefined)
    setComposingNew(true)
    setView("message")
  }

  function updateTaskProjection(task: Task, select = true) {
    const exists = tasksRef.current.some((current) => current.id === task.id)
    const next = exists
      ? tasksRef.current.map((current) =>
          current.id === task.id ? task : current,
        )
      : [task, ...tasksRef.current]
    tasksRef.current = next
    setTasks(next)
    if (select) {
      updateSelectedTask(task)
      if (activeCall) returnTaskIDRef.current = task.id
      setView("task")
    }
  }

  function handleMessageSent(message: Message) {
    setCommittedMessage(message)
    const summary: MessageThreadSummary = {
      ...message.thread,
      preview: message.body || message.attachment?.fileName || "Attachment",
      latestDirection: message.direction,
      latestDelivery: message.delivery,
      latestActivity: message.createdAt,
      unread: false,
    }
    setSelectedThread(summary)
    setComposingNew(false)
    setView("message")
    void loadMessageThreads()
  }

  async function openCallDetail(callID: string) {
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
    setHistoricalCall(result.data)
    setView("call")
  }

  const handleCallChanged = useCallback((call: CallingCall | undefined) => {
    setActiveCall(call)
    const previousCallID = activeCallIDRef.current
    activeCallIDRef.current = call?.id ?? ""
    if (!call) return
    const canFocus =
      call.state === "PREPARING" ||
      call.state === "RINGING" ||
      call.state === "CONNECTING" ||
      call.state === "RECONCILING" ||
      call.state === "CONNECTED" ||
      call.state === "NEEDS_DISPOSITION"
    if (!canFocus || call.id === focusedCallIDRef.current) return
    if (call.id !== previousCallID) {
      callDetailGenerationRef.current += 1
      setHistoricalCall(undefined)
    }
    focusedCallIDRef.current = call.id
    returnTaskIDRef.current =
      viewRef.current === "task" ? (selectedTaskRef.current?.id ?? "") : ""
    setView("call")
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
        updateRailMode("tasks")
        setSearch("")
        setSettledSearch("")
        updateTaskProjection(task.data)
        return
      }
    }
    const previous = tasksRef.current.find(
      (task) => task.id === returnTaskIDRef.current,
    )
    if (previous) {
      updateSelectedTask(previous)
      setView("task")
    } else {
      setView(tasksRef.current[0] ? "task" : "none")
      updateSelectedTask(tasksRef.current[0])
    }
  }

  if (session.isPending || (loadState === "loading" && !workspace)) {
    return <WorkspaceLoading />
  }
  if (loadState === "unauthorized") {
    return (
      <WorkspaceFailure
        title="Workspace access unavailable"
        description="Your identity is valid, but current Practice or Location authority is not available."
        action="Return to sign in"
        onAction={() =>
          void authClient.signOut().then(() => router.push("/sign-in"))
        }
      />
    )
  }
  if (loadState === "unavailable" || !discovery || !workspace) {
    return (
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => {
          if (discovery && practiceID && locationID) {
            setLoadState("loading")
            workspaceSyncRef.current?.refresh()
            return
          }
          void loadAuthority()
        }}
      />
    )
  }

  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  return (
    <SidebarProvider>
      <CallingDock
        platformOperator={workspace.platformOperator}
        practiceID={practiceID}
        locations={practice.locations}
        callRefreshRevision={callRefreshRevision}
        workspaceRevision={workspaceRevision}
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
        <TaskRail
          discovery={discovery}
          practice={practice}
          locationScopeID={locationScopeID}
          tasks={tasks}
          messages={messageThreads}
          mode={railMode}
          selectedTaskID={selectedTask?.id ?? ""}
          selectedThreadID={selectedThread?.id ?? ""}
          search={search}
          ordering={ordering}
          loading={tasksLoading}
          messageLoading={messagesLoading}
          nextCursor={nextCursor}
          messageNextCursor={messageNextCursor}
          connection={connection}
          onModeChange={selectRailMode}
          onSearchChange={(value) => {
            taskQueryGenerationRef.current += 1
            setSearch(value)
          }}
          onOrderingChange={(value) => {
            taskQueryGenerationRef.current += 1
            orderingRef.current = value
            setOrdering(value)
            window.localStorage.setItem(
              taskOrderingKey(discovery.actor.subject, practiceID),
              value,
            )
          }}
          onTaskSelect={selectTask}
          onThreadSelect={selectMessageThread}
          onNewText={composeNewMessage}
          onLoadMore={() => void loadTasks(nextCursor, true)}
          onMessageLoadMore={() =>
            void loadMessageThreads(messageNextCursor, true)
          }
        />
        <SidebarInset
          data-testid="mounted-workspace"
          data-workspace-version={workspace.version}
          className="min-w-0"
        >
          {workspace.supportMode && (
            <SupportBanner
              supportMode={workspace.supportMode}
              onChanged={() => void loadSnapshot(practiceID, locationID, false)}
            />
          )}
          <header className="flex h-12 shrink-0 items-center gap-3 border-b bg-card/40 px-3">
            <SidebarTrigger />
            <WorkspaceSelector
              discovery={discovery}
              practiceID={practiceID}
              locationID={locationID}
              locationScopeID={locationScopeID}
              mode={railMode}
              onSelect={selectWorkspaceScope}
            />
            <CallingAvailabilityControl />
            {workspace.platformOperator && !workspace.supportMode && (
              <SupportDialog
                practiceID={practiceID}
                onEntered={() =>
                  void loadSnapshot(practiceID, locationID, false)
                }
              />
            )}
          </header>
          {railMode === "messages" && view !== "call" ? (
            <MessageWorkspace
              thread={selectedThread}
              composingNew={composingNew}
              practiceID={practiceID}
              locationID={locationID}
              locationName={
                practice.locations.find((location) => location.id === locationID)
                  ?.name ?? "Office"
              }
              supportSessionID={workspace.supportMode?.id ?? ""}
              canMutate={
                !workspace.platformOperator || Boolean(workspace.supportMode)
              }
              revision={workspaceRevision}
              initialMessage={committedMessage}
              onMessageSent={handleMessageSent}
              onThreadRead={(threadID) => {
                const nextThreads = messageThreadsRef.current.map((thread) =>
                  thread.id === threadID ? { ...thread, unread: false } : thread,
                )
                messageThreadsRef.current = nextThreads
                setMessageThreads(nextThreads)
                const nextTasks = tasksRef.current.map((task) =>
                  task.conversationThreadId === threadID ||
                  task.messageThreadId === threadID
                    ? { ...task, unread: false }
                    : task,
                )
                tasksRef.current = nextTasks
                setTasks(nextTasks)
              }}
              onTaskCreated={(task) => {
                updateTaskProjection(task, false)
                void loadTasks()
              }}
              onTaskOpen={(task) => {
                updateRailMode("tasks")
                setSearch("")
                setSettledSearch("")
                updateTaskProjection(task)
              }}
              onCallOpen={(callID) => void openCallDetail(callID)}
            />
          ) : (
            <InteractionWorkspace
              task={selectedTask}
              activeCall={historicalCall ?? activeCall}
              view={view === "message" ? "none" : view}
              supportSessionID={workspace.supportMode?.id ?? ""}
              canMutate={
                !workspace.platformOperator || Boolean(workspace.supportMode)
              }
              historyHint={workspaceRevision}
              taskCallPending={Boolean(taskCallRequest)}
              taskCallError={taskCallError}
              onTaskUpdated={(task) => {
                updateTaskProjection(task)
                void loadTasks()
              }}
              onStartTaskCall={(task) => {
                setTaskCallError("")
                setTaskCallRequest({
                  id: window.crypto.randomUUID(),
                  taskID: task.id,
                })
              }}
              onReturnToCall={() => {
                if (activeCall) setView("call")
              }}
            />
          )}
        </SidebarInset>
      </CallingDock>
    </SidebarProvider>
  )
}

function WorkspaceSelector({
  discovery,
  practiceID,
  locationID,
  locationScopeID,
  mode,
  onSelect,
}: {
  discovery: AccessDiscovery
  practiceID: string
  locationID: string
  locationScopeID: string
  mode: RailMode
  onSelect: (practiceID: string, locationID: string) => void
}) {
  const [open, setOpen] = useState(false)
  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  if (!practice) return null
  const selectedLocationID =
    mode === "messages" ? locationID : locationScopeID
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
            className="min-w-0 max-w-80"
          />
        }
      >
        <span className="truncate">
          {practice.name} · {locationLabel}
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
              {mode === "tasks" && item.locations.length > 1 && (
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

function taskOrderingKey(userSubject: string, practiceID: string) {
  return `${taskOrderingStorageKey}.${userSubject}.${practiceID}`
}

function workspaceTaskQueryKey(
  practiceID: string,
  locationID: string,
  search: string,
  ordering: TaskOrdering,
) {
  return `${practiceID}:${locationID}:${search}:${ordering}`
}

function workspaceMessageQueryKey(
  practiceID: string,
  locationID: string,
  search: string,
) {
  return `${practiceID}:${locationID}:${search}`
}

function readTaskOrdering(
  userSubject: string,
  practiceID: string,
): TaskOrdering {
  const stored = window.localStorage.getItem(
    taskOrderingKey(userSubject, practiceID),
  )
  return stored === "priority" ? "priority" : "time"
}

function SupportDialog({
  practiceID,
  onEntered,
}: {
  practiceID: string
  onEntered: () => void
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const token = await getAccessToken()
    if (!token) {
      setPending(false)
      setError("Your authentication needs to be refreshed.")
      return
    }
    const result = await enterSupportMode({
      client: portalClient(token),
      body: {
        practiceId: practiceID,
        reason: String(data.get("reason") ?? ""),
        durationMinutes: Number(data.get("duration") ?? 30),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("Support Mode could not be entered.")
      return
    }
    setOpen(false)
    onEntered()
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button size="sm" variant="outline" />}>
        <ShieldCheckIcon />
        Enter Support Mode
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Enter Practice-scoped Support Mode</DialogTitle>
          <DialogDescription>
            Your Platform Operator identity remains the actor for every Task
            change.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <FieldGroup>
            <Field data-invalid={Boolean(error)}>
              <FieldLabel htmlFor="support-reason">Reason</FieldLabel>
              <Input
                id="support-reason"
                name="reason"
                minLength={8}
                maxLength={240}
                required
              />
              <FieldDescription>
                The reason is recorded with supported mutations.
              </FieldDescription>
              <FieldError>{error}</FieldError>
            </Field>
            <Field>
              <FieldLabel htmlFor="support-duration">Duration</FieldLabel>
              <NativeSelect
                id="support-duration"
                name="duration"
                defaultValue="30"
              >
                <NativeSelectOption value="15">15 minutes</NativeSelectOption>
                <NativeSelectOption value="30">30 minutes</NativeSelectOption>
                <NativeSelectOption value="60">1 hour</NativeSelectOption>
              </NativeSelect>
            </Field>
            <DialogFooter>
              <Button type="submit" disabled={pending}>
                {pending && <Spinner />}
                Enter Support Mode
              </Button>
            </DialogFooter>
          </FieldGroup>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SupportBanner({
  supportMode,
  onChanged,
}: {
  supportMode: NonNullable<WorkspaceSnapshot["supportMode"]>
  onChanged: () => void
}) {
  const [pending, setPending] = useState(false)

  async function exitSupport() {
    setPending(true)
    const token = await getAccessToken()
    if (token) {
      await revokeSupportMode({
        client: portalClient(token),
        path: { supportSessionId: supportMode.id },
      })
    }
    setPending(false)
    onChanged()
  }

  return (
    <div
      role="status"
      className="flex items-center gap-3 border-b border-warning/30 bg-warning/10 px-4 py-2 text-[13px]"
    >
      <ShieldCheckIcon className="size-4 stroke-[1.75] text-warning" />
      <span className="min-w-0 flex-1 truncate">
        Support Mode active · {supportMode.reason}
      </span>
      <Button
        variant="outline"
        size="sm"
        onClick={() => void exitSupport()}
        disabled={pending}
      >
        {pending && <Spinner />}
        Exit
      </Button>
    </div>
  )
}

function WorkspaceLoading() {
  return (
    <div className="flex min-h-svh w-full">
      <aside className="hidden w-64 border-r p-4 md:block">
        <Skeleton className="h-9 w-40" />
        <Skeleton className="mt-5 h-8 w-full" />
        <Skeleton className="mt-3 h-64 w-full" />
      </aside>
      <main className="flex flex-1 items-center justify-center">
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Spinner />
          Reconstructing authorized workspace
        </div>
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
