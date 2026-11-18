"use client"

import { useState } from "react"

import { useSettledState } from "@/hooks/use-settled-state"
import type { Task } from "@/lib/api/generated/types.gen"
import { signInRequired } from "@/lib/clients/portal-request"
import { completeTask, reopenTask } from "@/lib/clients/tasks"
import { cn } from "@/lib/utils"
import { taskCloseSummary } from "@/lib/vocabulary"

import { ClosedTaskActions, TaskCheck } from "./task-check"

export function TextReviewCard({ task, reviewed, onUpdated, onNext }: {
  task: Task
  reviewed: boolean
  onUpdated: (task: Task) => void
  onNext?: () => void
}) {
  const open = task.state === "OPEN"
  const needsReview = open && !reviewed
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const stateSettled = useSettledState(task.state)
  async function transition() {
    if (pending || needsReview) return
    if (!open && !stateSettled) return
    setPending(true)
    setError("")
    const outcome = await (open ? completeTask(task, "DONE") : reopenTask(task))
    setPending(false)
    if (!outcome.ok) {
      setError(signInRequired(outcome.failure)
        ? "Sign in again to update this conversation."
        : "Could not update this conversation. Review the latest messages and try again.")
      return
    }
    onUpdated(outcome.data)
  }
  return (
    <section aria-label="Text review" className="flex flex-col rounded-lg bg-muted/60 px-3 py-2.5">
      <header className="flex items-start gap-2">
        <TaskCheck
          open={open}
          label="Mark done"
          title={needsReview ? "Load the latest messages first" : "Mark done without replying"}
          pending={pending}
          blocked={needsReview}
          onComplete={() => void transition()}
        />
        <h2 className={cn("min-w-0 flex-1 text-sm font-medium leading-5", !open && "text-muted-foreground line-through")}>
          {task.preview || task.title}
        </h2>
      </header>
      <div className="pl-7">
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {open
            ? "Replying closes this. Tick it if no reply is needed."
            : taskCloseSummary(task) ?? "Completed"}
        </p>
        {!open && (
          <ClosedTaskActions
            className="mt-2"
            pending={pending}
            settled={stateSettled}
            onNext={onNext}
            onReopen={() => void transition()}
          />
        )}
        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      </div>
    </section>
  )
}

export function textReviewKey(task: Task | undefined) {
  return task ? `${task.id}:${task.version}` : ""
}
