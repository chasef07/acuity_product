"use client"

import { useEffect, useState } from "react"
import {
  ArrowRightLeftIcon,
  CheckIcon,
  Grid2X2Icon,
  LoaderCircleIcon,
  MicIcon,
  MicOffIcon,
  PhoneCallIcon,
  PhoneOffIcon,
  RotateCcwIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  projectCallingCard,
  projectCallingFailure,
  type CallingCardCallView,
  type CallingCardFailure,
  type CallingCardFailureView,
  type CallingCardOfferView,
  type CallingDispositionOutcome,
} from "@/lib/calling/calling-card"
import type { SoftphoneRuntimeSnapshot } from "@/lib/calling/softphone-runtime"
import { cn } from "@/lib/utils"

type CallingCardProps = {
  snapshot: SoftphoneRuntimeSnapshot
  onAnswer: (callLegID: string) => void
  onDecline: (callLegID: string) => void
  onLoadTransferCandidates: () => void
  onRequestTransfer: (recipientSubject: string, handoffNote: string) => void
  onCancelTransfer: () => void
  onEnd: () => void
  onMute: () => void
  onDTMF: (digit: string) => void
  onDisposition: (outcome: CallingDispositionOutcome) => void
  onRetry: () => void
  onRecover: () => void
  onClose: () => void
}

export function CallingCard({
  snapshot,
  onAnswer,
  onDecline,
  onLoadTransferCandidates,
  onRequestTransfer,
  onCancelTransfer,
  onEnd,
  onMute,
  onDTMF,
  onDisposition,
  onRetry,
  onRecover,
  onClose,
}: CallingCardProps) {
  const [keypadCallID, setKeypadCallID] = useState("")
  const [transferCallID, setTransferCallID] = useState("")
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const interval = window.setInterval(() => setNow(Date.now()), 250)
    return () => window.clearInterval(interval)
  }, [])
  const view = projectCallingCard(snapshot, now)
  if (!view) return null
  const keypadOpen = view.kind === "call" && keypadCallID === view.callId

  return (
    <Card
      role="region"
      aria-label="Call controls"
      data-calling-card-shell={view.shell}
      className="max-h-[calc(100dvh-8rem)] gap-4 overflow-y-auto rounded-2xl shadow-lg shadow-black/5 [--card-spacing:--spacing(5)]"
    >
      {view.kind === "offers" ? (
        <IncomingOfferTray
          view={view}
          onAnswer={onAnswer}
          onDecline={onDecline}
        />
      ) : view.kind === "call" ? (
        <ActiveCallHeader view={view} />
      ) : null}

      {view.failure && (
        <CardContent>
          <CallingFailure failure={view.failure} onRecover={onRecover} />
        </CardContent>
      )}

      {view.kind === "call" && (
        <>
          <ActiveCallActions
            view={view}
            onDisposition={onDisposition}
            onRetry={onRetry}
            onClose={onClose}
          />
          <ActiveCallControls
            view={view}
            keypadOpen={keypadOpen}
            transferOpen={transferCallID === view.callId}
            onToggleTransfer={() => {
              setKeypadCallID("")
              setTransferCallID((current) => current === view.callId ? "" : view.callId)
              if (transferCallID !== view.callId) onLoadTransferCandidates()
            }}
            onEnd={onEnd}
            onMute={onMute}
            onToggleKeypad={() => {
              setTransferCallID("")
              setKeypadCallID((current) =>
                current === view.callId ? "" : view.callId,
              )
            }}
          />
          <TransferPanel
            key={view.callId}
            transfer={view.transfer}
            open={transferCallID === view.callId}
            onClose={() => setTransferCallID("")}
            onRequest={onRequestTransfer}
            onCancel={onCancelTransfer}
          />
          {keypadOpen && view.controls.slots[2].visible && (
            <Keypad disabled={view.controls.slots[2].disabled} onDTMF={onDTMF} />
          )}
        </>
      )}
    </Card>
  )
}

function ActiveCallHeader({ view }: { view: CallingCardCallView }) {
  const ended = view.phase === "ended"
  return (
    <CardHeader className="gap-2">
      <CardTitle className="min-w-0 text-xl font-semibold tracking-tight">
        {ended ? (
          <h2><span role="status" aria-label={view.status}>{view.status}</span></h2>
        ) : (
          <h2 className="truncate">{view.identity.primary}</h2>
        )}
      </CardTitle>
      {(ended || view.identity.details.length > 0) && (
        <CardDescription className="min-w-0 break-words text-sm">
          {[...(ended ? [view.identity.primary] : []), ...view.identity.details].join(" · ")}
        </CardDescription>
      )}
      {!ended && (
        <div className="flex items-center gap-2 text-sm">
          <span aria-hidden className={cn(
            "size-2 rounded-full",
            view.phase === "connected" ? "bg-success" : "bg-muted-foreground motion-safe:animate-pulse",
          )} />
          <span role="status" aria-label={view.status}>{view.status}</span>
          {view.elapsed && (
            <span aria-label={`Call duration ${view.elapsed}`} className="tabular-nums text-muted-foreground">
              · {view.elapsed}
            </span>
          )}
        </div>
      )}
    </CardHeader>
  )
}

function IncomingOfferTray({
  view,
  onAnswer,
  onDecline,
}: {
  view: CallingCardOfferView
  onAnswer: (callLegID: string) => void
  onDecline: (callLegID: string) => void
}) {
  return (
    <>
      <CardHeader>
        <CardTitle className="sr-only">Calling</CardTitle>
        <CardAction>
          <LifecycleStatus status={view.status} />
        </CardAction>
      </CardHeader>
      <CardContent>
        <ul
          aria-label={view.trayLabel}
          className="max-h-[min(24rem,calc(100vh-8rem))] divide-y overflow-y-auto"
        >
          {view.offers.map((offer) => (
            <li
              key={offer.callLegId}
              data-call-leg-id={offer.callLegId}
              className="grid grid-cols-[1fr_auto] items-center gap-x-3 gap-y-1 py-3 first:pt-0 last:pb-0"
            >
              <div className="min-w-0">
                <h3 className="truncate text-sm font-semibold">
                  {offer.identity.primary}
                </h3>
                {offer.identity.details.map((detail) => (
                  <p
                    key={detail}
                    className="truncate text-xs text-muted-foreground"
                  >
                    {detail}
                  </p>
                ))}
              </div>
              <div className="row-span-2 flex items-center gap-2">
                <Badge
                  aria-label={offer.countdownLabel}
                  variant="outline"
                  className="tabular-nums"
                >
                  {offer.countdown}
                </Badge>
                {offer.decline && (
                  <Button
                    size="sm"
                    variant="outline"
                    aria-label={offer.decline.label}
                    disabled={offer.decline.disabled}
                    onClick={() => onDecline(offer.callLegId)}
                  >
                    Decline
                  </Button>
                )}
                <Button
                  size="sm"
                  aria-label={offer.answer.label}
                  disabled={!offer.answer.eligible}
                  onClick={() => onAnswer(offer.callLegId)}
                >
                  <PhoneCallIcon />
                  Answer
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </CardContent>
    </>
  )
}

function TransferPanel({
  transfer,
  open,
  onClose,
  onRequest,
  onCancel,
}: {
  transfer: CallingCardCallView["transfer"]
  open: boolean
  onClose: () => void
  onRequest: (recipientSubject: string, handoffNote: string) => void
  onCancel: () => void
}) {
  const [recipient, setRecipient] = useState("")
  const [note, setNote] = useState("")
  const selectedRecipient =
    recipient || transfer.candidates[0]?.subject || ""

  if (transfer.active) {
    return (
      <CardContent className="border-t pt-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <p className="text-sm font-medium">{transfer.active.status}</p>
            <p className="truncate text-xs text-muted-foreground">
              You own the caller until the transfer is confirmed.
            </p>
          </div>
          {transfer.active.canCancel && (
            <Button
              size="sm"
              variant="outline"
              disabled={transfer.pending}
              onClick={onCancel}
            >
              Cancel
            </Button>
          )}
        </div>
      </CardContent>
    )
  }

  if (!open || (!transfer.canStart && !transfer.pending)) return null

  return (
    <CardContent className="space-y-3 border-t pt-3">
      {transfer.pending && transfer.candidates.length === 0 ? (
        <p className="text-sm text-muted-foreground">Loading available staff…</p>
      ) : transfer.candidates.length === 0 ? (
        <p className="text-sm text-muted-foreground">No staff are available.</p>
      ) : (
        <>
          <label className="grid gap-1 text-xs font-medium">
            Transfer to
            <select
              className="h-9 rounded-md border bg-background px-3 text-sm"
              value={selectedRecipient}
              disabled={transfer.pending}
              onChange={(event) => setRecipient(event.target.value)}
            >
              {transfer.candidates.map((candidate) => (
                <option key={candidate.subject} value={candidate.subject}>
                  {candidate.email}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-xs font-medium">
            Handoff note (optional)
            <textarea
              className="min-h-16 rounded-md border bg-background px-3 py-2 text-sm"
              maxLength={500}
              value={note}
              disabled={transfer.pending}
              onChange={(event) => setNote(event.target.value)}
            />
          </label>
        </>
      )}
      <div className="flex justify-end gap-2">
        <Button
          size="sm"
          variant="ghost"
          disabled={transfer.pending}
          onClick={onClose}
        >
          Close
        </Button>
        {transfer.candidates.length > 0 && (
          <Button
            size="sm"
            disabled={!selectedRecipient || transfer.pending}
            onClick={() => {
              onClose()
              setRecipient("")
              setNote("")
              onRequest(selectedRecipient, note)
            }}
          >
            <ArrowRightLeftIcon />
            {transfer.pending ? "Transferring…" : "Transfer call"}
          </Button>
        )}
      </div>
    </CardContent>
  )
}

function LifecycleStatus({ status }: { status: string }) {
  return (
    <Badge
      role="status"
      aria-label={status}
      variant="outline"
      className="tabular-nums"
    >
      {status}
    </Badge>
  )
}

export function CallingFailureNotice({
  failure,
  onRecover,
}: {
  failure: CallingCardFailure
  onRecover: () => void
}) {
  return (
    <CallingFailure
      failure={projectCallingFailure(failure)}
      onRecover={onRecover}
    />
  )
}

function CallingFailure({
  failure,
  onRecover,
}: {
  failure: CallingCardFailureView
  onRecover: () => void
}) {
  const handleAction = () => {
    if (failure.action?.kind === "reload-page") {
      window.location.reload()
      return
    }
    onRecover()
  }

  return (
    <Alert className="border-warning/35 bg-warning/10 px-3 py-2.5 text-foreground [&>svg]:text-warning">
      <PhoneOffIcon aria-hidden />
      <AlertTitle className="text-sm font-semibold">{failure.title}</AlertTitle>
      <AlertDescription className="space-y-2.5">
        <p>{failure.message}</p>
        {failure.action && (
          <Button
            size="sm"
            variant="outline"
            className="h-7 bg-background px-2.5 text-xs"
            onClick={handleAction}
          >
            <RotateCcwIcon />
            {failure.action.label}
          </Button>
        )}
      </AlertDescription>
    </Alert>
  )
}

function ActiveCallActions({
  view,
  onDisposition,
  onRetry,
  onClose,
}: {
  view: CallingCardCallView
  onDisposition: (outcome: CallingDispositionOutcome) => void
  onRetry: () => void
  onClose: () => void
}) {
  if (
    view.actions.dispositions.length === 0 &&
    !view.actions.retry &&
    !view.actions.close
  ) {
    return null
  }
  return (
    <CardContent className="flex flex-wrap items-center gap-2">
      {view.actions.dispositions.map((action) => (
        <Button
          key={action.outcome}
          size="sm"
          className="min-h-10 rounded-lg px-3"
          variant={action.primary ? "default" : "outline"}
          disabled={action.disabled}
          onClick={() => onDisposition(action.outcome)}
        >
          {action.primary && <CheckIcon />}
          {action.label}
        </Button>
      ))}
      {view.actions.retry && (
        <Button
          size="sm"
          className="min-h-10 rounded-lg px-3"
          disabled={view.actions.retry.disabled}
          onClick={onRetry}
        >
          <RotateCcwIcon />
          {view.actions.retry.label}
        </Button>
      )}
      {view.actions.close && (
        <Button
          size="sm"
          variant="ghost"
          className="ml-auto min-h-10 rounded-lg px-3 text-muted-foreground"
          onClick={onClose}
        >
          {view.actions.close.label}
        </Button>
      )}
    </CardContent>
  )
}

function ActiveCallControls({
  view,
  keypadOpen,
  transferOpen,
  onEnd,
  onMute,
  onToggleKeypad,
  onToggleTransfer,
}: {
  view: CallingCardCallView
  keypadOpen: boolean
  transferOpen: boolean
  onEnd: () => void
  onMute: () => void
  onToggleKeypad: () => void
  onToggleTransfer: () => void
}) {
  const [mute, end, keypad] = view.controls.slots
  if (view.phase === "ended") return null
  if (!view.controls.slots.some((control) => control.visible)) return null

  return (
    <CardFooter className="justify-center gap-3">
      {mute.visible && (
        <ControlSlot kind="mute" label={mute.label}>
          <Button
            size="icon"
            variant="secondary"
            className="size-11 rounded-full aria-pressed:bg-primary aria-pressed:text-primary-foreground"
            aria-label={mute.label}
            aria-pressed={mute.label === "Unmute"}
            disabled={mute.disabled}
            onClick={onMute}
          >
            {mute.label === "Unmute" ? <MicOffIcon /> : <MicIcon />}
          </Button>
        </ControlSlot>
      )}
      {keypad.visible && (
        <ControlSlot kind="keypad" label="Keypad">
          <Button
            size="icon"
            variant="secondary"
            className="size-11 rounded-full"
            aria-label="Keypad"
            aria-expanded={keypadOpen}
            disabled={keypad.disabled}
            onClick={onToggleKeypad}
          >
            <Grid2X2Icon />
          </Button>
        </ControlSlot>
      )}
      {view.phase === "connected" && (
        <ControlSlot kind="transfer" label="Transfer">
          <Button
            size="icon"
            variant="secondary"
            className="size-11 rounded-full"
            aria-label="Transfer"
            aria-expanded={transferOpen}
            disabled={!view.transfer.canStart || view.transfer.pending}
            onClick={onToggleTransfer}
          >
            <ArrowRightLeftIcon />
          </Button>
        </ControlSlot>
      )}
      {end.visible && (
        <ControlSlot kind="end" label={end.label}>
          <Button
            size="icon"
            variant="destructive"
            aria-label={end.label}
            className="size-11 rounded-full bg-destructive text-white hover:bg-destructive/85 hover:text-white"
            disabled={end.disabled}
            onClick={onEnd}
          >
            {end.label === "Ending…" ? (
              <LoaderCircleIcon className="size-5 motion-safe:animate-spin" />
            ) : (
              <PhoneOffIcon className="size-5" />
            )}
          </Button>
        </ControlSlot>
      )}
    </CardFooter>
  )
}

function ControlSlot({
  kind,
  label,
  children,
}: {
  kind: string
  label: string
  children: React.ReactNode
}) {
  return (
    <div data-control-slot={kind} className="flex flex-1 flex-col items-center gap-2">
      {children}
      <span className="whitespace-nowrap text-xs font-medium">{label}</span>
    </div>
  )
}

function Keypad({ disabled, onDTMF }: { disabled: boolean; onDTMF: (digit: string) => void }) {
  return (
    <CardContent>
      <div aria-label="Keypad" className="grid grid-cols-3 gap-1 border-t pt-3">
        {["1", "2", "3", "4", "5", "6", "7", "8", "9", "*", "0", "#"].map(
          (digit) => (
            <Button
              key={digit}
              type="button"
              size="sm"
              variant="outline"
              aria-label={`Send ${digit}`}
              disabled={disabled}
              onClick={() => onDTMF(digit)}
            >
              {digit}
            </Button>
          ),
        )}
      </div>
    </CardContent>
  )
}
