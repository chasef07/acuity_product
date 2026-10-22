"use client"

import { PhoneCallIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { Task } from "@/lib/api/generated/types.gen"
import { useTaskCallEligibility } from "@/lib/clients/calls"

export function TaskCallAction({ task, canCall, pending, onCall }: {
  task: Task
  canCall: boolean
  pending: boolean
  onCall: (task: Task) => void
}) {
  const eligibility = useTaskCallEligibility({ task, enabled: canCall })
  const answer =
    eligibility.status === "loading" ||
    (eligibility.status === "failed" && eligibility.failure.kind === "signedOut")
      ? undefined
      : eligibility
  const current = answer?.status === "ready" && !answer.refreshFailure ? answer.data : undefined
  const taskCallingEligible = canCall && Boolean(current?.eligible)
  const taskCallingReason = !canCall
    ? "Calling is not enabled for this account."
    : !answer
      ? "Checking Call route…"
      : current
        ? current.reason
        : "Call eligibility is temporarily unavailable."

  return <Button variant="outline" size="sm" className="h-7 shadow-xs" disabled={!taskCallingEligible || pending} title={taskCallingEligible ? "Call this Task" : taskCallingReason} onClick={() => onCall(task)}>
    {pending ? <Spinner /> : <PhoneCallIcon />} {pending ? "Preparing…" : "Call"}
  </Button>
}
