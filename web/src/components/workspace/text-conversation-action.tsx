"use client"

import { useState } from "react"
import { CheckIcon, RotateCcwIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { useSettledState } from "@/hooks/use-settled-state"
import type { Task } from "@/lib/api/generated/types.gen"
import { signInRequired } from "@/lib/clients/portal-request"
import { completeTask, reopenTask } from "@/lib/clients/tasks"
import { cn } from "@/lib/utils"
import { taskCloseSummary } from "@/lib/vocabulary"

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
        {open ? (
          <Button
            size="icon-xs"
            variant="outline"
            className="mt-0.5 shrink-0 rounded-full bg-background text-transparent hover:text-success focus-visible:text-success [@media(hover:none)]:text-muted-foreground/50"
            aria-label="Mark done"
            title={needsReview ? "Load the latest messages first" : "Mark done without replying"}
            disabled={pending || needsReview}
            onClick={() => void transition()}
          >
            {pending ? <Spinner className="text-muted-foreground" /> : <CheckIcon />}
          </Button>
        ) : (
          <span aria-hidden="true" className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-foreground text-background">
            <CheckIcon className="size-3" />
          </span>
        )}
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
          <div className="mt-2 flex items-center gap-1">
            {onNext && <Button size="sm" onClick={onNext}>Next task</Button>}
            <Button size="sm" variant="ghost" className="text-muted-foreground" disabled={pending || !stateSettled} onClick={() => void transition()}>
              {pending ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}Reopen
            </Button>
          </div>
        )}
        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
      </div>
    </section>
  )
}
