"use client"

import type { Task } from "@/lib/api/generated/types.gen"
import {
  taskGroupLabel,
  taskOriginLabels,
  taskUrgencyLabels,
} from "@/lib/vocabulary"

export function TaskSourceDetails({ task }: { task: Task }) {
  return (
    <>
      <Metadata label="Source" value={taskOriginLabels[task.origin]} />
      {task.category && (
        <Metadata label="Category" value={taskGroupLabel(task.category)} />
      )}
      <Metadata label="Urgency" value={taskUrgencyLabels[task.urgency]} />
      {task.callerName && (
        <Metadata label="Sourced name" value={task.callerName} />
      )}
    </>
  )
}

export function actorLabel(actor: Task["createdBy"]) {
  if (actor.email) return actor.email
  return actor.kind === "SERVICE" ? "Abita AI" : actor.subject
}

export function Metadata({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <span className="block text-xs font-medium">
        {label}
      </span>
      <span className="mt-1 block truncate text-foreground">{value}</span>
    </div>
  )
}
