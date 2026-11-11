"use client"

import { useEffect, useEffectEvent, useId, useRef, useState, type ReactNode, type Ref } from "react"

import { CallingFailureNotice } from "@/components/workspace/calling-card"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent } from "@/components/ui/dialog"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import type { CallingCall, PhoneSummary, Task } from "@/lib/api/generated/types.gen"
import {
  wrapUpCounts,
  wrapUpDuration,
  wrapUpOffersFollowUp,
  wrapUpRequest,
  type WrapUpReason,
  type WrapUpRequest,
  type WrapUpState,
  type WrapUpTick,
} from "@/lib/calling/call-wrap-up"
import type { SettledWrapUp, SoftphoneRuntimeSnapshot } from "@/lib/calling/softphone-runtime"
import { useEngagementSummary } from "@/lib/clients/engagement"
import { useOpenTask } from "@/lib/clients/tasks"
import type { PortalQuery } from "@/lib/data/queries"
import {
  callWrapUpReasons,
  formatPhone,
  taskCloseReasonLabels,
  taskGroupLabel,
  taskOriginLabels,
} from "@/lib/vocabulary"

import { phoneNames } from "./engagement-workspace"
import { offersTaskCloseMenu } from "./task-close-menu"

type CallWrapUpProps = {
  call: CallingCall
  settled?: Pick<SettledWrapUp, "automatic" | "shortfall">
  saving: boolean
  failure?: SoftphoneRuntimeSnapshot["failure"]
  incomingCall?: ReactNode
  onRecover: () => void
  onSave: (request: WrapUpRequest) => void
  onDismiss: () => void
}

export function CallWrapUp(props: CallWrapUpProps & { sawConnected: boolean }) {
  const titleID = useId()
  const primary = useRef<HTMLButtonElement>(null)
  const [endedAt] = useState(() => Date.now())
  const summary = useEngagementSummary({
    practiceID: props.call.practiceId,
    phone: props.call.phone,
  })
  const callTaskID = props.call.entryPoint === "TASK" ? props.call.taskId : undefined
  const callTask = useOpenTask(callTaskID)
  const shortfall = props.settled?.shortfall
  const refresh = useEffectEvent(() => {
    void summary.retry()
    void callTask.retry()
  })
  useEffect(() => {
    if (shortfall) refresh()
  }, [shortfall])
  return (
    <Dialog
      open
      disablePointerDismissal
      onOpenChange={(open) => {
        if (!open && props.settled) props.onDismiss()
      }}
    >
      <DialogContent
        showCloseButton={props.settled !== undefined}
        initialFocus={primary}
        aria-labelledby={titleID}
        className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 text-sm sm:max-w-lg"
      >
        <CallWrapUpView
          {...props}
          titleID={titleID}
          primaryRef={primary}
          durationSeconds={
            props.sawConnected && props.call.connectedAt
              ? (endedAt - Date.parse(props.call.connectedAt)) / 1_000
              : undefined
          }
          summary={summary}
          callTask={callTaskID ? callTask : undefined}
          onRetry={() => void summary.retry()}
        />
      </DialogContent>
    </Dialog>
  )
}

export function CallWrapUpView({
  call,
  durationSeconds,
  summary,
  callTask,
  settled,
  saving,
  failure,
  incomingCall,
  onRecover,
  onRetry,
  onSave,
  onDismiss,
  titleID,
  primaryRef,
}: CallWrapUpProps & {
  durationSeconds?: number
  summary: PortalQuery<Pick<PhoneSummary, "names" | "openTasks" | "openTaskCount">>
  callTask?: PortalQuery<Task>
  onRetry: () => void
  titleID?: string
  primaryRef?: Ref<HTMLButtonElement>
}) {
  const [state, setState] = useState<WrapUpState>({ ticks: new Map(), followUp: false })
  const shortfall = settled?.shortfall
  const [appliedShortfall, setAppliedShortfall] = useState(shortfall)
  if (shortfall !== appliedShortfall) {
    setAppliedShortfall(shortfall)
    if (shortfall) {
      setState((current) => ({
        ...current,
        ticks: new Map(
          [...current.ticks].filter(([taskID]) => shortfall.unclosedTaskIds.includes(taskID)),
        ),
      }))
    }
  }
  const loaded = summary.status === "ready" ? summary.data : undefined
  const names = phoneNames(loaded?.names ?? [], call.displayName)
  const callTaskID = call.entryPoint === "TASK" ? call.taskId : undefined
  const ownTask =
    callTask?.status === "ready"
      ? callTask.data.state === "OPEN"
        ? callTask.data
        : undefined
      : loaded?.openTasks.find((task) => task.id === callTaskID)
  const tasks = [
    ...(ownTask ? [ownTask] : []),
    ...(loaded?.openTasks ?? []).filter(
      (task) => task.id !== callTaskID && offersTaskCloseMenu(task),
    ),
  ]
  const ticked = new Map(
    tasks.flatMap((task) => {
      const tick = state.ticks.get(task.id)
      return tick ? [[task.id, tick] as const] : []
    }),
  )
  const counts = wrapUpCounts({
    openTaskCount: loaded?.openTaskCount,
    closedInSummary:
      !ownTask && loaded?.openTasks.some((task) => task.id === callTaskID) ? 1 : 0,
    listed: tasks.length,
    ticked: ticked.size,
  })
  const followUpOffered = wrapUpOffersFollowUp(call)
  const followUp = followUpOffered && state.followUp && !settled?.automatic
  const followUpLost =
    Boolean(shortfall?.followUpNotCreated) || Boolean(settled?.automatic && state.followUp)

  function setTick(task: Task, reason: WrapUpReason | undefined) {
    setState((current) => {
      const ticks = new Map<string, WrapUpTick>(current.ticks)
      if (reason) {
        ticks.set(task.id, {
          reason,
          expectedVersion: current.ticks.get(task.id)?.expectedVersion ?? task.version,
        })
      } else {
        ticks.delete(task.id)
      }
      return { ...current, ticks }
    })
  }

  const finish = () =>
    settled ? onDismiss() : onSave(wrapUpRequest(call, { ticks: new Map(), followUp }))
  const closeTicked = () => onSave(wrapUpRequest(call, { ticks: ticked, followUp }))

  return (
    <>
      <header className="flex flex-col gap-1 px-5 pt-5">
        <p className="text-xs text-muted-foreground">
          Call ended
          {durationSeconds !== undefined && ` · ${wrapUpDuration(durationSeconds)}`}
        </p>
        <h2 id={titleID} className="truncate text-base font-medium">
          {names.length === 1 ? names[0] : formatPhone(call.phone) || "Unknown caller"}
        </h2>
      </header>
      <div
        data-testid="call-wrap-up-body"
        aria-busy={summary.status === "loading" || undefined}
        className="flex min-h-0 flex-col gap-3 overflow-y-auto px-5 py-4"
      >
        {incomingCall}
        {settled?.automatic && (
          <p role="status" className="text-xs text-muted-foreground">
            This call was marked resolved automatically. You can still close Tasks.
          </p>
        )}
        {shortfall && shortfall.changedTaskIds.length + shortfall.unclosedTaskIds.length > 0 && (
          <div role="alert" className="flex flex-col gap-1 text-xs text-destructive">
            {shortfall.changedTaskIds.length > 0 && (
              <p>
                {countTasks(shortfall.changedTaskIds.length)} changed since you opened this and stayed
                open. Check and tick again to close.
              </p>
            )}
            {shortfall.unclosedTaskIds.length > 0 && (
              <p>{countTasks(shortfall.unclosedTaskIds.length)} could not be closed. Try again.</p>
            )}
          </div>
        )}
        {summary.status === "loading" && (
          <div data-testid="call-wrap-up-loading" className="flex items-start gap-3 rounded-lg border p-3">
            <span className="sr-only">Loading open Tasks</span>
            <Skeleton className="mt-0.5 size-4 shrink-0 rounded-[4px]" />
            <div className="flex min-w-0 flex-1 flex-col gap-2">
              <Skeleton className="h-4 w-3/4" />
              <Skeleton className="h-3 w-1/2" />
            </div>
          </div>
        )}
        {summary.status === "failed" && (
          <div role="alert" className="flex flex-wrap items-center gap-2 text-xs text-destructive">
            <p>This patient&apos;s open Tasks could not be loaded. You can still record the call.</p>
            <Button size="sm" variant="outline" className="text-foreground" onClick={onRetry}>
              Retry
            </Button>
          </div>
        )}
        {loaded && tasks.length === 0 && (
          <p className="text-muted-foreground">
            {loaded.openTaskCount > 0
              ? "No Tasks to close from this call."
              : "No open Tasks for this patient."}
          </p>
        )}
        {tasks.length > 0 && (
          <>
            <p className="text-muted-foreground">
              Tick what this call took care of. Anything unticked stays open.
            </p>
            <ul className="flex flex-col gap-1.5">
              {tasks.map((task) => (
                <WrapUpTaskRow
                  key={task.id}
                  task={task}
                  thisCall={task.id === callTaskID}
                  callerName={names.length > 1 ? task.callerName : undefined}
                  note={
                    shortfall?.changedTaskIds.includes(task.id)
                      ? "Changed since you opened this"
                      : shortfall?.unclosedTaskIds.includes(task.id)
                        ? "Not closed"
                        : undefined
                  }
                  reason={ticked.get(task.id)?.reason}
                  disabled={saving}
                  onChange={(reason) => setTick(task, reason)}
                />
              ))}
            </ul>
            {counts.open > tasks.length && (
              <p className="text-xs text-muted-foreground">
                Showing {tasks.length} of {counts.open} open Tasks
              </p>
            )}
          </>
        )}
        {followUpOffered && (
          <div className="flex flex-col gap-1 py-1">
            <label className="flex items-center gap-3">
              <Checkbox
                checked={settled ? state.followUp && !followUpLost : state.followUp}
                disabled={saving || settled !== undefined}
                onCheckedChange={(checked) =>
                  setState((current) => ({ ...current, followUp: checked }))
                }
              />
              <span>Create a follow-up Task</span>
            </label>
            {settled && (
              <p
                data-testid="call-wrap-up-follow-up-reason"
                className={followUpLost ? "text-xs text-destructive" : "text-xs text-muted-foreground"}
              >
                {followUpLost && "The follow-up Task was not created. "}
                A follow-up can no longer be created from this call.
              </p>
            )}
          </div>
        )}
        {failure && <CallingFailureNotice failure={failure} onRecover={onRecover} />}
      </div>
      <footer className="flex flex-wrap justify-end gap-2 border-t px-5 py-3">
        {ticked.size > 0 && (
          <Button variant="outline" disabled={saving} onClick={finish}>
            Keep all open
          </Button>
        )}
        <Button ref={primaryRef} disabled={saving} onClick={ticked.size > 0 ? closeTicked : finish}>
          {saving && <Spinner data-icon="inline-start" />}
          {ticked.size > 0
            ? counts.closeLabel
            : settled
              ? "Close"
              : loaded && tasks.length === 0
                ? "Finish"
                : "Keep all open"}
        </Button>
      </footer>
    </>
  )
}

function countTasks(count: number) {
  return count === 1 ? "1 Task" : `${count} Tasks`
}

function WrapUpTaskRow({
  task,
  thisCall,
  callerName,
  note,
  reason,
  disabled,
  onChange,
}: {
  task: Task
  thisCall: boolean
  callerName?: string
  note?: string
  reason: WrapUpReason | undefined
  disabled: boolean
  onChange: (reason: WrapUpReason | undefined) => void
}) {
  return (
    <li
      data-testid="call-wrap-up-task"
      data-wrap-up-task-id={task.id}
      data-ticked={reason ? "" : undefined}
      className="flex flex-wrap items-start gap-2 rounded-lg border p-3 data-ticked:border-foreground"
    >
      <label className="flex min-w-0 flex-[1_1_13rem] items-start gap-3">
        <Checkbox
          className="mt-0.5"
          checked={reason !== undefined}
          disabled={disabled}
          onCheckedChange={(checked) => onChange(checked ? "DONE" : undefined)}
        />
        <span className="flex min-w-0 flex-col">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate font-medium">{task.title}</span>
            {thisCall && (
              <Badge variant="outline" className="h-5 shrink-0 text-[10px]">
                This call
              </Badge>
            )}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {callerName && `${callerName} · `}
            {task.category ? taskGroupLabel(task.category) : taskOriginLabels[task.origin]} ·{" "}
            {task.locationName}
          </span>
          {note && <span className="text-xs text-destructive">{note}</span>}
        </span>
      </label>
      {reason && (
        <NativeSelect
          size="sm"
          className="ml-auto shrink-0"
          aria-label={`Reason for closing: ${task.title}`}
          value={reason}
          disabled={disabled}
          onChange={(event) => onChange(event.target.value as WrapUpReason)}
        >
          {callWrapUpReasons.map((option) => (
            <NativeSelectOption key={option} value={option}>
              {taskCloseReasonLabels[option]}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      )}
    </li>
  )
}
