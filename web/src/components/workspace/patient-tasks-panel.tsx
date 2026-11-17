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
  taskRelativeTime,
} from "@/lib/vocabulary"

import { offersTaskCloseMenu, TaskCloseMenu } from "./task-close-menu"

const collapsedRowLimit = 8

type TaskFailure = { taskID: string; message: string }

export type ExpandedTask = { task: Task; content: ReactNode }

type PatientTasksPanelProps = {
  currentTaskID?: string
  expanded?: ExpandedTask
  callTaskID?: string
  completion: { pending: readonly string[]; errors: Readonly<Record<string, string>> }
  className?: string
  onComplete: (task: Task, reason: StaffTaskCloseReason) => void
  onOpen?: (task: Task) => void
  opensReviews?: boolean
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
  expanded,
  callTaskID,
  completion,
  reopening,
  reopenFailure,
  className,
  onComplete,
  onOpen,
  opensReviews = false,
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
  const listed = useMemo(() => {
    if (!loaded) return undefined
    const tasks = loaded.openTasks
    return {
      open: [
        ...tasks.filter((task) => task.id === callTaskID),
        ...tasks.filter((task) => task.id !== callTaskID),
      ],
      count: loaded.openTaskCount,
    }
  }, [loaded, callTaskID])
  const open = listed?.open

  useEffect(() => {
    const closed = closing.current
    if (!closed || !open) return
    if (open.some((task) => task.id === closed.taskID)) {
      if (!completion.pending.includes(closed.taskID)) closing.current = undefined
      return
    }
    closing.current = undefined
    if (document.activeElement && document.activeElement !== document.body) return
    const next = open[closed.index] ?? open.at(-1)
    const row = next && section.current?.querySelector<HTMLElement>(`[data-patient-task-id="${next.id}"]`)
    ;(row ?? section.current)?.focus()
  }, [open, completion.pending])

  if (!loaded || !listed || !open) {
    return (
      <PanelFrame className={className} busy={summary.status === "loading"}>
        {expanded && <ul><ExpandedRow expanded={expanded} /></ul>}
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

  const closedToday = loaded.closedToday.filter((task) => task.id !== expanded?.task.id)
  const expandedListed = expanded && open.some((task) => task.id === expanded.task.id)
  const shared = loaded.names.length > 1
  const shown = showAll
    ? open
    : open.filter((task, index) => index < collapsedRowLimit || task.id === expanded?.task.id)
  const { count } = listed
  const capped = count > open.length

  const complete = (task: Task, reason: StaffTaskCloseReason) => {
    closing.current = { taskID: task.id, index: open.indexOf(task) }
    onComplete(task, reason)
  }

  return (
    <div className={cn("flex min-h-0 min-w-0 flex-col gap-3", className)}>
    <PanelFrame
      ref={section}
      count={count}
    >
      {summary.status === "ready" && summary.refreshFailure && (
        <p role="alert" className="text-xs text-destructive">
          This list could not be refreshed.{" "}
          <button type="button" className="underline underline-offset-2" onClick={onRetry}>
            Retry
          </button>
        </p>
      )}
      {expanded && !expandedListed && <ul><ExpandedRow expanded={expanded} /></ul>}
      {open.length === 0 ? (
        !expanded && <p className="mt-1 text-xs text-muted-foreground">Nothing open for this patient.</p>
      ) : (
        <ul className="-mx-2 flex flex-col gap-0.5">
          {shown.map((task) => task.id === expanded?.task.id ? (
            <ExpandedRow key={task.id} expanded={expanded} />
          ) : (
            <OpenTaskRow
              key={task.id}
              task={task}
              current={task.id === currentTaskID}
              thisCall={task.id === callTaskID}
              callerName={shared ? task.callerName : undefined}
              pending={completion.pending.includes(task.id)}
              error={completion.errors[task.id] ?? ""}
              onComplete={(reason) => complete(task, reason)}
              onOpen={onOpen && (offersTaskCloseMenu(task) || opensReviews) ? () => onOpen(task) : undefined}
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
          {showAll ? "Show fewer" : capped ? `Show ${open.length} of ${count}` : `Show all ${count}`}
        </Button>
      )}
      {showAll && capped && (
        <p className="mt-1 text-xs text-muted-foreground">
          Showing {open.length} of {count}
        </p>
      )}
    </PanelFrame>
      {closedToday.length > 0 && (
        <section aria-label="Done today" className="shrink-0 rounded-2xl border bg-background px-5 pt-3 pb-2 shadow-sm">
          <h2 className="flex h-8 items-center text-xs font-medium text-muted-foreground">
            Done today
          </h2>
          <ul className="-mx-2 flex flex-col gap-0.5">
            {closedToday.map((task) => (
              <li key={task.id} data-patient-task-id={task.id} className="group/done rounded-md px-2 py-1.5 hover:bg-muted/60">
                <div className="flex items-start gap-2">
                  <span aria-hidden="true" className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-muted-foreground/80 text-background">
                    <CheckIcon className="size-3" />
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm text-muted-foreground line-through decoration-muted-foreground/60">{task.title}</p>
                    <p className="truncate text-xs text-muted-foreground">
                      {taskCloseSummary(task) ?? "Completed"}
                    </p>
                  </div>
                  <Button
                    size="xs"
                    variant="ghost"
                    className="text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/done:opacity-100 focus-visible:opacity-100"
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
        </section>
      )}
    </div>
  )
}

function ExpandedRow({ expanded }: { expanded: ExpandedTask }) {
  return (
    <li
      data-patient-task-id={expanded.task.id}
      data-expanded="true"
      className="py-1"
    >
      {expanded.content}
    </li>
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
      className={cn("flex min-h-0 min-w-0 flex-col rounded-2xl border bg-background pt-3 pb-2 shadow-sm outline-none", className)}
    >
      <header className="flex h-8 shrink-0 items-center gap-2 pr-11 pl-5">
        <h2 className="truncate text-xs font-medium text-muted-foreground">Open Tasks</h2>
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
  error,
  onComplete,
  onOpen,
}: {
  task: Task
  current: boolean
  thisCall: boolean
  callerName?: string
  pending: boolean
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
      className="rounded-md px-2 py-1.5 outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/30"
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
                  disabled={pending}
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
            {onOpen && closable ? (
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
                disabled={pending}
              />
            }
            onClose={onComplete}
          >
            <EllipsisIcon aria-hidden="true" />
          </TaskCloseMenu>
        )}
        {!closable && onOpen && !current && (
          <Button
            size="xs"
            variant="outline"
            aria-label={`Open Task: ${task.title}`}
            onClick={onOpen}
          >
            Open
          </Button>
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
