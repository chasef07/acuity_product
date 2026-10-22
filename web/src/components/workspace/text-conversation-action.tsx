"use client"

import { useState } from "react"

import { Button } from "@/components/ui/button"
import { useSettledState } from "@/hooks/use-settled-state"
import type { Task } from "@/lib/api/generated/types.gen"
import { completeTask, reopenTask } from "@/lib/clients/tasks"

export function TextConversationAction({ task, reviewedTask, onUpdated, onNext }: { task: Task; reviewedTask?: Task; onUpdated: (task: Task) => void; onNext?: () => void }) {
  const needsReview = task.state === "OPEN" && (reviewedTask?.id !== task.id || reviewedTask.version !== task.version)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const stateSettled = useSettledState(task.state)
  async function transition() {
    if (pending || needsReview) return
    if (task.state === "COMPLETED" && !stateSettled) return
    setPending(true)
    setError("")
    const outcome = await (task.state === "OPEN" ? completeTask : reopenTask)(task)
    setPending(false)
    if (!outcome.ok) {
      const { kind } = outcome.failure
      setError(kind === "signedOut" || kind === "unauthenticated"
        ? "Sign in again to update this conversation."
        : "Could not update this conversation. Review the latest messages and try again.")
      return
    }
    onUpdated(outcome.data)
  }
  return <div className="flex items-center gap-2">
    {error && <span role="alert" className="max-w-52 text-xs text-destructive">{error}</span>}
    {task.state === "COMPLETED" && <Button size="sm" variant="ghost" className="text-xs text-muted-foreground" disabled={pending || !stateSettled} onClick={() => void transition()}>{pending ? "Saving…" : "Reopen"}</Button>}
    {task.state === "COMPLETED" && <span className="text-xs text-muted-foreground">Completed</span>}
    {task.state === "COMPLETED" && onNext && <Button size="sm" onClick={onNext}>Next task</Button>}
    {task.state === "OPEN" && <Button size="sm" variant="ghost" disabled={pending || needsReview} title={needsReview ? "Load the latest messages before marking done." : undefined} onClick={() => void transition()}>{pending ? "Saving…" : "Mark done"}</Button>}
  </div>
}
