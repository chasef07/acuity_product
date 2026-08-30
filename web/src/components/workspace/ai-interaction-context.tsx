"use client"

import { useEffect, useState } from "react"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type {
  AiAppointmentFacts,
  AiInteractionDetail,
} from "@/lib/api/generated/types.gen"
import {
  aiCallTimelinePresentation,
  appointmentOutcomeTitle,
} from "@/lib/ai-interactions"
export function AIInteractionContext({
  interactionID,
  detail,
  loading,
  error,
  onReview,
}: {
  interactionID: string
  detail?: AiInteractionDetail
  loading: boolean
  error: string
  onReview?: (interactionID: string) => Promise<boolean>
}) {
  const [reviewRequest, setReviewRequest] = useState<{
    interactionID: string
    state: "saving" | "saved" | "failed"
  }>({ interactionID: "", state: "saved" })
  useEffect(() => {
    if (
      !interactionID ||
      !detail ||
      !onReview ||
      detail.appointmentOutcome === "INDETERMINATE" ||
      reviewRequest.interactionID === interactionID
    ) return
    let cancelled = false
    void Promise.resolve().then(async () => {
      if (cancelled) return
      setReviewRequest({ interactionID, state: "saving" })
      const reviewed = await onReview(interactionID).catch(() => false)
        if (cancelled) return
        setReviewRequest({
          interactionID,
          state: reviewed ? "saved" : "failed",
        })
    })
    return () => {
      cancelled = true
    }
  }, [detail, interactionID, onReview, reviewRequest.interactionID])

  async function retryReview() {
    if (!onReview) return
    setReviewRequest({ interactionID, state: "saving" })
    const reviewed = await onReview(interactionID).catch(() => false)
    setReviewRequest({
      interactionID,
      state: reviewed ? "saved" : "failed",
    })
  }

  if (loading) {
    return (
      <div className="flex min-h-48 flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
        <Spinner />
        Loading AI call
      </div>
    )
  }
  if (error) {
    return (
      <div className="p-4">
        <Alert variant="destructive">
          <AlertTitle>AI call unavailable</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      </div>
    )
  }
  if (!detail) return null
  const reviewFailed =
    reviewRequest.interactionID === interactionID &&
    reviewRequest.state === "failed"
  return (
    <div className="min-h-0 flex-1 overflow-y-auto">
      {reviewFailed && (
        <div className="px-4 pt-4">
          <Alert variant="destructive">
            <AlertTitle>Review status not saved</AlertTitle>
            <AlertDescription className="flex flex-wrap items-center gap-3">
              <span>
                This appointment will remain in the sidebar until its review
                status is saved.
              </span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => void retryReview()}
              >
                Try again
              </Button>
            </AlertDescription>
          </Alert>
        </div>
      )}
      <AIInteractionDetailView detail={detail} />
    </div>
  )
}

function AIInteractionDetailView({ detail }: { detail: AiInteractionDetail }) {
  const hasAppointmentOutcome = detail.appointmentOutcome !== "INDETERMINATE"
  const title = hasAppointmentOutcome
    ? appointmentOutcomeTitle(detail.appointmentOutcome)
    : aiCallTimelinePresentation(detail.appointmentOutcome, detail.status).title
  const exception = aiCallException(detail)
  return (
    <div>
      <section className="px-5 py-5 pr-12">
        <div className="flex flex-wrap items-center gap-2">
          <h2 className="text-lg font-semibold tracking-[-0.015em]">{title}</h2>
          {exception && (
            <Badge variant={exception.variant}>{exception.label}</Badge>
          )}
        </div>
        {detail.summary && (
          <p className="mt-3 text-sm leading-6">{detail.summary}</p>
        )}
        <p className="mt-3 text-xs tabular-nums text-muted-foreground">
          {formatPhone(detail.phone)} · {detail.locationName}
        </p>
      </section>

      {hasAppointmentOutcome && (
        <section className="border-t px-5 py-4">
          <AppointmentSummary
            facts={detail.appointment}
            contextLocation={detail.locationName}
          />
          {detail.previousAppointment &&
            hasAppointmentFacts(detail.previousAppointment) && (
              <p className="mt-3 border-t pt-3 text-xs leading-5 text-muted-foreground">
                Previously {appointmentFactLine(detail.previousAppointment)}
              </p>
            )}
        </section>
      )}

      <details className="group border-t px-5 py-4">
        <summary className="cursor-pointer text-sm font-medium">Details</summary>
        <dl className="mt-3 flex flex-col gap-3 text-sm">
          <DetailValue
            label="Started"
            value={formatDateTime(detail.startedAt)}
          />
          <DetailValue
            label="Call length"
            value={formatDuration(detail.startedAt, detail.endedAt)}
          />
        </dl>
      </details>

      <details className="group border-t px-5 py-4">
        <summary className="cursor-pointer text-sm font-medium">Evidence</summary>
        <div className="mt-4 flex flex-col gap-3">
          <dl className="flex flex-col gap-3 text-xs">
            <EvidenceValue label="Source call" value={detail.sourceCallId} />
            {detail.externalPatientId && (
              <EvidenceValue
                label="External patient"
                value={detail.externalPatientId}
              />
            )}
            {detail.oldAppointmentId && (
              <EvidenceValue
                label="Previous appointment"
                value={detail.oldAppointmentId}
              />
            )}
            {detail.newAppointmentId && (
              <EvidenceValue
                label="New appointment"
                value={detail.newAppointmentId}
              />
            )}
          </dl>
          {detail.bookingResult && (
            <ReceiptEvidence
              label="Booking receipt"
              value={detail.bookingResult}
            />
          )}
          {detail.cancellationResult && (
            <ReceiptEvidence
              label="Cancellation receipt"
              value={detail.cancellationResult}
            />
          )}
        </div>
      </details>
    </div>
  )
}

function AppointmentSummary({
  facts,
  contextLocation,
}: {
  facts: AiAppointmentFacts
  contextLocation: string
}) {
  const appointmentTime = formatAppointmentDateTime(facts)
  const supportingFacts = [
    facts.providerName,
    visitLabel(facts) === "—" ? "" : visitLabel(facts),
    facts.locationName !== contextLocation ? facts.locationName : "",
  ].filter(Boolean)
  if (!appointmentTime && supportingFacts.length === 0 && !facts.patientName) {
    return (
      <p className="text-xs text-muted-foreground">
        Appointment details unavailable.
      </p>
    )
  }
  return (
    <div className="min-w-0">
      {appointmentTime && (
        <p className="text-sm font-semibold">{appointmentTime}</p>
      )}
      {supportingFacts.length > 0 && (
        <p className="mt-1 text-xs leading-5 text-muted-foreground">
          {supportingFacts.join(" · ")}
        </p>
      )}
      {facts.patientName && (
        <p className="mt-1 text-xs text-muted-foreground">
          {facts.patientName}
        </p>
      )}
    </div>
  )
}

function DetailValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 truncate font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

function aiCallException(detail: AiInteractionDetail) {
  if (detail.status === "FAILED") {
    return { label: "Failed", variant: "destructive" as const }
  }
  if (detail.appointmentOutcome === "PARTIAL") {
    return { label: "Needs review", variant: "outline" as const }
  }
  if (detail.status === "ESCALATED") {
    return { label: "Needs staff", variant: "outline" as const }
  }
  return undefined
}

function appointmentFactLine(facts: AiAppointmentFacts) {
  return [
    formatAppointmentDateTime(facts) ?? "Appointment details unavailable",
    facts.providerName,
    facts.locationName,
  ]
    .filter(Boolean)
    .join(" · ")
}

function hasAppointmentFacts(facts: AiAppointmentFacts) {
  return Object.values(facts).some(Boolean)
}

function visitLabel(facts: AiAppointmentFacts) {
  if (facts.appointmentTypeName) return facts.appointmentTypeName
  if (facts.careLane === "medical_md") return "Medical"
  if (facts.careLane === "routine_od") return "Routine vision"
  return "—"
}

function formatAppointmentDateTime(facts: AiAppointmentFacts) {
  if (facts.startDatetime) {
    const value = new Date(facts.startDatetime)
    if (!Number.isNaN(value.getTime())) {
      return new Intl.DateTimeFormat(undefined, {
        dateStyle: "long",
        timeStyle: "short",
      }).format(value)
    }
  }
  if (!facts.appointmentDate) return facts.appointmentTime
  const match = facts.appointmentDate.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  const date = match
    ? new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
    : new Date(facts.appointmentDate)
  const dateLabel = Number.isNaN(date.getTime())
    ? facts.appointmentDate
    : new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(date)
  return facts.appointmentTime
    ? `${dateLabel} · ${facts.appointmentTime}`
    : dateLabel
}

function formatDuration(startedAt: string, endedAt?: string) {
  if (!endedAt) return "In progress"
  const seconds = Math.max(
    0,
    Math.round((new Date(endedAt).getTime() - new Date(startedAt).getTime()) / 1000),
  )
  if (!Number.isFinite(seconds)) return "—"
  const minutes = Math.floor(seconds / 60)
  const remainder = seconds % 60
  if (minutes === 0) return `${remainder}s`
  return `${minutes}m ${remainder}s`
}

function ReceiptEvidence({
  label,
  value,
}: {
  label: string
  value: Record<string, unknown>
}) {
  return (
    <details className="group border-t pt-3">
      <summary className="cursor-pointer text-xs font-medium">{label}</summary>
      <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-background p-3 font-mono text-[0.6875rem] leading-5 text-muted-foreground">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  )
}

function EvidenceValue({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 break-all font-mono text-[0.6875rem]" title={value}>
        {value}
      </dd>
    </div>
  )
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value))
}
