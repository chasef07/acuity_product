"use client"

import {
  type ReactNode,
  useEffect,
  useState,
  useSyncExternalStore,
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
import { AIInteractionContext } from "@/components/workspace/ai-interaction-context"
import { EngagementWorkspace } from "@/components/workspace/engagement-workspace"
import { TaskCallContext } from "@/components/workspace/task-call-context"
import { WorkspaceRail } from "@/components/workspace/workspace-rail"
import { WorkspaceWindowFailure } from "@/components/workspace/workspace-window-failure"
import type {
  AccessDiscovery,
  CallingCall,
} from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { workspaceScopeForCall } from "@/lib/calling/workspace-scope"
import { cn } from "@/lib/utils"
import { canViewPracticeAnalytics } from "@/lib/booking-analytics"
import {
  createWorkspaceProjection,
  type WorkspaceProjectionIntent,
} from "@/lib/workspace-projection"
import {
  createWorkspaceAuthorityAdapter,
  createWorkspaceRealtimeAdapter,
} from "@/lib/workspace-projection-http"

const PracticeAnalytics = dynamic(
  () => import("@/components/workspace/practice-analytics").then(module => module.PracticeAnalytics),
  { loading: () => <Skeleton aria-label="Loading booking analytics" className="m-6 h-72" /> },
)

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
        className="flex min-h-0 flex-1 bg-background p-4 sm:p-6 lg:p-8"
      >
        <Skeleton className="h-40 w-full rounded-xl" />
      </div>
    ),
  },
)

export function PortalWorkspace() {
  const router = useRouter()
  const session = authClient.useSession()
  const sessionID = session.data?.session.id
  const [projection] = useState(() =>
    createWorkspaceProjection({
      authority: createWorkspaceAuthorityAdapter(),
      realtime: createWorkspaceRealtimeAdapter(),
      preferences: {
        read: (key) => {
          try {
            return window.localStorage.getItem(key)
          } catch {
            return null
          }
        },
        write: (key, value) => {
          try {
            window.localStorage.setItem(key, value)
          } catch {
            // Safe preferences never outrank current authorized state.
          }
        },
      },
      environment: {
        isHidden: () => document.hidden,
        clock: {
          setTimeout: (callback, milliseconds) =>
            window.setTimeout(callback, milliseconds),
          clearTimeout: (id) => window.clearTimeout(id),
        },
      },
    }),
  )
  const state = useSyncExternalStore(
    projection.subscribe,
    projection.getSnapshot,
    projection.getSnapshot,
  )
  const [taskCallRequest, setTaskCallRequest] = useState<{
    id: string
    taskID: string
  }>()
  const [taskCallError, setTaskCallError] = useState("")

  useEffect(() => {
    if (session.isPending) return
    if (!sessionID) {
      router.replace("/sign-in?next=%2Fworkspace")
      return
    }
    void projection.start()
  }, [projection, router, session.isPending, sessionID])

  useEffect(() => () => projection.stop(), [projection])

  useEffect(() => {
    const visibilityChanged = () => {
      void projection.dispatch({ type: "visibility-changed" })
    }
    document.addEventListener("visibilitychange", visibilityChanged)
    return () =>
      document.removeEventListener("visibilitychange", visibilityChanged)
  }, [projection])

  if (
    session.isPending ||
    (state.loadState === "loading" && !state.discovery)
  ) {
    return <WorkspaceLoading />
  }
  if (state.loadState === "unauthenticated") {
    return (
      <WorkspaceFailure
        title="Session expired"
        description="Sign in again to reconstruct your authorized workspace."
        action="Return to sign in"
        onAction={() => void router.push("/sign-in?next=%2Fworkspace")}
      />
    )
  }
  if (state.loadState === "unauthorized") {
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
  if (!state.discovery) {
    return (
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => void projection.dispatch({ type: "retry" })}
      />
    )
  }

  const discovery = state.discovery
  const practice =
    discovery.practices.find(
      (item) => item.id === state.scope.practiceID,
    ) ?? discovery.practices[0]
  if (!practice) {
    return (
      <WorkspaceFailure
        title="Workspace access unavailable"
        description="No authorized Practice is available for this identity."
        action="Return to sign in"
        onAction={() => void router.push("/sign-in")}
      />
    )
  }
  const callingEnabled = practice.callingEnabled
  const callingRuntimeEnabled = discovery.practices.some(
    (item) => item.callingEnabled,
  )
  const contextPanelLabel =
    state.selection.contextView === "task"
      ? "Task context"
      : state.selection.contextView === "call"
        ? "Call context"
        : "AI call context"
  const callingShell = (
    children: (
      activeCall: CallingCall | undefined,
      callingOccupied: boolean,
    ) => ReactNode,
  ) => (
    <SidebarProvider>
      <CallingDock
        key={discovery.actor.subject}
        actorSubject={discovery.actor.subject}
        callingEnabled={callingEnabled}
        callingRuntimeEnabled={callingRuntimeEnabled}
        practiceID={state.scope.practiceID}
        taskCallRequest={taskCallRequest}
        onTaskCallHandled={(requestID, requestError) => {
          setTaskCallRequest((current) =>
            current?.id === requestID ? undefined : current,
          )
          setTaskCallError(requestError ?? "")
        }}
        onCallScope={(call) => {
          const scope = workspaceScopeForCall(
            discovery,
            state.scope.practiceID,
            state.scope.locationScopeID,
            call,
          )
          if (scope) {
            void projection.dispatch({
              type: "select-scope",
              practiceID: scope.practiceID,
              locationScopeID: scope.locationID,
            })
          }
        }}
        onCallConnected={(call) =>
          void projection.dispatch({ type: "call-connected", call })
        }
        onDisposition={(result) =>
          void projection.dispatch({ type: "call-disposition", result })
        }
      >
        {children}
      </CallingDock>
    </SidebarProvider>
  )

  if (state.loadState === "loading" && !state.workspace) {
    return callingShell(() => <WorkspaceLoading />)
  }
  if (state.loadState === "unavailable" || !state.workspace) {
    return callingShell(() => (
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => void projection.dispatch({ type: "retry" })}
      />
    ))
  }

  const workspace = state.workspace
  const selectedTask = state.selection.task
  const selectedEngagement = state.selection.engagement
  const selectedAIInteractionID = state.selection.aiInteractionID
  const historicalCall = state.selection.historicalCall
  const contextView = state.selection.contextView
  const contextPanelOpen = state.selection.contextPanelOpen
  const view = state.selection.view

  return callingShell((activeCall, callingOccupied) => {
    const sendIntent = (intent: WorkspaceProjectionIntent) => {
      if (intent.type === "select-task" && activeCall) {
        void projection.dispatch({ ...intent, rememberForCall: true })
        return
      }
      void projection.dispatch(intent)
    }
    return (
      <>
        <WorkspaceRail
          projection={state}
          workspaceControl={
            <WorkspaceSelector
              discovery={discovery}
              practiceID={state.scope.practiceID}
              locationScopeID={state.scope.locationScopeID}
              disabled={callingOccupied}
              onSelect={(practiceID, locationScopeID) =>
                void projection.dispatch({
                  type: "select-scope",
                  practiceID,
                  locationScopeID,
                })
              }
            />
          }
          availabilityControl={<CallingAvailabilityControl />}
          onIntent={sendIntent}
        />
        <SidebarInset
          data-testid="mounted-workspace"
          data-workspace-version={workspace.version}
          className="h-svh min-h-0 min-w-0 overflow-hidden"
        >
          {view !== "engagement" && view !== "analytics" && (
            <header className="flex h-12 shrink-0 items-center gap-3 border-b px-3">
              <SidebarTrigger collapsedOnly />
              <div className="flex-1" />
            </header>
          )}
          {view === "analytics" && canViewPracticeAnalytics(discovery, state.scope.practiceID) ? (
            <PracticeAnalytics
              key={`${state.scope.practiceID}:${state.scope.locationScopeID}`}
              practiceID={state.scope.practiceID}
              locationScopeID={state.scope.locationScopeID}
              locations={discovery.practices.find(practice => practice.id === state.scope.practiceID)?.locations ?? []}
            />
          ) : view === "operator-analytics" && discovery.platformOperator ? (
            <OperatorAnalytics
              practiceID={state.scope.practiceID}
              locationScopeID={state.scope.locationScopeID}
            />
          ) : view === "engagement" && selectedEngagement ? (
            <div className="relative flex min-h-0 flex-1 bg-background">
              <div className="flex min-h-0 min-w-0 flex-1 bg-background">
                <EngagementWorkspace
                  key={selectedEngagement.phone}
                  engagement={selectedEngagement}
                  practiceID={state.scope.practiceID}
                  canMutate
                  revision={state.detailRevision}
                  selectedTaskID={
                    contextPanelOpen && contextView === "task"
                      ? selectedTask?.id
                      : undefined
                  }
                  selectedCallID={
                    contextPanelOpen && contextView === "call"
                      ? (historicalCall ?? activeCall)?.id
                      : undefined
                  }
                  selectedAIInteractionID={
                    contextPanelOpen && contextView === "ai-call"
                      ? selectedAIInteractionID
                      : undefined
                  }
                  headerLeading={<SidebarTrigger collapsedOnly />}
                  onTaskCreated={(task) =>
                    void projection.dispatch({ type: "task-created", task })
                  }
                  onTaskOpen={(task) =>
                    void projection.dispatch({ type: "open-task-context", task })
                  }
                  onCallOpen={(callID) =>
                    void projection.dispatch({ type: "open-call-context", callID })
                  }
                  onAIInteractionOpen={(interactionID) =>
                    void projection.dispatch({
                      type: "open-ai-context",
                      interactionID,
                    })
                  }
                />
              </div>
              <aside
                aria-label={contextPanelLabel}
                aria-hidden={!contextPanelOpen}
                data-state={contextPanelOpen ? "open" : "closed"}
                data-testid="context-panel"
                inert={!contextPanelOpen}
                className={cn(
                  "absolute top-3 right-3 flex h-fit max-h-[calc(100%-1.5rem)] w-[calc(100%-1.5rem)] max-w-[20rem] self-start flex-col overflow-hidden rounded-3xl border bg-popover shadow-lg transition-[width,margin,opacity,transform,border-color,box-shadow] duration-200 ease-out motion-reduce:transition-none lg:relative lg:inset-auto lg:my-3 lg:max-w-none lg:shrink-0",
                  contextPanelOpen
                    ? "translate-x-0 opacity-100 lg:mr-3 lg:w-72"
                    : "pointer-events-none translate-x-4 border-transparent opacity-0 shadow-none lg:mr-0 lg:w-0",
                )}
                onTransitionEnd={(event) => {
                  if (event.currentTarget === event.target) {
                    void projection.dispatch({ type: "context-transition-ended" })
                  }
                }}
              >
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="absolute top-3 right-3 z-10"
                  aria-label="Close context panel"
                  onClick={() => void projection.dispatch({ type: "close-context" })}
                >
                  <PanelRightCloseIcon />
                </Button>
                <div className="flex min-h-0 flex-1">
                  {contextView === "ai-call" ? (
                    <AIInteractionContext
                      interactionID={selectedAIInteractionID}
                      detail={state.selection.aiInteraction}
                      loading={state.selection.aiInteractionLoading}
                      error={state.selection.aiInteractionError}
                      onReview={projection.reviewAIOutcome}
                    />
                  ) : contextView === "task" && state.selection.taskError ? (
                    <div className="flex-1 p-4">
                      <WorkspaceWindowFailure
                        message={state.selection.taskError}
                        onRetry={() =>
                          void projection.dispatch({ type: "retry" })
                        }
                      />
                    </div>
                  ) : (
                    <TaskCallContext
                      task={selectedTask}
                      activeCall={historicalCall ?? activeCall}
                      view={contextView}
                      canMutate
                      canCall={callingEnabled && !callingOccupied}
                      historyHint={state.detailRevision}
                      taskCallPending={Boolean(taskCallRequest)}
                      taskCallError={taskCallError}
                      onTaskUpdated={(task) =>
                        void projection.dispatch({ type: "task-committed", task })
                      }
                      onStartTaskCall={(task) => {
                        setTaskCallError("")
                        void projection.dispatch({
                          type: "remember-return-task",
                          taskID: task.id,
                        })
                        setTaskCallRequest({
                          id: window.crypto.randomUUID(),
                          taskID: task.id,
                        })
                      }}
                      onReturnToCall={() => {
                        if (activeCall) {
                          void projection.dispatch({ type: "return-to-call" })
                        }
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
      </>
    )
  })
}

function WorkspaceSelector({
  discovery,
  practiceID,
  locationScopeID,
  disabled,
  onSelect,
}: {
  discovery: AccessDiscovery
  practiceID: string
  locationScopeID: string
  disabled: boolean
  onSelect: (practiceID: string, locationID: string) => void
}) {
  const [open, setOpen] = useState(false)
  const practice =
    discovery.practices.find((item) => item.id === practiceID) ??
    discovery.practices[0]
  if (!practice) return null
  const locationLabel = locationScopeID
    ? (practice.locations.find((item) => item.id === locationScopeID)?.name ??
      "Office")
    : "All offices"

  function select(nextPracticeID: string, nextLocationID: string) {
    onSelect(nextPracticeID, nextLocationID)
    setOpen(false)
  }

  return (
    <Popover
      open={!disabled && open}
      onOpenChange={(nextOpen) => {
        if (!disabled) setOpen(nextOpen)
      }}
    >
      <PopoverTrigger
        render={
          <Button
            aria-label="Workspace selector"
            variant="ghost"
            size="sm"
            disabled={disabled}
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
                  item.id === practiceID && location.id === locationScopeID
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
    <main className="flex min-h-svh items-center justify-center bg-background p-6">
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
