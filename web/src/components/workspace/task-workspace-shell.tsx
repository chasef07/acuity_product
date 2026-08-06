"use client"

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import {
  CheckIcon,
  ChevronsUpDownIcon,
  CopyIcon,
  PhoneCallIcon,
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
import {
  EngagementWorkspace,
  MessageWorkspace,
} from "@/components/workspace/message-workspace"
import {
  type ConnectionState,
  TaskRail,
} from "@/components/workspace/task-rail"
import { portalClient, realtimeURL } from "@/lib/api/client"
import {
  discoverAccess,
  enterSupportMode,
  getWorkspace,
  listLiveCalls,
  queryEngagements,
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
  LiveCall,
  Message,
  MessageThreadSummary,
  Task,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"
import { isCompletePhoneSearch } from "@/lib/phone"
import {
  createWorkspaceSync,
  type WorkspaceSync,
  WorkspaceSyncUnauthorizedError,
} from "@/lib/workspace-sync/workspace-sync"

type LoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type View = "none" | "message" | "engagement"

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"
const taskScopeStorageKey = "acuity.taskLocationScope"
const taskOrderingStorageKey = "acuity.taskOrdering"
const lastNumberStorageKey = "acuity.lastNumberInbox"
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
  const [settledSearch, setSettledSearch] = useState("")
  const [ordering, setOrdering] = useState<TaskOrdering>("priority")
  const [taskState] = useState<"OPEN" | "COMPLETED">("OPEN")
  const [engagements, setEngagements] = useState<EngagementSummary[]>([])
  const [engagementLoading, setEngagementLoading] = useState(false)
  const [selectedEngagement, setSelectedEngagement] = useState<EngagementSummary>()
  const [recentInboxes, setRecentInboxes] = useState<EngagementSummary[]>([])
  const [recentLoaded, setRecentLoaded] = useState(false)
  const [attentionHighlightTaskID, setAttentionHighlightTaskID] = useState("")
  const [liveCalls, setLiveCalls] = useState<LiveCall[]>([])
  const [tasks, setTasks] = useState<Task[]>([])
  const [tasksLoading, setTasksLoading] = useState(false)
  const [messageThreads, setMessageThreads] = useState<MessageThreadSummary[]>(
    [],
  )
  const [messagesLoading, setMessagesLoading] = useState(false)
  const [selectedTask, setSelectedTask] = useState<Task>()
  const [selectedThread, setSelectedThread] = useState<MessageThreadSummary>()
  const [committedMessage, setCommittedMessage] = useState<Message>()
  const [composingNew, setComposingNew] = useState(false)
  const [view, setView] = useState<View>("none")
  const [activeCall, setActiveCall] = useState<CallingCall>()
  const [callRefreshRevision, setCallRefreshRevision] = useState(0)
  const [workspaceRevision, setWorkspaceRevision] = useState(0)
  const [taskCallRequest, setTaskCallRequest] = useState<{
    id: string
    taskID: string
  }>()
  const [taskCallError, setTaskCallError] = useState("")
  const selectedTaskRef = useRef<Task | undefined>(undefined)
  const selectedThreadRef = useRef<MessageThreadSummary | undefined>(undefined)
  const selectedPhoneRef = useRef("")
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
  const settledSearchRef = useRef("")
  const orderingRef = useRef<TaskOrdering>("priority")
  const taskStateRef = useRef<"OPEN" | "COMPLETED">("OPEN")
  const locationScopeRef = useRef("")
  const workspaceSyncRef = useRef<WorkspaceSync | undefined>(undefined)
  const returnTaskIDRef = useRef("")
  const focusedCallIDRef = useRef("")
  const engagementGenerationRef = useRef(0)
  const recentGenerationRef = useRef(0)
  const restoredPracticeRef = useRef("")

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
    taskStateRef.current = taskState
  }, [taskState])
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

  useEffect(() => {
    if (!discovery || !practiceID || loadState !== "ready") return
    const generation = ++recentGenerationRef.current
    const phones = readRecentNumbers(discovery.actor.subject, practiceID)
    const timeout = window.setTimeout(async () => {
      setRecentLoaded(false)
      const token = await getAccessToken()
      if (!token) return
      const client = portalClient(token)
      const results =
        phones.length > 0
          ? await Promise.all(
              phones.map((phone) =>
                queryEngagements({
                  client,
                  body: { practiceId: practiceID, phone },
                }).catch(() => undefined),
              ),
            )
          : [
              await queryEngagements({
                client,
                body: { practiceId: practiceID, limit: 7 },
              }).catch(() => undefined),
            ]
      if (generation !== recentGenerationRef.current) return
      const authorized = results.flatMap((result) => result?.data?.items ?? [])
      setRecentInboxes(authorized)
      setRecentLoaded(true)
      writeRecentNumbers(
        discovery.actor.subject,
        practiceID,
        authorized.map((engagement) => engagement.phone),
      )
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [discovery, loadState, practiceID])

  useEffect(() => {
    if (
      !discovery ||
      !practiceID ||
      !recentLoaded ||
      !hasLoadedTasksRef.current ||
      !hasLoadedThreadsRef.current ||
      restoredPracticeRef.current === practiceID
    ) {
      return
    }
    const remembered = readLastNumber(discovery.actor.subject, practiceID)
    const lastOpen = recentInboxes.find(
      (engagement) => engagement.phone === remembered,
    )
    const startup = lastOpen ??
      (!selectedPhoneRef.current && tasks.length === 0 && messageThreads.length === 0
        ? recentInboxes[0]
        : undefined)
    restoredPracticeRef.current = practiceID
    if (!startup) return
    updateSelectedTask(undefined)
    selectedPhoneRef.current = startup.phone
    setSelectedEngagement(startup)
    setView("engagement")
  }, [discovery, messageThreads.length, practiceID, recentInboxes, recentLoaded, tasks.length])

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
    async (cursor = "", append = false, preserveSelection = false) => {
      if (!practiceID) return
      const queryKey = workspaceTaskQueryKey(
        practiceID,
        locationScopeID,
        taskState,
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
          state: taskState,
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

      const selected = selectedTaskRef.current
      if (selected) {
        const current = next.find((task) => task.id === selected.id)
        if (current) updateSelectedTask(current)
        else if (!preserveSelection) {
          updateSelectedTask(undefined)
          if (next[0] && !selectedPhoneRef.current) {
            selectedPhoneRef.current = next[0].phone
            setSelectedEngagement(taskEngagement(next[0]))
            setView("engagement")
          }
        }
      } else if (firstLoad && next[0]) {
        const initial =
          next.find((task) => task.phone === readLastNumber(discovery?.actor.subject, practiceID)) ??
          next[0]
        selectedPhoneRef.current = initial.phone
        setSelectedEngagement(taskEngagement(initial))
        setView("engagement")
      } else if (
        !next[0] &&
        !selectedPhoneRef.current
      ) {
        setView("none")
      }
    },
    [discovery?.actor.subject, locationScopeID, ordering, practiceID, taskState],
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
      const selectedPhone = selectedPhoneRef.current
      if (selectedPhone) {
        const matching = next.filter(
          (thread) => thread.externalPhone === selectedPhone,
        )
        setSelectedEngagement((current) =>
          current?.phone === selectedPhone
            ? {
                ...current,
                unread: matching.some((thread) => thread.unread),
                textNeedsAttention: matching.some(
                  (thread) => thread.needsAttention,
                ),
              }
            : current,
        )
        setRecentInboxes((current) =>
          current.map((engagement) =>
            engagement.phone === selectedPhone
              ? {
                  ...engagement,
                  unread: matching.some((thread) => thread.unread),
                  textNeedsAttention: matching.some(
                    (thread) => thread.needsAttention,
                  ),
                }
              : engagement,
          ),
        )
      }

      const selected = selectedThreadRef.current
      if (selected) {
        const current = next.find((item) => item.id === selected.id)
        if (current) setSelectedThread(current)
      } else if (
        firstLoad &&
        !composingNewRef.current &&
        next[0] &&
        viewRef.current !== "engagement"
      ) {
        const remembered = readLastNumber(discovery?.actor.subject, practiceID)
        const initial = next.find((thread) => thread.externalPhone === remembered)
        if (initial || !selectedPhoneRef.current) {
          const selected = initial ?? next[0]
          setSelectedThread(selected)
          selectedPhoneRef.current = selected.externalPhone
          setSelectedEngagement(threadEngagement(selected, tasksRef.current))
          setView("engagement")
        }
      } else if (
        !next[0] &&
        !composingNewRef.current &&
        viewRef.current === "message"
      ) {
        setView("none")
      }
    },
    [discovery?.actor.subject, locationScopeID, practiceID],
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
      const currentTaskState = taskStateRef.current
      const selectedTaskID = selectedTaskRef.current?.id
      const taskQueryKey = workspaceTaskQueryKey(
        scope.practiceID,
        taskLocationID,
        currentTaskState,
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
              state: currentTaskState,
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
              }
            } else if (
              firstLoad &&
              tasksWithSelection[0] &&
              viewRef.current !== "engagement"
            ) {
              const remembered = readLastNumber(
                snapshot.actor.subject,
                scope.practiceID,
              )
              const initial =
                tasksWithSelection.find((task) => task.phone === remembered) ??
                tasksWithSelection[0]
              selectedPhoneRef.current = initial.phone
              setSelectedEngagement(taskEngagement(initial))
              setView("engagement")
            }
          }

          if (messageGeneration === messageQueryGenerationRef.current) {
            const firstLoad = !hasLoadedThreadsRef.current
            hasLoadedThreadsRef.current = true
            messageQueryKeyRef.current = messageQueryKey
            messageThreadsRef.current = nextMessages
            setMessageThreads(nextMessages)
            const selectedPhone = selectedPhoneRef.current
            if (selectedPhone) {
              const matching = nextMessages.filter(
                (thread) => thread.externalPhone === selectedPhone,
              )
              setSelectedEngagement((current) =>
                current?.phone === selectedPhone
                  ? {
                      ...current,
                      unread: matching.some((thread) => thread.unread),
                      textNeedsAttention: matching.some(
                        (thread) => thread.needsAttention,
                      ),
                    }
                  : current,
              )
            }
            const selected = selectedThreadRef.current
            if (selected) {
              const current = nextMessages.find(
                (thread) => thread.id === selected.id,
              )
              if (current) setSelectedThread(current)
            } else if (
              firstLoad &&
              !composingNewRef.current &&
              nextMessages[0] &&
              viewRef.current !== "engagement"
            ) {
              const remembered = readLastNumber(
                snapshot.actor.subject,
                scope.practiceID,
              )
              const initial =
                nextMessages.find(
                  (thread) => thread.externalPhone === remembered,
                ) ?? nextMessages[0]
              if (!selectedPhoneRef.current || initial.externalPhone === remembered) {
                setSelectedThread(initial)
                selectedPhoneRef.current = initial.externalPhone
                setSelectedEngagement(
                  threadEngagement(initial, tasksRef.current),
                )
                setView("engagement")
              }
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
      taskState,
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
    taskState,
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
    const requestGeneration = ++engagementGenerationRef.current
    if (!practiceID || !isCompletePhoneSearch(settledSearch)) return
    const timeout = window.setTimeout(async () => {
      setEngagementLoading(true)
      const token = await getAccessToken()
      if (!token) return
      const result = await queryEngagements({
        client: portalClient(token),
        body: { practiceId: practiceID, phone: settledSearch },
      }).catch(() => undefined)
      if (requestGeneration !== engagementGenerationRef.current) return
      setEngagementLoading(false)
      setEngagements(result?.data?.items ?? [])
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [practiceID, settledSearch, workspaceRevision])

  useEffect(() => {
    if (!practiceID || !locationID || loadState !== "ready") return
    const liveLocationID = locationScopeID
    const timeout = window.setTimeout(async () => {
      const token = await getAccessToken()
      if (!token) return
      const result = await listLiveCalls({
        client: portalClient(token),
        query: {
          practiceId: practiceID,
          ...(liveLocationID ? { locationId: liveLocationID } : {}),
        },
      }).catch(() => undefined)
      setLiveCalls(result?.data?.items ?? [])
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [
    callRefreshRevision,
    loadState,
    locationID,
    locationScopeID,
    practiceID,
    workspaceRevision,
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
    taskQueryGenerationRef.current += 1
    messageQueryGenerationRef.current += 1
    hasLoadedTasksRef.current = false
    hasLoadedThreadsRef.current = false
    taskQueryKeyRef.current = ""
    messageQueryKeyRef.current = ""
    tasksRef.current = []
    messageThreadsRef.current = []
    engagementGenerationRef.current += 1
    setTasks([])
    setMessageThreads([])
    setSearch("")
    setSettledSearch("")
    setEngagements([])
    setEngagementLoading(false)
    updateSelectedTask(undefined)
    setSelectedThread(undefined)
    setComposingNew(false)
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
    setComposingNew(false)
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
    setAttentionHighlightTaskID("")
    updateSelectedTask(task)
    if (activeCall) returnTaskIDRef.current = task.id
    const engagement = {
      ...taskEngagement(task),
      textNeedsAttention: messageThreadsRef.current.some(
        (thread) => thread.externalPhone === task.phone && thread.needsAttention,
      ),
    }
    rememberInbox(engagement)
    selectedPhoneRef.current = task.phone
    setSelectedEngagement(engagement)
    setView("engagement")
  }

  function selectEngagement(engagement: EngagementSummary) {
    updateSelectedTask(undefined)
    rememberInbox(engagement)
    selectedPhoneRef.current = engagement.phone
    setSelectedEngagement(engagement)
    setView("engagement")
  }

  async function submitPhoneSearch() {
    const phone = search.trim()
    if (!isCompletePhoneSearch(phone) || !practiceID) return
    const requestGeneration = ++engagementGenerationRef.current
    setEngagementLoading(true)
    const token = await getAccessToken()
    if (!token) {
      setEngagementLoading(false)
      return
    }
    const result = await queryEngagements({
      client: portalClient(token),
      body: { practiceId: practiceID, phone },
    }).catch(() => undefined)
    if (requestGeneration !== engagementGenerationRef.current) return
    setEngagementLoading(false)
    const items = result?.data?.items ?? []
    setEngagements(items)
    if (items[0]) selectEngagement(items[0])
  }

  function composeNewMessage() {
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
    if (select) selectTask(task)
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
      needsAttention: messageThreadsRef.current.some(
        (thread) =>
          thread.externalPhone === message.thread.externalPhone &&
          thread.needsAttention,
      ),
    }
    setSelectedThread(summary)
    setComposingNew(false)
    rememberInbox(threadEngagement(summary, tasksRef.current))
    selectedPhoneRef.current = summary.externalPhone
    setSelectedEngagement(threadEngagement(summary, tasksRef.current))
    setView("engagement")
    void loadMessageThreads()
  }

  function rememberInbox(engagement: EngagementSummary) {
    const userSubject = discovery?.actor.subject
    rememberNumber(userSubject, practiceID, engagement.phone)
    if (!userSubject || !practiceID) return
    setRecentInboxes((current) => {
      const next = [
        engagement,
        ...current.filter((item) => item.phone !== engagement.phone),
      ].slice(0, 7)
      writeRecentNumbers(
        userSubject,
        practiceID,
        next.map((item) => item.phone),
      )
      return next
    })
  }

  function handleThreadRead(threadID: string) {
    const nextThreads = messageThreadsRef.current.map((thread) =>
      thread.id === threadID ? { ...thread, unread: false } : thread,
    )
    messageThreadsRef.current = nextThreads
    setMessageThreads(nextThreads)
    const nextTasks = tasksRef.current.map((task) =>
      task.conversationThreadId === threadID || task.messageThreadId === threadID
        ? { ...task, unread: false }
        : task,
    )
    tasksRef.current = nextTasks
    setTasks(nextTasks)
  }

  function handleEngagementRead(phone: string) {
    const clearedThreadIDs = new Set(
      messageThreadsRef.current
        .filter((thread) => thread.externalPhone === phone)
        .map((thread) => thread.id),
    )
    const nextThreads = messageThreadsRef.current.map((thread) =>
      thread.externalPhone === phone ? { ...thread, unread: false } : thread,
    )
    messageThreadsRef.current = nextThreads
    setMessageThreads(nextThreads)
    setSelectedEngagement((current) =>
      current?.phone === phone ? { ...current, unread: false } : current,
    )
    setRecentInboxes((current) =>
      current.map((item) =>
        item.phone === phone ? { ...item, unread: false } : item,
      ),
    )
    const nextTasks = tasksRef.current.map((task) =>
      clearedThreadIDs.has(task.conversationThreadId ?? "") ||
      clearedThreadIDs.has(task.messageThreadId ?? "")
        ? { ...task, unread: false }
        : task,
    )
    tasksRef.current = nextTasks
    setTasks(nextTasks)
  }

  const handleCallChanged = useCallback((call: CallingCall | undefined) => {
    setActiveCall(call)
    if (!call) return
    const opensInbox =
      call.direction === "OUTBOUND" || call.state === "CONNECTED"
    if (!opensInbox || call.id === focusedCallIDRef.current) {
      return
    }
    focusedCallIDRef.current = call.id
    returnTaskIDRef.current = selectedTaskRef.current?.id ?? ""
    updateSelectedTask(undefined)
    setSelectedThread(undefined)
    setAttentionHighlightTaskID("")
    selectedPhoneRef.current = call.phone
    const engagement: EngagementSummary = {
      phone: call.phone,
      ...(call.displayName ? { displayName: call.displayName } : {}),
      locations: [{ id: call.locationId, name: call.locationName }],
      latestActivity: call.connectedAt ?? new Date().toISOString(),
      openTaskCount: call.recoveryTask?.state === "OPEN" ? 1 : 0,
      unread: false,
      textNeedsAttention: false,
    }
    rememberNumber(discovery?.actor.subject, practiceID, call.phone)
    setRecentInboxes((current) => {
      const next = [
        engagement,
        ...current.filter((item) => item.phone !== engagement.phone),
      ].slice(0, 7)
      if (discovery?.actor.subject && practiceID) {
        writeRecentNumbers(
          discovery.actor.subject,
          practiceID,
          next.map((item) => item.phone),
        )
      }
      return next
    })
    setSelectedEngagement(engagement)
    setView("engagement")
  }, [discovery?.actor.subject, practiceID])

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
        updateTaskProjection(task.data, false)
        selectTask(task.data)
        return
      }
    }
    const previous = tasksRef.current.find(
      (task) => task.id === returnTaskIDRef.current,
    )
    if (previous) {
      selectTask(previous)
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
          key={practice.id}
          discovery={discovery}
          practice={practice}
          tasks={tasks}
          messages={messageThreads}
          engagements={engagements}
          recent={recentInboxes}
          selectedTaskID={selectedTask?.id ?? attentionHighlightTaskID}
          search={search}
          engagementLoading={engagementLoading}
          loading={tasksLoading}
          messageLoading={messagesLoading}
          connection={connection}
          onSearchChange={(value) => {
            setSearch(value)
            engagementGenerationRef.current += 1
            setEngagements([])
            setEngagementLoading(isCompletePhoneSearch(value))
          }}
          onSearchSubmit={submitPhoneSearch}
          onEngagementSelect={selectEngagement}
          onTaskSelect={selectTask}
          onNewText={composeNewMessage}
        />
        <SidebarInset
          data-testid="mounted-workspace"
          data-workspace-version={workspace.version}
          className="h-svh min-w-0 overflow-hidden"
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
              locationScopeID={locationScopeID}
              onSelect={selectWorkspaceScope}
            />
            <CallingAvailabilityControl />
            <LiveCallsIndicator
              calls={liveCalls}
              groupByLocation={!locationScopeID}
            />
            {workspace.platformOperator && !workspace.supportMode && (
              <SupportDialog
                practiceID={practiceID}
                onEntered={() =>
                  void loadSnapshot(practiceID, locationID, false)
                }
              />
            )}
          </header>
          {view === "engagement" && selectedEngagement ? (
            <EngagementWorkspace
              key={`${selectedEngagement.phone}:${selectedTask?.id ?? ""}:${selectedTask?.version ?? ""}`}
              engagement={selectedEngagement}
              practiceID={practiceID}
              supportSessionID={workspace.supportMode?.id ?? ""}
              canMutate={
                !workspace.platformOperator || Boolean(workspace.supportMode)
              }
              revision={workspaceRevision}
              initialMessage={committedMessage}
              focusedTask={selectedTask}
              onMessageSent={handleMessageSent}
              onThreadRead={handleThreadRead}
              onEngagementRead={handleEngagementRead}
              onTaskUpdated={(task) => {
                updateTaskProjection(task, false)
                if (task.state === "COMPLETED") {
                  const nextTask = tasksRef.current.find(
                    (candidate) =>
                      candidate.id !== task.id && candidate.state === "OPEN",
                  )
                  updateSelectedTask(undefined)
                  setAttentionHighlightTaskID(nextTask?.id ?? "")
                }
                void loadTasks("", false, true)
              }}
              onTextHandled={() => {
                void loadMessageThreads()
              }}
              taskCallPending={Boolean(taskCallRequest)}
              taskCallError={taskCallError}
              onStartTaskCall={(task) => {
                setTaskCallError("")
                setTaskCallRequest({
                  id: window.crypto.randomUUID(),
                  taskID: task.id,
                })
              }}
              onTaskCreated={(task) => updateTaskProjection(task, false)}
              onTaskOpen={selectTask}
            />
          ) : composingNew ? (
            <MessageWorkspace
              key={
                selectedThread?.id ??
                (composingNew ? `new:${locationID}` : `empty:${locationID}`)
              }
              thread={selectedThread}
              composingNew={composingNew}
              practiceID={practiceID}
              locationID={locationID}
              locationName={
                practice.locations.find((location) => location.id === locationID)
                  ?.name ?? "Office"
              }
              locations={practice.locations}
              supportSessionID={workspace.supportMode?.id ?? ""}
              canMutate={
                !workspace.platformOperator || Boolean(workspace.supportMode)
              }
              revision={workspaceRevision}
              initialMessage={committedMessage}
              onMessageSent={handleMessageSent}
              onThreadRead={handleThreadRead}
              onTaskCreated={(task) => {
                updateTaskProjection(task, false)
                void loadTasks()
              }}
              onTaskOpen={(task) => {
                updateTaskProjection(task, false)
                selectTask(task)
              }}
            />
          ) : (
            <NumberInboxEmpty />
          )}
        </SidebarInset>
      </CallingDock>
    </SidebarProvider>
  )
}

function NumberInboxEmpty() {
  return (
    <section className="flex min-h-0 flex-1 items-center justify-center p-8">
      <div className="max-w-sm text-center">
        <PhoneCallIcon className="mx-auto size-8 text-muted-foreground" />
        <h1 className="mt-4 text-lg font-semibold">No recorded activity yet</h1>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          Select an attention item, search an authorized phone number, or start a
          new text to open a number inbox.
        </p>
      </div>
    </section>
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
  state: "OPEN" | "COMPLETED",
  ordering: TaskOrdering,
) {
  return `${practiceID}:${locationID}:${state}:${ordering}`
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
    textNeedsAttention: false,
  }
}

function threadEngagement(
  thread: MessageThreadSummary,
  tasks: Task[],
): EngagementSummary {
  const matchingTasks = tasks.filter((task) => task.phone === thread.externalPhone)
  return {
    phone: thread.externalPhone,
    ...(thread.displayName ? { displayName: thread.displayName } : {}),
    locations: [{ id: thread.locationId, name: thread.locationName }],
    latestActivity: thread.latestActivity,
    openTaskCount: matchingTasks.filter((task) => task.state === "OPEN").length,
    unread: thread.unread,
    textNeedsAttention: thread.needsAttention,
  }
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

function lastNumberKey(userSubject: string, practiceID: string) {
  return `${lastNumberStorageKey}.${userSubject}.${practiceID}`
}

function recentNumbersKey(userSubject: string, practiceID: string) {
  return `${recentNumbersStorageKey}.${userSubject}.${practiceID}`
}

function rememberNumber(
  userSubject: string | undefined,
  practiceID: string,
  phone: string,
) {
  if (!userSubject || !practiceID || !phone) return
  window.localStorage.setItem(lastNumberKey(userSubject, practiceID), phone)
}

function readLastNumber(
  userSubject: string | undefined,
  practiceID: string,
) {
  if (!userSubject || !practiceID) return ""
  return window.localStorage.getItem(lastNumberKey(userSubject, practiceID)) ?? ""
}

function readRecentNumbers(userSubject: string, practiceID: string): string[] {
  try {
    const stored = window.localStorage.getItem(
      recentNumbersKey(userSubject, practiceID),
    )
    return stored ? (JSON.parse(stored) as string[]).slice(0, 7) : []
  } catch {
    return []
  }
}

function writeRecentNumbers(
  userSubject: string,
  practiceID: string,
  phones: string[],
) {
  window.localStorage.setItem(
    recentNumbersKey(userSubject, practiceID),
    JSON.stringify(phones.slice(0, 7)),
  )
}

function LiveCallsIndicator({
  calls,
  groupByLocation,
}: {
  calls: LiveCall[]
  groupByLocation: boolean
}) {
  const [copiedCallID, setCopiedCallID] = useState("")
  if (calls.length === 0) return null
  const groups = groupByLocation
    ? Object.values(
        calls.reduce<Record<string, { name: string; calls: LiveCall[] }>>(
          (current, call) => {
            current[call.locationId] ??= {
              name: call.locationName,
              calls: [],
            }
            current[call.locationId]!.calls.push(call)
            return current
          },
          {},
        ),
      )
    : [{ name: "", calls }]
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button size="sm" variant="outline" aria-label="Live calls" />
        }
      >
        <PhoneCallIcon />
        {calls.length} live
      </PopoverTrigger>
      <PopoverContent align="end" className="w-96">
        <PopoverHeader>
          <PopoverTitle>Live calls</PopoverTitle>
          <PopoverDescription>
            Informational only. No assignment or claim is implied.
          </PopoverDescription>
        </PopoverHeader>
        <div className="mt-2 flex max-h-80 flex-col overflow-y-auto">
          {groups.map((group) => (
            <section key={group.name || "current"} className="border-b last:border-b-0">
              {group.name && (
                <h3 className="sticky top-0 bg-popover py-2 text-xs font-semibold text-muted-foreground">
                  {group.name}
                </h3>
              )}
              <div className="divide-y">
                {group.calls.map((call) => (
                  <div key={call.id} className="flex items-start gap-3 py-3 text-sm">
                    <div className="min-w-0 flex-1">
                      <p className="font-medium tabular-nums">
                        {formatPhone(call.phone)} · {call.direction === "INBOUND" ? "Inbound" : "Outbound"}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">
                        {call.staffEmail || call.staffSubject}
                        {!groupByLocation && ` · ${call.locationName}`}
                      </p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        Connected {new Date(call.connectedAt).toLocaleTimeString([], {
                          hour: "numeric",
                          minute: "2-digit",
                        })}
                      </p>
                    </div>
                    <Button
                      size="icon-sm"
                      variant="ghost"
                      aria-label={`Copy ${call.phone}`}
                      title={copiedCallID === call.id ? "Copied" : "Copy phone"}
                      onClick={() => {
                        void navigator.clipboard.writeText(call.phone)
                        setCopiedCallID(call.id)
                        window.setTimeout(() => setCopiedCallID(""), 1500)
                      }}
                    >
                      {copiedCallID === call.id ? <CheckIcon /> : <CopyIcon />}
                    </Button>
                  </div>
                ))}
              </div>
            </section>
          ))}
        </div>
      </PopoverContent>
    </Popover>
  )
}

function formatPhone(phone: string) {
  const digits = phone.replace(/\D/g, "")
  if (digits.length === 11 && digits.startsWith("1")) {
    return `+1 (${digits.slice(1, 4)}) ${digits.slice(4, 7)}-${digits.slice(7)}`
  }
  return phone
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
