"use client"

import { useState } from "react"
import { CheckIcon, FlagIcon } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { portalClient } from "@/lib/api/client"
import { reviewOperatorAiCallIssue } from "@/lib/api/generated/sdk.gen"
import type { OperatorAiCallIssue, OperatorAiCallIssueOutcome } from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { issueReasons } from "./agent-call-panel"

const outcomes: { value: OperatorAiCallIssueOutcome; label: string }[] = [
  { value: "CONFIRMED", label: "Real issue" },
  { value: "NOT_AN_ISSUE", label: "Not an issue" },
]

function formatIssueTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown time"
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date)
}

// Staff-flagged calls awaiting Acuity review, regardless of the selected range.
export function PendingCallIssues({ issues, onSelect }: {
  issues: OperatorAiCallIssue[]
  onSelect: (interactionID: string) => void
}) {
  if (!issues.length) return null
  return (
    <Alert role="status" className="mt-5 sm:mt-6">
      <FlagIcon aria-hidden="true" />
      <AlertTitle>{issues.length === 1 ? "1 call flagged by staff needs review" : `${issues.length} calls flagged by staff need review`}</AlertTitle>
      <AlertDescription>
        <ul className="mt-1 flex w-full flex-col gap-1.5">
          {issues.map((issue) => (
            <li key={issue.interactionId} className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <span>
                <span className="font-medium text-foreground">{formatUSPhone(issue.phone)}</span>
                {" · "}{issueReasons[issue.reason]} · Flagged by {issue.reportedBy} · {formatIssueTime(issue.reportedAt)}
              </span>
              <Button size="xs" variant="outline" aria-label={`Review call from ${formatUSPhone(issue.phone)}`} onClick={() => onSelect(issue.interactionId)}>
                Review
              </Button>
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  )
}

// Acuity's review of a staff flag. Practices never see this decision.
export function CallIssueReview({ interactionID, initialIssue, onChange }: {
  interactionID: string
  initialIssue?: OperatorAiCallIssue
  onChange: (issue: OperatorAiCallIssue) => void
}) {
  const [issue, setIssue] = useState(initialIssue)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")

  async function review(outcome: OperatorAiCallIssueOutcome) {
    if (saving || issue?.review?.outcome === outcome) return
    setSaving(true)
    setError("")
    try {
      const token = await getAccessToken()
      if (!token) throw new Error("Sign in again to save the review.")
      const result = await reviewOperatorAiCallIssue({ client: portalClient(token), path: { interactionId: interactionID }, body: { outcome } })
      if (!result.data) throw new Error("The review could not be saved. Please try again.")
      setIssue(result.data)
      onChange(result.data)
    } catch (error) {
      setError(error instanceof Error ? error.message : "The review could not be saved. Please try again.")
    } finally {
      setSaving(false)
    }
  }

  if (!issue) return null
  return (
    <section aria-label="Staff flag" className="border-b px-5 py-4 sm:px-6">
      <Alert role="status">
        <FlagIcon aria-hidden="true" />
        <AlertTitle>Flagged by {issue.reportedBy}</AlertTitle>
        <AlertDescription>
          <p>{issueReasons[issue.reason]} · {formatIssueTime(issue.reportedAt)}</p>
          <div className="mt-2 flex flex-wrap gap-2">
            {outcomes.map((outcome) => {
              const selected = issue.review?.outcome === outcome.value
              return (
                <Button key={outcome.value} size="sm" variant={selected ? "default" : "outline"} aria-pressed={selected} disabled={saving} onClick={() => void review(outcome.value)}>
                  {selected && <CheckIcon data-icon="inline-start" />}
                  {outcome.label}
                </Button>
              )
            })}
          </div>
          {issue.review && (
            <p className="text-xs">Reviewed by {issue.review.reviewedBy} · {formatIssueTime(issue.review.reviewedAt)}</p>
          )}
          {error && <p className="text-destructive">{error}</p>}
        </AlertDescription>
      </Alert>
    </section>
  )
}
