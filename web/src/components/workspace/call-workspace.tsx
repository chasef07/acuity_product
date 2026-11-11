"use client"

import type { ReactNode } from "react"
import { ArrowLeftIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { CallingCall, Task } from "@/lib/api/generated/types.gen"
import { formatTimestamp } from "@/lib/format-time"
import {
  callDirectionLabels,
  callEntryPointLabels,
  callStateLabels,
  formatPhone,
} from "@/lib/vocabulary"

import { CallRecordingSource, VoicemailSource } from "./recording-source"
import { Metadata } from "./task-details"

export function CallWorkspace({
  call,
  live,
  returnTask,
  onReturnToTask,
  onOpenRecoveryTask,
  children,
}: {
  call: CallingCall
  live: boolean
  returnTask: Task | undefined
  onReturnToTask: (() => void) | undefined
  onOpenRecoveryTask: (taskID: string) => void
  children?: ReactNode
}) {
  const formattedPhone = formatPhone(call.phone)
  const contactName = callContactName(call)
  const taskAction = call.recoveryTask
    ? {
        label: "Open Task",
        onClick: () => onOpenRecoveryTask(call.recoveryTask!.id),
      }
    : returnTask && onReturnToTask
      ? { label: "Back to Task", onClick: onReturnToTask }
      : undefined
  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col">
      {live ? (
        <header className="flex h-11 shrink-0 items-center gap-2 pt-3 pr-12 pl-5">
          <h2 className="min-w-0 flex-1 truncate text-sm font-medium">
            {callStateLabels[call.state]}
          </h2>
          {taskAction && (
            <Button size="sm" variant="outline" onClick={taskAction.onClick}>
              {taskAction.label}
            </Button>
          )}
        </header>
      ) : (
        <header className="min-h-11 shrink-0 px-5 pt-5 pr-12">
          <h2 className="text-base font-medium leading-snug">
            {callStateLabels[call.state]}
          </h2>
        </header>
      )}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {!live && (
          <div className="px-5 pb-5 pr-12">
            {contactName && <p className="mt-3 font-medium">{contactName}</p>}
            <p className="mt-1 text-sm tabular-nums text-muted-foreground">
              {formattedPhone} · {call.locationName}
            </p>
            {call.transferReason && (
              <p className="mt-3 text-sm leading-6">{call.transferReason}</p>
            )}
            {taskAction && (
              <Button className="mt-4 w-full" variant="outline" onClick={taskAction.onClick}>
                {taskAction.label === "Back to Task" && <ArrowLeftIcon />}
                {taskAction.label}
              </Button>
            )}
          </div>
        )}
        {children}
        {call.voicemail && <VoicemailSource call={call} />}
        {call.recording && <CallRecordingSource call={call} />}
        <details className="group border-t px-5 py-4">
          <summary className="cursor-pointer text-sm font-medium">Details</summary>
          <div className="mt-3 flex flex-col gap-3">
            <Metadata label="Direction" value={callDirectionLabels[call.direction]} />
            <Metadata label="Started from" value={callEntryPointLabels[call.entryPoint]} />
            {call.connectedAt && (
              <Metadata label="Connected" value={formatTimestamp(call.connectedAt)} />
            )}
          </div>
        </details>
      </div>
    </section>
  )
}

export function callContactName(call: Pick<CallingCall, "displayName" | "phone">) {
  return call.displayName &&
    call.displayName !== call.phone &&
    call.displayName !== formatPhone(call.phone)
    ? call.displayName
    : ""
}
