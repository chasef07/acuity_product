"use client"

import { useId, useState } from "react"

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
  wrapUpDuration,
  wrapUpOffersFollowUp,
  wrapUpRequest,
  type WrapUpReason,
  type WrapUpRequest,
  type WrapUpState,
} from "@/lib/calling/call-wrap-up"
import type { SoftphoneRuntimeSnapshot } from "@/lib/calling/softphone-runtime"
import { useEngagementSummary } from "@/lib/clients/engagement"
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
  saving: boolean
  failure?: SoftphoneRuntimeSnapshot["failure"]
  onRecover: () => void
  onSave: (request: WrapUpRequest) => void
}

export function CallWrapUp(props: CallWrapUpProps & { sawConnected: boolean }) {
  const titleID = useId()
  const [endedAt] = useState(() => Date.now())
  const summary = useEngagementSummary({
    practiceID: props.call.practiceId,
    phone: props.call.phone,
  })
  return (
    <Dialog open disablePointerDismissal>
      <DialogContent
        showCloseButton={false}
        aria-labelledby={titleID}
        className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 text-sm sm:max-w-lg"
      >
        <CallWrapUpView
          {...props}
          titleID={titleID}
          durationSeconds={
            props.sawConnected && props.call.connectedAt
              ? (endedAt - Date.parse(props.call.connectedAt)) / 1_000
              : undefined
          }
          summary={summary}
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
  saving,
  failure,
  onRecover,
  onRetry,
  onSave,
  titleID,
}: CallWrapUpProps & {
  durationSeconds?: number
  summary: PortalQuery<Pick<PhoneSummary, "names" | "openTasks" | "openTaskCount">>
  onRetry: () => void
  titleID?: string
}) {
  const [state, setState] = useState<WrapUpState>({ ticks: new Map(), followUp: false })
  const loaded = summary.status === "ready" ? summary.data : undefined
  const names = phoneNames(loaded?.names ?? [], call.displayName)
  const tasks = loaded
    ? [
        ...loaded.openTasks.filter((task) => task.id === call.taskId),
        ...loaded.openTasks.filter((task) => task.id !== call.taskId),
      ].filter(offersTaskCloseMenu)
    : []
  const ticked = new Map(
    tasks.flatMap((task) => {
      const reason = state.ticks.get(task.id)
      return reason ? [[task.id, reason] as const] : []
    }),
  )
  const followUpOffered = wrapUpOffersFollowUp(call)
  const followUp = followUpOffered && state.followUp

  function setTick(taskID: string, reason: WrapUpReason | undefined) {
    setState((current) => {
      const ticks = new Map(current.ticks)
      if (reason) ticks.set(taskID, reason)
      else ticks.delete(taskID)
      return { ...current, ticks }
    })
  }

  const keepAll = () => onSave(wrapUpRequest(call, { ticks: new Map(), followUp }))
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
          <p className="text-muted-foreground">No open Tasks for this patient.</p>
        )}
        {loaded && tasks.length > 0 && (
          <>
            <p className="text-muted-foreground">
              Tick what this call took care of. Anything unticked stays open.
            </p>
            <ul className="flex flex-col gap-1.5">
              {tasks.map((task) => (
                <WrapUpTaskRow
                  key={task.id}
                  task={task}
                  thisCall={task.id === call.taskId}
                  callerName={names.length > 1 ? task.callerName : undefined}
                  reason={ticked.get(task.id)}
                  disabled={saving}
                  onChange={(reason) => setTick(task.id, reason)}
                />
              ))}
            </ul>
            {loaded.openTaskCount > loaded.openTasks.length && (
              <p className="text-xs text-muted-foreground">
                Showing {loaded.openTasks.length} of {loaded.openTaskCount}
              </p>
            )}
          </>
        )}
        {followUpOffered && (
          <label className="flex items-center gap-3 py-1">
            <Checkbox
              checked={state.followUp}
              disabled={saving}
              onCheckedChange={(checked) =>
                setState((current) => ({ ...current, followUp: checked }))
              }
            />
            <span>Create a follow-up Task</span>
          </label>
        )}
        {failure && <CallingFailureNotice failure={failure} onRecover={onRecover} />}
      </div>
      <footer className="flex flex-wrap justify-end gap-2 border-t px-5 py-3">
        {loaded && tasks.length === 0 ? (
          <Button disabled={saving} onClick={keepAll}>
            {saving && <Spinner data-icon="inline-start" />}
            Finish
          </Button>
        ) : (
          <>
            <Button variant="outline" disabled={saving} onClick={keepAll}>
              Keep all open
            </Button>
            {loaded && (
              <Button disabled={saving || ticked.size === 0} onClick={closeTicked}>
                {saving && <Spinner data-icon="inline-start" />}
                {ticked.size === 0
                  ? "Tick a Task to close it"
                  : `Close ${ticked.size} · keep ${tasks.length - ticked.size} open`}
              </Button>
            )}
          </>
        )}
      </footer>
    </>
  )
}

function WrapUpTaskRow({
  task,
  thisCall,
  callerName,
  reason,
  disabled,
  onChange,
}: {
  task: Task
  thisCall: boolean
  callerName?: string
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
