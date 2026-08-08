"use client"

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  CheckIcon,
  ChevronsUpDownIcon,
  PanelRightCloseIcon,
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
import { EngagementWorkspace } from "@/components/workspace/message-workspace"
import {
  type ConnectionState,
  TaskRail,
} from "@/components/workspace/task-rail"
import { portalClient, realtimeURL } from "@/lib/api/client"
import {
  discoverAccess,
  enterSupportMode,
  getCallingCall,
  getWorkspace,
  markMessageThreadRead,
  queryMessageThreads,
  queryTasks,
  readTask,
  revokeSupportMode,
} from "@/lib/api/generated/sdk.gen"
import type {
  AccessDiscovery,
  CallingCall,
  CallingDispositionResult,
  EngagementSummary,
  MessageThreadSummary,
  Task,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"
import { normalizeUSPhone } from "@/lib/phone"
import {
  createWorkspaceSync,
  type WorkspaceSync,
  WorkspaceSyncUnauthorizedError,
} from "@/lib/workspace-sync/workspace-sync"

type LoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type View = "none" | "engagement"
type ContextView = "none" | "task" | "call"

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"
const taskScopeStorageKey = "acuity.taskLocationScope"
const taskOrderingStorageKey = "acuity.taskOrdering"
const recentNumbersStorageKey = "acuity.recentNumberInboxes"
type TaskOrdering = "recent" | "priority"

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
  const [ordering, setOrdering] = useState<TaskOrdering>("priority")
  const [engagementError, setEngagementError] = useState("")
  const [selectedEngagement, setSelectedEngagement] = useState<EngagementSummary>()
  const [recentInboxes, setRecentInboxes] = useState<EngagementSummary[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [nextCursor, setNextCursor] = useState("")
  const [tasksLoading, setTasksLoading] = useState(false)
  const [messageThreads, setMessageThreads] = useState<MessageThreadSummary[]>(
    [],
  )
  const [messageNextCursor, setMessageNextCursor] = useState("")
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [selectedTask, setSelectedTask] = useState<Task>()
  const [view, setView] = useState<View>("none")
  const [contextView, setContextView] = useState<ContextView>("none")
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [historicalCall, setHistoricalCall] = useState<CallingCall>()
  const [workspaceRevision, setWorkspaceRevision] = useState(0)
  const [taskCallRequest, setTaskCallRequest] = useState<{
    id: string
    taskID: string
  }>()
  const [taskCallError, setTaskCallError] = useState("")
  const selectedTaskRef = useRef<Task | undefined>(undefined)
  const workspaceRef = useRef<WorkspaceSnapshot | undefined>(undefined)
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
  const orderingRef = useRef<TaskOrdering>("priority")
  const locationScopeRef = useRef("")
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
    orderingRef.current = ordering
  }, [ordering])
  useEffect(() => {
    locationScopeRef.current = locationScopeID
  }, [locationScopeID])
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
          state: "OPEN",
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
      } else if (firstLoad && next[0] && viewRef.current === "none") {
        const engagement = taskEngagement(next[0])
        updateSelectedTask(next[0])
        setSelectedEngagement(engagement)
        setContextView("task")
        setView("engagement")
      }
    },
    [locationScopeID, ordering, practiceID],
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
      const taskOrdering = orderingRef.current
      const selectedTaskID = selectedTaskRef.current?.id
      const taskQueryKey = workspaceTaskQueryKey(
        scope.practiceID,
        taskLocationID,
        taskOrdering,
      )
      const messageQueryKey = workspaceMessageQueryKey(
        scope.practiceID,
        taskLocationID,
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
              state: "OPEN",
              ordering: taskOrdering,
              limit: 50,
            },
            signal,
          }).catch(() => undefined),
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
                setContextView("none")
              }
            } else if (
              firstLoad &&
              tasksWithSelection[0] &&
              viewRef.current === "none"
            ) {
              const engagement = taskEngagement(tasksWithSelection[0])
              updateSelectedTask(tasksWithSelection[0])
              setSelectedEngagement(engagement)
              setContextView("task")
              setView("engagement")
            }
          }

          if (messageGeneration === messageQueryGenerationRef.current) {
            setMessagesLoading(false)
            hasLoadedThreadsRef.current = true
            messageQueryKeyRef.current = messageQueryKey
            messageThreadsRef.current = nextMessages
            setMessageThreads(nextMessages)
            setMessageNextCursor(messageResult.data.nextCursor)
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
    setRecentInboxes(
      readRecentInboxes(result.data.actor.subject, practice.id),
    )
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
    const sync = createWorkspaceSync({
      realtimeURL: realtimeURL(),
      getToken: getAccessToken,
      reconcile: (input) => reconcileWorkspaceRef.current(input),
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
    setHistoricalCall(undefined)
    setContextView("none")
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
    setHistoricalCall(undefined)
    setContextView("none")
    setView("none")

    const nextOrdering = readTaskOrdering(
      discovery.actor.subject,
      nextPractice.id,
    )
    const nextScope = nextLocationScopeID
    orderingRef.current = nextOrdering
    locationScopeRef.current = nextScope
    workspaceRef.current = undefined
    setWorkspace(undefined)
    setOrdering(nextOrdering)
    setRecentInboxes(
      readRecentInboxes(discovery.actor.subject, nextPractice.id),
    )
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
  }

  function selectEngagement(engagement: EngagementSummary, focusedTask?: Task) {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    setContextView("none")
    updateSelectedTask(focusedTask)
    setSelectedEngagement(engagement)
    if (discovery && practiceID) {
      const next = rememberEngagement(recentInboxes, engagement)
      setRecentInboxes(next)
      writeRecentInboxes(discovery.actor.subject, practiceID, next)
    }
    setView("engagement")
    void markEngagementRead(engagement.phone)
  }

  async function markEngagementRead(phone: string) {
    const unreadThreadIDs = messageThreadsRef.current
      .filter((thread) => thread.externalPhone === phone && thread.unread)
      .map((thread) => thread.id)
    if (unreadThreadIDs.length === 0) return
    const token = await getAccessToken()
    if (!token) return
    const supportSessionID = workspaceRef.current?.supportMode?.id ?? ""
    const results = await Promise.all(
      unreadThreadIDs.map(async (threadID) => {
        const result = await markMessageThreadRead({
          client: portalClient(token),
          path: { threadId: threadID },
          body: {
            ...(supportSessionID ? { supportSessionId: supportSessionID } : {}),
          },
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
    setRecentInboxes((current) => {
      const next = current.map((engagement) =>
        engagement.phone === phone
          ? { ...engagement, unread: false }
          : engagement,
      )
      if (discovery && practiceID) {
        writeRecentInboxes(discovery.actor.subject, practiceID, next)
      }
      return next
    })
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

  function submitPhoneSearch() {
    const phone = normalizeUSPhone(search)
    if (!phone || !practiceID) {
      setEngagementError("Enter a complete US phone number.")
      return
    }
    setEngagementError("")
    setSearch("")
    selectEngagement(
      newNumberEngagement(phone, practice.locations, locationScopeID),
    )
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
      selectTask(task)
    }
  }

  function openTaskContext(task: Task) {
    callDetailGenerationRef.current += 1
    setSearch("")
    setHistoricalCall(undefined)
    updateTaskProjection(task, false)
    updateSelectedTask(task)
    setContextView("task")
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
    updateSelectedTask(undefined)
    setHistoricalCall(result.data)
    setContextView("call")
  }

  function closeContextPanel() {
    callDetailGenerationRef.current += 1
    setContextView("none")
    setHistoricalCall(undefined)
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
    const returnTask = selectedTaskRef.current
    if (returnTask?.phone !== call.phone) updateSelectedTask(undefined)
    setSelectedEngagement(callEngagement(call))
    setContextView("call")
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
      else setContextView("none")
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
          workspaceControl={
            <WorkspaceSelector
              discovery={discovery}
              practiceID={practiceID}
              locationScopeID={locationScopeID}
              onSelect={selectWorkspaceScope}
            />
          }
          locationScopeID={locationScopeID}
          tasks={tasks}
          messages={messageThreads}
          recent={recentInboxes}
          selectedTaskID={selectedTask?.id ?? ""}
          selectedPhone={selectedEngagement?.phone ?? ""}
          search={search}
          engagementError={engagementError}
          loading={tasksLoading}
          messageLoading={messagesLoading}
          nextCursor={nextCursor}
          messageNextCursor={messageNextCursor}
          connection={connection}
          onSearchChange={(value) => {
            setSearch(value)
            setEngagementError("")
          }}
          onSearchSubmit={submitPhoneSearch}
          onEngagementSelect={selectEngagement}
          onTaskSelect={selectTask}
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
          {view !== "engagement" && (
            <header className="flex h-12 shrink-0 items-center gap-3 border-b px-3">
              <SidebarTrigger />
              <div className="flex-1" />
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
          )}
          {view === "engagement" && selectedEngagement ? (
            <div className="relative flex min-h-0 flex-1 bg-muted/20">
              <div className="flex min-h-0 min-w-0 flex-1 bg-background">
                <EngagementWorkspace
                  key={selectedEngagement.phone}
                  engagement={selectedEngagement}
                  practiceID={practiceID}
                  supportSessionID={workspace.supportMode?.id ?? ""}
                  canMutate={
                    !workspace.platformOperator || Boolean(workspace.supportMode)
                  }
                  revision={workspaceRevision}
                  headerLeading={<SidebarTrigger />}
                  headerTrailing={
                    <>
                      <CallingAvailabilityControl />
                      {workspace.platformOperator && !workspace.supportMode && (
                        <SupportDialog
                          practiceID={practiceID}
                          onEntered={() =>
                            void loadSnapshot(practiceID, locationID, false)
                          }
                        />
                      )}
                    </>
                  }
                  onTaskCreated={(task) => updateTaskProjection(task, false)}
                  onTaskOpen={openTaskContext}
                  onCallOpen={(callID) => void openCallContext(callID)}
                />
              </div>
              {contextView !== "none" &&
                ((contextView === "task" && selectedTask) ||
                  (contextView === "call" && (historicalCall || activeCall))) && (
                  <aside
                    aria-label={`${contextView === "task" ? "Task" : "Call"} context`}
                    className="absolute inset-y-3 right-3 flex w-[calc(100%-1.5rem)] max-w-sm flex-col overflow-hidden rounded-xl border bg-popover shadow-lg lg:relative lg:inset-auto lg:my-3 lg:mr-3 lg:w-96 lg:max-w-none lg:shrink-0"
                  >
                    <div className="flex h-12 shrink-0 items-center border-b px-4">
                      <p className="text-sm font-medium">
                        {contextView === "task" ? "Task context" : "Call context"}
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
                      <InteractionWorkspace
                        task={selectedTask}
                        activeCall={historicalCall ?? activeCall}
                        view={contextView}
                        supportSessionID={workspace.supportMode?.id ?? ""}
                        canMutate={
                          !workspace.platformOperator ||
                          Boolean(workspace.supportMode)
                        }
                        historyHint={workspaceRevision}
                        taskCallPending={Boolean(taskCallRequest)}
                        taskCallError={taskCallError}
                        onTaskUpdated={(task) => {
                          updateTaskProjection(task, false)
                          updateSelectedTask(task)
                          setContextView("task")
                          void loadTasks()
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
                        }}
                      />
                    </div>
                  </aside>
                )}
            </div>
          ) : (
            <section aria-label="No number selected" className="min-h-0 flex-1" />
          )}
        </SidebarInset>
      </CallingDock>
    </SidebarProvider>
  )
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

function taskOrderingKey(userSubject: string, practiceID: string) {
  return `${taskOrderingStorageKey}.${userSubject}.${practiceID}`
}

function workspaceTaskQueryKey(
  practiceID: string,
  locationID: string,
  ordering: TaskOrdering,
) {
  return `${practiceID}:${locationID}:OPEN:${ordering}`
}

function workspaceMessageQueryKey(
  practiceID: string,
  locationID: string,
) {
  return `${practiceID}:${locationID}`
}

function readTaskOrdering(
  userSubject: string,
  practiceID: string,
): TaskOrdering {
  const stored = window.localStorage.getItem(
    taskOrderingKey(userSubject, practiceID),
  )
  return stored === "recent" ? "recent" : "priority"
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

function rememberEngagement(
  current: EngagementSummary[],
  engagement: EngagementSummary,
) {
  return [
    engagement,
    ...current.filter((item) => item.phone !== engagement.phone),
  ].slice(0, 7)
}

function recentInboxesKey(userSubject: string, practiceID: string) {
  return `${recentNumbersStorageKey}.${userSubject}.${practiceID}`
}

function readRecentInboxes(userSubject: string, practiceID: string) {
  try {
    const value = JSON.parse(
      window.sessionStorage.getItem(recentInboxesKey(userSubject, practiceID)) ??
        "[]",
    ) as EngagementSummary[]
    return Array.isArray(value) ? value.slice(0, 7) : []
  } catch {
    return []
  }
}

function writeRecentInboxes(
  userSubject: string,
  practiceID: string,
  engagements: EngagementSummary[],
) {
  window.sessionStorage.setItem(
    recentInboxesKey(userSubject, practiceID),
    JSON.stringify(engagements),
  )
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
