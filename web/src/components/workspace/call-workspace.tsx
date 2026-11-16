"use client"

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
  returnTask,
  onReturnToTask,
  onOpenRecoveryTask,
}: {
  call: CallingCall
  returnTask: Task | undefined
  onReturnToTask: (() => void) | undefined
  onOpenRecoveryTask: (taskID: string) => void
}) {
  const formattedPhone = formatPhone(call.phone)
  const contactName =
    call.displayName &&
    call.displayName !== call.phone &&
    call.displayName !== formattedPhone
      ? call.displayName
      : ""
  const taskAction = call.recoveryTask
    ? {
        label: "Open Task",
        onClick: () => onOpenRecoveryTask(call.recoveryTask!.id),
      }
    : returnTask && onReturnToTask
      ? { label: "Back to Task", onClick: onReturnToTask }
      : undefined
  return (
    <section className="flex min-h-0 flex-1 flex-col">
      <header className="px-5 py-5 pr-12">
        <h2 className="text-base font-medium leading-snug">
          {callStateLabels[call.state]}
        </h2>
        {contactName && <p className="mt-3 font-medium">{contactName}</p>}
        <p className="mt-1 text-sm tabular-nums text-muted-foreground">
          {formattedPhone} · {call.locationName}
        </p>
        {call.transferReason && (
          <p className="mt-3 text-sm leading-6">{call.transferReason}</p>
        )}
        {call.recoveryTask && (
          <p className="mt-2 text-xs text-muted-foreground">
            {call.recoveryTask.title}
          </p>
        )}
        {taskAction && (
          <Button className="mt-4 w-full" variant="outline" onClick={taskAction.onClick}>
            {taskAction.label === "Back to Task" && <ArrowLeftIcon />}
            {taskAction.label}
          </Button>
        )}
      </header>
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
    </section>
  )
}
