"use client"

import { useEffect, useState } from "react"
import {
  CheckIcon,
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
  type CallingCardCallView,
  type CallingCardFailureView,
  type CallingCardOfferView,
  type CallingDispositionOutcome,
} from "@/lib/calling/calling-card"
import type { SoftphoneRuntimeSnapshot } from "@/lib/calling/softphone-runtime"

type CallingCardProps = {
  snapshot: SoftphoneRuntimeSnapshot
  onAnswer: (callLegID: string) => void
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
  onEnd,
  onMute,
  onDTMF,
  onDisposition,
  onRetry,
  onRecover,
  onClose,
}: CallingCardProps) {
  const [keypadCallID, setKeypadCallID] = useState("")
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
      size="sm"
    >
      {view.kind === "offers" ? (
        <IncomingOfferTray view={view} onAnswer={onAnswer} />
      ) : view.kind === "call" ? (
        <ActiveCallHeader view={view} />
      ) : null}

      {view.failure && (
        <CallingFailure failure={view.failure} onRecover={onRecover} />
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
            onEnd={onEnd}
            onMute={onMute}
            onToggleKeypad={() =>
              setKeypadCallID((current) =>
                current === view.callId ? "" : view.callId,
              )
            }
          />
          {keypadOpen && view.controls.slots[2].visible && (
            <Keypad onDTMF={onDTMF} />
          )}
        </>
      )}
    </Card>
  )
}

function ActiveCallHeader({ view }: { view: CallingCardCallView }) {
  return (
    <CardHeader>
      <CardTitle className="min-w-0 text-2xl font-semibold tracking-tight">
        <h2 className="truncate">{view.identity.primary}</h2>
      </CardTitle>
      {view.identity.details.length > 0 && (
        <CardDescription className="flex min-w-0 flex-col">
          {view.identity.details.map((detail) => (
            <span key={detail} className="truncate">
              {detail}
            </span>
          ))}
        </CardDescription>
      )}
      <CardAction>
        <LifecycleStatus status={view.status} />
      </CardAction>
    </CardHeader>
  )
}

function IncomingOfferTray({
  view,
  onAnswer,
}: {
  view: CallingCardOfferView
  onAnswer: (callLegID: string) => void
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

function CallingFailure({
  failure,
  onRecover,
}: {
  failure: CallingCardFailureView
  onRecover: () => void
}) {
  return (
    <CardContent>
      <Alert variant="destructive">
        <AlertTitle>{failure.title}</AlertTitle>
        <AlertDescription className="space-y-3">
          <p>{failure.message}</p>
          {failure.action && (
            <Button size="sm" variant="outline" onClick={onRecover}>
              {failure.action.label}
            </Button>
          )}
        </AlertDescription>
      </Alert>
    </CardContent>
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
    <CardContent className="flex flex-wrap gap-2">
      {view.actions.dispositions.map((action) => (
        <Button
          key={action.outcome}
          size="sm"
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
          variant="outline"
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
          disabled={view.actions.close.disabled}
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
  onEnd,
  onMute,
  onToggleKeypad,
}: {
  view: CallingCardCallView
  keypadOpen: boolean
  onEnd: () => void
  onMute: () => void
  onToggleKeypad: () => void
}) {
  const [mute, end, keypad] = view.controls.slots
  return (
    <CardFooter className="grid grid-cols-3 items-start gap-3">
      <ControlSlot control={mute}>
        <Button
          size="icon"
          variant="outline"
          aria-label={mute.label}
          disabled={mute.disabled}
          onClick={onMute}
        >
          {mute.label === "Unmute" ? <MicOffIcon /> : <MicIcon />}
        </Button>
      </ControlSlot>
      <ControlSlot control={end}>
        <Button
          size="icon"
          variant="destructive"
          aria-label={end.label === "Ending…" ? "Ending" : end.label}
          className="size-12 rounded-full bg-destructive text-background hover:bg-destructive/85 hover:text-background"
          disabled={end.disabled}
          onClick={onEnd}
        >
          <PhoneOffIcon className="size-5" />
        </Button>
      </ControlSlot>
      <ControlSlot control={keypad}>
        <Button
          size="icon"
          variant="outline"
          aria-label={keypad.label}
          aria-expanded={keypadOpen}
          disabled={keypad.disabled}
          onClick={onToggleKeypad}
        >
          <span aria-hidden className="font-mono tracking-widest">
            ···
          </span>
        </Button>
      </ControlSlot>
    </CardFooter>
  )
}

function ControlSlot({
  control,
  children,
}: {
  control: CallingCardCallView["controls"]["slots"][number]
  children: React.ReactNode
}) {
  return (
    <div
      data-control-slot={control.kind}
      className="flex min-h-16 flex-col items-center gap-1"
    >
      {control.visible ? (
        <>
          {children}
          <span className="text-xs font-medium">{control.label}</span>
        </>
      ) : (
        <div aria-hidden className="invisible size-12" />
      )}
    </div>
  )
}

function Keypad({ onDTMF }: { onDTMF: (digit: string) => void }) {
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
