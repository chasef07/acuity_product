"use client"

import { type ReactNode, type Ref, useEffect, useMemo, useRef, useState } from "react"
import { CheckIcon, EllipsisIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type {
  PhoneSummary,
  StaffTaskCloseReason,
  Task,
} from "@/lib/api/generated/types.gen"
import { useEngagementSummary } from "@/lib/clients/engagement"
import { signInRequired } from "@/lib/clients/portal-request"
import { reopenTask } from "@/lib/clients/tasks"
import type { PortalQuery } from "@/lib/data/queries"
import { cn } from "@/lib/utils"
import {
  formatRelativeTime,
  taskCloseSummary,
  taskGroupLabel,
  taskOriginLabels,
} from "@/lib/vocabulary"

import { offersTaskCloseMenu, TaskCloseMenu } from "./task-close-menu"
import { taskRelativeTime } from "./workspace-rail-task-row"

const collapsedRowLimit = 8

type TaskFailure = { taskID: string; message: string }

type PatientTasksPanelProps = {
  currentTaskID?: string
  callTaskID?: string
  completion: { pendingTaskID: string; errorTaskID: string; error: string }
  className?: string
  onComplete: (task: Task, reason: StaffTaskCloseReason) => void
  onOpen?: (task: Task) => void
}

export function PatientTasksPanel({
  practiceID,
  phone,
  onTaskUpdated,
  ...props
}: PatientTasksPanelProps & {
  practiceID: string
  phone: string
  onTaskUpdated: (task: Task) => void
}) {
  const summary = useEngagementSummary({ practiceID, phone })
  const [reopening, setReopening] = useState("")
  const [reopenFailure, setReopenFailure] = useState<TaskFailure>()

  async function reopen(task: Task) {
    if (reopening) return
    setReopening(task.id)
    setReopenFailure(undefined)
    const outcome = await reopenTask(task)
    setReopening("")
    if (outcome.ok) {
      onTaskUpdated(outcome.data)
      return
    }
    if (outcome.failure.kind === "conflict") void summary.retry()
    setReopenFailure({
      taskID: task.id,
      message: signInRequired(outcome.failure)
        ? "Sign in again to reopen this Task."
        : outcome.failure.kind === "conflict"
          ? "This Task changed elsewhere. The latest state is now loaded."
          : "The Task could not be reopened.",
    })
  }

  return (
    <PatientTasksPanelView
      {...props}
      summary={summary}
      reopening={reopening}
      reopenFailure={reopenFailure}
      onReopen={(task) => void reopen(task)}
      onRetry={() => void summary.retry()}
    />
  )
}

export function PatientTasksPanelView({
  summary,
  currentTaskID,
  callTaskID,
  completion,
  reopening,
  reopenFailure,
  className,
  onComplete,
  onOpen,
  onReopen,
  onRetry,
}: PatientTasksPanelProps & {
  summary: PortalQuery<Pick<PhoneSummary, "names" | "openTasks" | "openTaskCount" | "closedToday">>
  reopening: string
  reopenFailure?: TaskFailure
  onReopen: (task: Task) => void
  onRetry: () => void
}) {
  const section = useRef<HTMLElement>(null)
  const closing = useRef<{ taskID: string; index: number }>(undefined)
  const [showAll, setShowAll] = useState(false)
  const loaded = summary.status === "ready" ? summary.data : undefined
  const open = useMemo(
    () =>
      loaded && [
        ...loaded.openTasks.filter((task) => task.id === callTaskID),
        ...loaded.openTasks.filter((task) => task.id !== callTaskID),
      ],
    [loaded, callTaskID],
  )

  useEffect(() => {
    const closed = closing.current
    if (!closed || !open) return
    if (open.some((task) => task.id === closed.taskID)) {
      if (!completion.pendingTaskID) closing.current = undefined
      return
    }
    closing.current = undefined
    if (document.activeElement && document.activeElement !== document.body) return
    const next = open[closed.index] ?? open.at(-1)
    const row = next && section.current?.querySelector<HTMLElement>(`[data-patient-task-id="${next.id}"]`)
    ;(row ?? section.current)?.focus()
  }, [open, completion.pendingTaskID])

  if (!loaded || !open) {
    return (
      <PanelFrame className={className} busy={summary.status === "loading"}>
        {summary.status === "loading" ? (
          <div data-testid="patient-tasks-loading" className="flex items-start gap-2 py-2">
            <span className="sr-only">Loading open Tasks</span>
            <Skeleton className="mt-0.5 size-5 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1">
              <div className="flex h-5 items-center"><Skeleton className="h-3.5 w-3/4" /></div>
              <div className="flex h-4 items-center"><Skeleton className="h-3 w-1/2" /></div>
            </div>
          </div>
        ) : (
          <>
            <p role="alert" className="mt-1 text-xs text-destructive">
              This patient&apos;s open Tasks could not be loaded.
            </p>
            <Button size="sm" variant="outline" className="mt-2" onClick={onRetry}>
              Retry
            </Button>
          </>
        )}
      </PanelFrame>
    )
  }

  const { closedToday } = loaded
  const shared = loaded.names.length > 1
  const shown = showAll ? open : open.slice(0, collapsedRowLimit)

  const complete = (task: Task, reason: StaffTaskCloseReason) => {
    closing.current = { taskID: task.id, index: open.indexOf(task) }
    onComplete(task, reason)
  }

  return (
    <PanelFrame ref={section} className={className} count={loaded.openTaskCount}>
      {summary.status === "ready" && summary.refreshFailure && (
        <p role="alert" className="text-xs text-destructive">
          This list could not be refreshed.{" "}
          <button type="button" className="underline underline-offset-2" onClick={onRetry}>
            Retry
          </button>
        </p>
      )}
      {open.length === 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">Nothing open for this patient.</p>
      ) : (
        <ul className="divide-y">
          {shown.map((task) => (
            <OpenTaskRow
              key={task.id}
              task={task}
              current={task.id === currentTaskID}
              thisCall={task.id === callTaskID}
              callerName={shared ? task.callerName : undefined}
              pending={completion.pendingTaskID === task.id}
              disabled={Boolean(completion.pendingTaskID)}
              error={completion.errorTaskID === task.id ? completion.error : ""}
              onComplete={(reason) => complete(task, reason)}
              onOpen={onOpen && task.origin !== "INBOUND_MESSAGE_REVIEW" ? () => onOpen(task) : undefined}
            />
          ))}
        </ul>
      )}
      {open.length > collapsedRowLimit && (
        <Button
          size="sm"
          variant="ghost"
          className="mt-1 text-muted-foreground"
          aria-expanded={showAll}
          onClick={() => setShowAll((value) => !value)}
        >
          {showAll ? "Show fewer" : `Show all ${open.length}`}
        </Button>
      )}
      {showAll && loaded.openTaskCount > open.length && (
        <p className="mt-1 text-xs text-muted-foreground">
          Showing {open.length} of {loaded.openTaskCount}
        </p>
      )}
      {closedToday.length > 0 && (
        <>
          <h2 className="mt-4 text-sm font-medium">Closed today</h2>
          <ul className="mt-1 divide-y">
            {closedToday.map((task) => (
              <li key={task.id} data-patient-task-id={task.id} className="py-2">
                <div className="flex items-center gap-2">
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm text-muted-foreground">{task.title}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {taskCloseSummary(task) ?? "Completed"}
                    </p>
                  </div>
                  <Button
                    size="sm"
                    variant="ghost"
                    aria-label={`Reopen Task: ${task.title}`}
                    disabled={Boolean(reopening)}
                    onClick={() => onReopen(task)}
                  >
                    {reopening === task.id && <Spinner data-icon="inline-start" />}
                    Reopen
                  </Button>
                </div>
                {reopenFailure?.taskID === task.id && (
                  <p role="alert" className="mt-1 text-xs leading-snug text-destructive">
                    {reopenFailure.message}
                  </p>
                )}
              </li>
            ))}
          </ul>
        </>
      )}
    </PanelFrame>
  )
}

function PanelFrame({
  ref,
  className,
  busy,
  count,
  children,
}: {
  ref?: Ref<HTMLElement>
  className?: string
  busy?: boolean
  count?: number
  children: ReactNode
}) {
  return (
    <section
      ref={ref}
      tabIndex={-1}
      aria-label="Open Tasks"
      aria-busy={busy || undefined}
      className={cn("flex min-h-0 min-w-0 flex-col pt-3 pb-2 outline-none", className)}
    >
      <header className="flex h-7 shrink-0 items-center gap-2 pr-12 pl-5">
        <h2 className="text-sm font-medium">Open Tasks</h2>
        {count !== undefined && (
          <span className="text-xs tabular-nums text-muted-foreground">{count}</span>
        )}
      </header>
      <div data-testid="patient-tasks-body" className="min-h-0 min-w-0 overflow-x-hidden overflow-y-auto px-5">
        {children}
      </div>
    </section>
  )
}

function OpenTaskRow({
  task,
  current,
  thisCall,
  callerName,
  pending,
  disabled,
  error,
  onComplete,
  onOpen,
}: {
  task: Task
  current: boolean
  thisCall: boolean
  callerName?: string
  pending: boolean
  disabled: boolean
  error: string
  onComplete: (reason: StaffTaskCloseReason) => void
  onOpen?: () => void
}) {
  const closable = offersTaskCloseMenu(task)
  const age = taskRelativeTime(task).at
  const title = (
    <>
      {task.urgency === "high_priority" && (
        <span className="size-1.5 shrink-0 rounded-full bg-destructive">
          <span className="sr-only">Urgent</span>
        </span>
      )}
      <span className="truncate">{task.title}</span>
    </>
  )
  return (
    <li
      data-testid="patient-task-row"
      data-patient-task-id={task.id}
      tabIndex={-1}
      className="rounded-sm py-2 outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
    >
      <div className="flex min-w-0 items-start gap-2">
        {closable ? (
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  size="icon-xs"
                  variant="outline"
                  className="mt-0.5 rounded-full hover:text-success"
                  aria-label={`Mark done: ${task.title}`}
                  aria-busy={pending || undefined}
                  disabled={disabled}
                  onClick={() => onComplete("DONE")}
                />
              }
            >
              {pending ? <Spinner /> : <CheckIcon aria-hidden="true" />}
            </TooltipTrigger>
            <TooltipContent side="left">Done</TooltipContent>
          </Tooltip>
        ) : (
          <span aria-hidden="true" className="size-5 shrink-0" />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-baseline gap-2">
            {onOpen ? (
              <button
                type="button"
                aria-label={`Open Task: ${task.title}`}
                aria-current={current}
                className="flex min-w-0 flex-1 items-center gap-1.5 rounded-sm text-left text-sm leading-5 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/30 aria-[current=true]:font-medium"
                onClick={onOpen}
              >
                {title}
              </button>
            ) : (
              <p
                data-testid="patient-task-title"
                className={cn("flex min-w-0 flex-1 items-center gap-1.5 text-sm leading-5", current && "font-medium")}
              >
                {title}
              </p>
            )}
            <time
              dateTime={age}
              className="shrink-0 text-[10px] tabular-nums text-muted-foreground"
            >
              {formatRelativeTime(age)}
            </time>
          </div>
          <p className="truncate text-xs text-muted-foreground">
            {callerName && `${callerName} · `}
            {task.category ? taskGroupLabel(task.category) : taskOriginLabels[task.origin]} · {task.locationName}
          </p>
          {thisCall && (
            <Badge variant="outline" className="mt-1 h-5 text-[10px]">
              This call
            </Badge>
          )}
        </div>
        {closable && (
          <TaskCloseMenu
            trigger={
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={`Close as: ${task.title}`}
                disabled={disabled}
              />
            }
            onClose={onComplete}
          >
            <EllipsisIcon aria-hidden="true" />
          </TaskCloseMenu>
        )}
      </div>
      {error && (
        <p role="alert" className="mt-1 pl-7 text-xs leading-snug text-destructive">
          {error}
        </p>
      )}
    </li>
  )
}
