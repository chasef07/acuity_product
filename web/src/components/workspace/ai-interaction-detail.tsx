"use client"

import { useEffect, useState } from "react"
import {
  BotIcon,
  CalendarCheck2Icon,
  PhoneForwardedIcon,
  UserRoundIcon,
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
import type { AiInteractionDetail } from "@/lib/api/generated/types.gen"
import {
  aiCallCompletionLabel,
  appointmentOutcomeLabel,
  appointmentOutcomeTitle,
  transcriptTurns,
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
            <DialogTitle className="text-base">AI call evidence</DialogTitle>
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
            Transcript and receipt-backed appointment evidence for one call.
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
  const turns = transcriptTurns(detail.transcript)
  return (
    <div className="min-h-0 overflow-y-auto">
      <section className="grid gap-3 border-b bg-muted/20 px-5 py-4 sm:grid-cols-[1fr_auto]">
        <div>
          <p className="text-sm font-medium">
            {appointmentOutcomeTitle(detail.appointmentOutcome)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {formatPhone(detail.phone)} · {detail.locationName}
          </p>
        </div>
        <time
          className="text-xs tabular-nums text-muted-foreground sm:text-right"
          dateTime={detail.startedAt}
        >
          {formatDateTime(detail.startedAt)}
        </time>
        {(detail.oldAppointmentId || detail.newAppointmentId) && (
          <dl className="grid gap-2 text-xs sm:col-span-2 sm:grid-cols-2">
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
        )}
      </section>

      <section aria-labelledby="ai-transcript-title" className="px-5 py-4">
        <div className="mb-4 flex items-center gap-2">
          <h2 id="ai-transcript-title" className="text-sm font-semibold">
            Transcript
          </h2>
          <span className="text-xs tabular-nums text-muted-foreground">
            {turns.length} {turns.length === 1 ? "turn" : "turns"}
          </span>
        </div>
        {turns.length > 0 ? (
          <ol className="space-y-4">
            {turns.map((turn) => (
              <li key={turn.id} className="grid grid-cols-[1.5rem_1fr] gap-3">
                <span className="flex size-6 items-center justify-center rounded-full border bg-background text-muted-foreground">
                  {turn.speaker === "AI" ? (
                    <BotIcon className="size-3.5" aria-hidden="true" />
                  ) : (
                    <UserRoundIcon className="size-3.5" aria-hidden="true" />
                  )}
                </span>
                <div className="min-w-0 border-l-2 border-border pl-3">
                  <p className="text-[0.6875rem] font-medium uppercase tracking-[0.08em] text-muted-foreground">
                    {turn.speaker}
                  </p>
                  <p className="mt-1 whitespace-pre-wrap text-sm leading-6">
                    {turn.text}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        ) : (
          <p className="rounded-lg border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
            Transcript is not available for this call.
          </p>
        )}
      </section>
    </div>
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
