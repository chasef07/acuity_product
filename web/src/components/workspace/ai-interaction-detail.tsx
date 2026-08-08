"use client"

import { useEffect, useState } from "react"
import {
  BotIcon,
  CalendarCheck2Icon,
  PhoneForwardedIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Spinner } from "@/components/ui/spinner"
import { portalClient } from "@/lib/api/client"
import { getAiInteraction } from "@/lib/api/generated/sdk.gen"
import type {
  AiAppointmentFacts,
  AiInteractionDetail,
} from "@/lib/api/generated/types.gen"
import {
  aiCallCompletionLabel,
  appointmentOutcomeLabel,
  appointmentOutcomeTitle,
} from "@/lib/ai-interactions"
import { getAccessToken } from "@/lib/auth-client"

export function AIInteractionDetailDialog({
  interactionID,
  onClose,
}: {
  interactionID: string
  onClose: () => void
}) {
  const [request, setRequest] = useState<{
    interactionID: string
    detail?: AiInteractionDetail
    error?: string
  }>({ interactionID: "" })
  const currentRequest =
    request.interactionID === interactionID ? request : undefined
  const detail = currentRequest?.detail
  const error = currentRequest?.error ?? ""
  const loading = Boolean(interactionID) && !currentRequest

  useEffect(() => {
    if (!interactionID) return
    const controller = new AbortController()
    void getAccessToken().then(async (token) => {
      if (controller.signal.aborted) return
      if (!token) {
        setRequest({
          interactionID,
          error: "Your session is not authorized to load this AI call.",
        })
        return
      }
      const result = await getAiInteraction({
        client: portalClient(token),
        path: { interactionId: interactionID },
        signal: controller.signal,
      }).catch(() => undefined)
      if (controller.signal.aborted) return
      if (!result?.data) {
        setRequest({
          interactionID,
          error: "This AI call could not be loaded.",
        })
        return
      }
      setRequest({ interactionID, detail: result.data })
    })
    return () => controller.abort()
  }, [interactionID])

  return (
    <Dialog
      open={Boolean(interactionID)}
      onOpenChange={(open) => !open && onClose()}
    >
      <DialogContent className="max-h-[calc(100vh-2rem)] gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader className="border-b px-5 py-4 pr-12">
          <div className="flex flex-wrap items-center gap-2">
            <DialogTitle className="text-base">AI call details</DialogTitle>
            {detail && (
              <>
                <Badge
                  variant={
                    detail.status === "ESCALATED" ? "outline" : "secondary"
                  }
                >
                  {detail.status === "ESCALATED" ? (
                    <PhoneForwardedIcon aria-hidden="true" />
                  ) : (
                    <BotIcon aria-hidden="true" />
                  )}
                  {aiCallCompletionLabel(detail.status)}
                </Badge>
                <Badge variant="outline">
                  <CalendarCheck2Icon aria-hidden="true" />
                  {appointmentOutcomeLabel(detail.appointmentOutcome)}
                </Badge>
              </>
            )}
          </div>
          <DialogDescription>
            Appointment and call details recorded by the AI receptionist.
          </DialogDescription>
        </DialogHeader>

        {loading && (
          <div className="flex min-h-64 items-center justify-center gap-2 text-muted-foreground">
            <Spinner />
            Loading AI call
          </div>
        )}
        {!loading && error && (
          <div className="p-5">
            <Alert variant="destructive">
              <AlertTitle>AI call unavailable</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          </div>
        )}
        {!loading && detail && <AIInteractionDetailView detail={detail} />}
      </DialogContent>
    </Dialog>
  )
}

function AIInteractionDetailView({ detail }: { detail: AiInteractionDetail }) {
  return (
    <div className="min-h-0 overflow-y-auto">
      <section className="border-b px-5 py-5">
        <AppointmentSummary
          facts={detail.appointment}
          label={primaryAppointmentLabel(detail.appointmentOutcome)}
          title={appointmentOutcomeTitle(detail.appointmentOutcome)}
        />
        {detail.previousAppointment &&
          hasAppointmentFacts(detail.previousAppointment) && (
            <div className="mt-3 rounded-lg border border-dashed px-4 py-3">
              <p className="text-xs font-medium text-muted-foreground">
                Previous appointment
              </p>
              <p className="mt-1 text-sm font-medium">
                {formatAppointmentDateTime(detail.previousAppointment) ??
                  "Appointment details unavailable"}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">
                {[
                  detail.previousAppointment.providerName,
                  detail.previousAppointment.locationName,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            </div>
          )}
      </section>

      {detail.summary && (
        <section className="border-b px-5 py-4">
          <h2 className="text-xs font-medium text-muted-foreground">
            Call summary
          </h2>
          <p className="mt-2 text-sm leading-6">{detail.summary}</p>
        </section>
      )}

      <section className="border-b px-5 py-4">
        <h2 className="mb-3 text-sm font-semibold">Call details</h2>
        <dl className="grid grid-cols-2 gap-x-5 gap-y-4 text-sm">
          <DetailValue label="Caller" value={formatPhone(detail.phone)} />
          <DetailValue label="Office" value={detail.locationName} />
          <DetailValue
            label="Call started"
            value={formatDateTime(detail.startedAt)}
          />
          <DetailValue
            label="Call length"
            value={formatDuration(detail.startedAt, detail.endedAt)}
          />
        </dl>
      </section>

      <section className="px-5 py-4">
        <details className="group rounded-lg border px-4 py-3">
          <summary className="cursor-pointer text-sm font-medium">
            Technical evidence
          </summary>
          <div className="mt-4 space-y-3">
            <dl className="grid gap-2 text-xs sm:grid-cols-2">
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
      </section>
    </div>
  )
}

function AppointmentSummary({
  facts,
  label,
  title,
}: {
  facts: AiAppointmentFacts
  label: string
  title: string
}) {
  const appointmentTime = formatAppointmentDateTime(facts)
  return (
    <div className="rounded-xl border bg-muted/30 p-4">
      <div className="flex items-start gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg border bg-background text-success">
          <CalendarCheck2Icon className="size-4" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <p className="text-[0.6875rem] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
            {label}
          </p>
          <h2 className="mt-1 text-lg font-semibold tracking-tight">
            {appointmentTime ?? title}
          </h2>
          {appointmentTime && (
            <p className="mt-1 text-xs text-muted-foreground">{title}</p>
          )}
        </div>
      </div>
      <dl className="mt-4 grid grid-cols-2 gap-x-5 gap-y-4 border-t pt-4 text-sm">
        <DetailValue label="Patient" value={facts.patientName ?? "—"} />
        <DetailValue label="Visit" value={visitLabel(facts)} />
        <DetailValue label="Doctor" value={facts.providerName ?? "—"} />
        <DetailValue label="Office" value={facts.locationName ?? "—"} />
      </dl>
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

function primaryAppointmentLabel(
  outcome: AiInteractionDetail["appointmentOutcome"],
) {
  if (outcome === "CANCELLATION") return "Cancelled appointment"
  if (outcome === "RESCHEDULE") return "New appointment"
  if (outcome === "BOOKING") return "Appointment"
  return "Appointment review"
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
    <details className="group rounded-lg border bg-muted/20 px-3 py-2">
      <summary className="cursor-pointer text-xs font-medium">{label}</summary>
      <pre className="mt-2 overflow-x-auto whitespace-pre-wrap break-words rounded-md bg-background p-3 font-mono text-[0.6875rem] leading-5 text-muted-foreground">
        {JSON.stringify(value, null, 2)}
      </pre>
    </details>
  )
}

function EvidenceValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-background px-3 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-0.5 truncate font-mono text-[0.6875rem]" title={value}>
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
