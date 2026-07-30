"use client"

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { ShieldCheckIcon, WifiOffIcon } from "lucide-react"

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
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { CallingDock } from "@/components/workspace/calling-dock"
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
  CallingOffer,
  Message,
  MessageThreadSummary,
  Task,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"

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
  const [callingHint, setCallingHint] = useState(0)
  const [callingOffers, setCallingOffers] = useState<CallingOffer[]>([])
  const selectedTaskRef = useRef<Task | undefined>(undefined)
  const selectedThreadRef = useRef<MessageThreadSummary | undefined>(undefined)
  const composingNewRef = useRef(false)
  const tasksRef = useRef<Task[]>([])
  const messageThreadsRef = useRef<MessageThreadSummary[]>([])
  const hasLoadedTasksRef = useRef(false)
  const hasLoadedThreadsRef = useRef(false)
  const taskQueryGenerationRef = useRef(0)
  const messageQueryGenerationRef = useRef(0)
  const snapshotGenerationRef = useRef(0)
  const snapshotScopeRef = useRef("")
  const viewRef = useRef<View>("none")
  const connectionRef = useRef<ConnectionState>("connecting")
  const returnTaskIDRef = useRef("")
  const focusedCallIDRef = useRef("")
  const activeCallIDRef = useRef("")
  const callDetailGenerationRef = useRef(0)

  useEffect(() => {
    selectedTaskRef.current = selectedTask
  }, [selectedTask])
  useEffect(() => {
    selectedThreadRef.current = selectedThread
  }, [selectedThread])
  useEffect(() => {
    composingNewRef.current = composingNew
  }, [composingNew])
  useEffect(() => {
    viewRef.current = view
  }, [view])
  useEffect(() => {
    connectionRef.current = connection
  }, [connection])
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
      if (showLoading) setLoadState("loading")
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
        setLoadState(
          status === 401 || status === 403 ? "unauthorized" : "unavailable",
        )
        return false
      }
      setWorkspace(result.data)
      setLoadState("ready")
      return true
    },
    [],
  )

  const loadTasks = useCallback(
    async (cursor = "", append = false) => {
      if (!practiceID) return
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
          setSelectedTask(undefined)
          setView("none")
          setLoadState("unauthorized")
        }
        return
      }
      const firstLoad = !hasLoadedTasksRef.current
      hasLoadedTasksRef.current = true
      const next = append
        ? [...tasksRef.current, ...result.data.items]
        : result.data.items
      tasksRef.current = next
      setTasks(next)
      setNextCursor(result.data.nextCursor)

      const selected = selectedTaskRef.current
      if (selected) {
        const current = next.find((task) => task.id === selected.id)
        if (current) setSelectedTask(current)
      } else if (firstLoad && next[0] && viewRef.current !== "call") {
        setSelectedTask(next[0])
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
  const refreshSelectedTask = useCallback(async () => {
    const current = selectedTaskRef.current
    if (!current) return
    const token = await getAccessToken()
    if (!token) return
    const result = await readTask({
      client: portalClient(token),
      path: { taskId: current.id },
    }).catch(() => undefined)
    const isStillSelected = selectedTaskRef.current?.id === current.id
    if (result?.data) {
      const refreshedTask = result.data
      const next = tasksRef.current.map((task) =>
        task.id === refreshedTask.id ? refreshedTask : task,
      )
      tasksRef.current = next
      setTasks(next)
      if (isStillSelected) setSelectedTask(refreshedTask)
      return
    }
    if (result?.response?.status === 401 || result?.response?.status === 403) {
      const next = tasksRef.current.filter((task) => task.id !== current.id)
      tasksRef.current = next
      setTasks(next)
      if (isStillSelected) {
        setSelectedTask(undefined)
        if (viewRef.current !== "call") setView("none")
      }
    }
  }, [])

  const snapshotRef = useRef(loadSnapshot)
  const taskRefetchRef = useRef(loadTasks)
  const messageRefetchRef = useRef(loadMessageThreads)
  const selectedRefetchRef = useRef(refreshSelectedTask)
  useEffect(() => {
    snapshotRef.current = loadSnapshot
  }, [loadSnapshot])
  useEffect(() => {
    taskRefetchRef.current = loadTasks
  }, [loadTasks])
  useEffect(() => {
    messageRefetchRef.current = loadMessageThreads
  }, [loadMessageThreads])
  useEffect(() => {
    selectedRefetchRef.current = refreshSelectedTask
  }, [refreshSelectedTask])

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
    setOrdering(readTaskOrdering(result.data.actor.subject, practice.id))
    setDiscovery(result.data)
    snapshotScopeRef.current = `${practice.id}:${location.id}`
    setPracticeID(practice.id)
    setLocationID(location.id)
    setLocationScopeID(scope)
    window.localStorage.setItem(practiceStorageKey, practice.id)
    window.localStorage.setItem(locationStorageKey, location.id)
    await loadSnapshot(practice.id, location.id)
  }, [loadSnapshot, session.data])

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
    const timeout = window.setTimeout(() => void loadTasks(), 0)
    return () => window.clearTimeout(timeout)
  }, [loadState, loadTasks, practiceID])

  useEffect(() => {
    if (
      railMode !== "messages" ||
      !practiceID ||
      !locationID ||
      loadState !== "ready"
    ) {
      return
    }
    const timeout = window.setTimeout(() => void loadMessageThreads(), 0)
    return () => window.clearTimeout(timeout)
  }, [loadMessageThreads, loadState, locationID, practiceID, railMode])

  useEffect(() => {
    if (!practiceID || !locationID || loadState !== "ready") return
    const controller = new AbortController()
    let stopped = false

    async function connect() {
      while (!stopped) {
        setConnection("connecting")
        const token = await getAccessToken()
        if (!token) {
          setLoadState("unauthorized")
          return
        }
        if (!(await snapshotRef.current(practiceID, locationID, false))) return
        try {
          const url = new URL("/v1/events", realtimeURL())
          url.searchParams.set("practiceId", practiceID)
          url.searchParams.set("locationId", locationID)
          const response = await fetch(url, {
            headers: {
              accept: "text/event-stream",
              authorization: `Bearer ${token}`,
            },
            signal: controller.signal,
          })
          if (response.status === 401 || response.status === 403) {
            setLoadState("unauthorized")
            return
          }
          if (!response.ok || !response.body) {
            throw new Error("realtime unavailable")
          }
          setConnection("connected")
          const reader = response.body
            .pipeThrough(new TextDecoderStream())
            .getReader()
          let buffer = ""
          while (!stopped) {
            const { value, done } = await reader.read()
            if (done) break
            buffer += value
            const events = buffer.split("\n\n")
            buffer = events.pop() ?? ""
            if (events.some((event) => event.includes("data:"))) {
              await snapshotRef.current(practiceID, locationID, false)
              await taskRefetchRef.current()
              await messageRefetchRef.current()
              await selectedRefetchRef.current()
              setCallingHint((current) => current + 1)
            }
          }
        } catch {
          if (controller.signal.aborted) return
        }
        setConnection("disconnected")
        await new Promise((resolve) =>
          window.setTimeout(resolve, 500 + Math.random() * 750),
        )
      }
    }
    void connect()
    return () => {
      stopped = true
      controller.abort()
    }
  }, [loadState, locationID, practiceID])

  useEffect(() => {
    if (!practiceID) return
    const interval = window.setInterval(() => {
      if (connectionRef.current === "connected") return
      void taskRefetchRef.current()
      void messageRefetchRef.current()
      void selectedRefetchRef.current()
    }, 1_500)
    return () => window.clearInterval(interval)
  }, [practiceID])

  function selectPractice(nextPracticeID: string) {
    if (!discovery) return
    callDetailGenerationRef.current += 1
    taskQueryGenerationRef.current += 1
    messageQueryGenerationRef.current += 1
    const practice = discovery.practices.find(
      (item) => item.id === nextPracticeID,
    )
    const location = practice?.locations[0]
    if (!practice || !location) return
    const storedScope = window.localStorage.getItem(
      `${taskScopeStorageKey}.${practice.id}`,
    )
    const scope =
      practice.locations.length === 1
        ? location.id
        : practice.locations.some((item) => item.id === storedScope)
          ? (storedScope ?? "")
          : ""
    setOrdering(readTaskOrdering(discovery.actor.subject, practice.id))
    tasksRef.current = []
    messageThreadsRef.current = []
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    setTasks([])
    setMessageThreads([])
    setSelectedTask(undefined)
    setSelectedThread(undefined)
    setHistoricalCall(undefined)
    setComposingNew(false)
    setView(activeCall ? "call" : "none")
    snapshotScopeRef.current = `${practice.id}:${location.id}`
    setPracticeID(practice.id)
    setLocationID(location.id)
    setLocationScopeID(scope)
    window.localStorage.setItem(practiceStorageKey, practice.id)
    window.localStorage.setItem(locationStorageKey, location.id)
    void loadSnapshot(practice.id, location.id)
  }

  function selectLocationScope(nextLocationID: string) {
    callDetailGenerationRef.current += 1
    taskQueryGenerationRef.current += 1
    messageQueryGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    tasksRef.current = []
    messageThreadsRef.current = []
    setTasks([])
    setMessageThreads([])
    setSelectedTask(undefined)
    setSelectedThread(undefined)
    setHistoricalCall(undefined)
    setComposingNew(false)
    if (viewRef.current !== "call") setView("none")
    setLocationScopeID(nextLocationID)
    window.localStorage.setItem(
      `${taskScopeStorageKey}.${practiceID}`,
      nextLocationID,
    )
    if (nextLocationID) {
      snapshotScopeRef.current = `${practiceID}:${nextLocationID}`
      setLocationID(nextLocationID)
      window.localStorage.setItem(locationStorageKey, nextLocationID)
      void loadSnapshot(practiceID, nextLocationID, false)
    }
  }

  function selectRailMode(mode: RailMode) {
    if (mode === railMode) return
    setRailMode(mode)
    setSearch("")
    setSettledSearch("")
    if (mode === "messages") {
      const messageLocationID = locationScopeID || locationID
      if (messageLocationID !== locationScopeID) {
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
      setSelectedTask(tasksRef.current[0])
    }
  }

  function selectTask(task: Task) {
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    setSelectedTask(task)
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
      setSelectedTask(task)
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
    if (!call || call.id === previousCallID) return
    callDetailGenerationRef.current += 1
    setHistoricalCall(undefined)
    if (call.id === focusedCallIDRef.current) return
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
        setRailMode("tasks")
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
      setSelectedTask(previous)
      setView("task")
    } else {
      setView(tasksRef.current[0] ? "task" : "none")
      setSelectedTask(tasksRef.current[0])
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
        onAction={() => void loadAuthority()}
      />
    )
  }

  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  return (
    <SidebarProvider>
      <TaskRail
        discovery={discovery}
        practice={practice}
        practiceID={practiceID}
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
        callingOffers={callingOffers}
        onPracticeChange={selectPractice}
        onLocationScopeChange={selectLocationScope}
        onModeChange={selectRailMode}
        onSearchChange={(value) => {
          taskQueryGenerationRef.current += 1
          setSearch(value)
        }}
        onOrderingChange={(value) => {
          taskQueryGenerationRef.current += 1
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
        <header className="flex h-11 shrink-0 items-center gap-3 border-b px-3">
          <SidebarTrigger />
          <span className="min-w-0 truncate text-xs text-muted-foreground">
            {workspace.practice.name} ·{" "}
            {railMode === "messages"
              ? practice.locations.find(
                  (location) => location.id === locationID,
                )?.name
              : locationScopeID
                ? practice.locations.find(
                    (location) => location.id === locationScopeID,
                  )?.name
                : "All offices"}
          </span>
          <span className="ml-auto font-mono text-[0.625rem] uppercase tracking-[0.14em] text-muted-foreground">
            {workspace.platformOperator ? "Platform operator" : "Practice user"}
          </span>
          {workspace.platformOperator && !workspace.supportMode && (
            <SupportDialog
              practiceID={practiceID}
              onEntered={() => void loadSnapshot(practiceID, locationID, false)}
            />
          )}
        </header>
        <CallingDock
          platformOperator={workspace.platformOperator}
          hint={callingHint}
          onOffersChanged={setCallingOffers}
          onCallChanged={handleCallChanged}
          onDisposition={(result) => void handleDisposition(result)}
        />
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
            revision={callingHint}
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
              setRailMode("tasks")
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
            historyHint={callingHint}
            onTaskUpdated={(task) => {
              updateTaskProjection(
                task,
                selectedTaskRef.current?.id === task.id,
              )
              void loadTasks()
            }}
            onReturnToCall={() => {
              if (activeCall) setView("call")
            }}
          />
        )}
      </SidebarInset>
    </SidebarProvider>
  )
}

function taskOrderingKey(userSubject: string, practiceID: string) {
  return `${taskOrderingStorageKey}.${userSubject}.${practiceID}`
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
      className="flex items-center gap-3 border-b border-primary/30 bg-primary/10 px-4 py-2 text-xs"
    >
      <ShieldCheckIcon className="size-4 text-primary" />
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
