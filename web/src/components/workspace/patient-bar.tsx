"use client"

import { type ReactNode, useState } from "react"
import { BanIcon, CheckIcon, CopyIcon, EllipsisIcon, PanelRightIcon, PhoneCallIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useCallingNavigation } from "@/components/workspace/calling-dock"
import { useCopyState } from "@/hooks/use-copy-state"
import type { PhoneSummary } from "@/lib/api/generated/types.gen"
import { blockSender, useBlockedSender, useEngagementSummary } from "@/lib/clients/engagement"
import { patientName, phoneNames } from "@/lib/patient-work"
import { formatPhone } from "@/lib/vocabulary"
import type { EngagementSummary } from "@/lib/workspace-projection"

type Caller = Pick<PhoneSummary, "names" | "openTaskCount">

type PatientIdentityProps = {
  engagement: EngagementSummary
  practiceID: string
  route: string
  onRoute: (locationID: string) => void
  callName?: string
}

export function PatientIdentity(props: PatientIdentityProps) {
  const summary = useEngagementSummary({ practiceID: props.practiceID, phone: props.engagement.phone })
  return <PatientIdentityView {...props} caller={summary.status === "ready" ? summary.data : undefined} />
}

export function PatientIdentityView({
  engagement,
  route,
  onRoute,
  callName,
  caller,
}: PatientIdentityProps & { caller?: Caller }) {
  const names = phoneNames(caller?.names ?? [], callName)
  const [copyState, setCopyState] = useCopyState()
  const office = engagement.locations.find((location) => location.id === route)
  return (
    <div data-testid="patient-identity" className="group/patient flex min-w-0 items-center gap-x-2">
      <h1 className="truncate text-sm font-medium">
        {patientName(names.length === 1 ? names[0] : undefined, engagement.phone)}
      </h1>
      {names.length === 1 && (
        <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
          {formatPhone(engagement.phone)}
        </span>
      )}
      {names.length > 1 && (
        <span data-testid="shared-phone-names" className="min-w-0 truncate text-xs text-muted-foreground">
          Used by {names.join(", ")}
        </span>
      )}
      {engagement.locations.length > 1 ? (
        <NativeSelect
          aria-label="Sender office"
          size="sm"
          value={route}
          onChange={(event) => onRoute(event.target.value)}
        >
          <NativeSelectOption value="" disabled>
            Choose office
          </NativeSelectOption>
          {engagement.locations.map((location) => (
            <NativeSelectOption key={location.id} value={location.id}>
              {location.name}
            </NativeSelectOption>
          ))}
        </NativeSelect>
      ) : (
        office && <span className="truncate text-xs text-muted-foreground">· {office.name}</span>
      )}
      <Tooltip>
        <TooltipTrigger
          render={
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              className="text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/patient:opacity-100 group-focus-within/patient:opacity-100 focus-visible:opacity-100"
              aria-label={copyState === "copied" ? "Number copied" : "Copy phone number"}
              onClick={() => {
                void navigator.clipboard.writeText(engagement.phone).then(
                  () => setCopyState("copied"),
                  () => setCopyState("failed"),
                )
              }}
            />
          }
        >
          {copyState === "copied" ? <CheckIcon /> : <CopyIcon />}
        </TooltipTrigger>
        <TooltipContent>
          {copyState === "copied" ? "Copied" : copyState === "failed" ? "Copy failed" : "Copy number"}
        </TooltipContent>
      </Tooltip>
      <span className="sr-only" role="status">
        {copyState === "copied"
          ? "Phone number copied"
          : copyState === "failed"
            ? "Phone number could not be copied"
            : ""}
      </span>
    </div>
  )
}

type PatientActionsCalling = Pick<
  ReturnType<typeof useCallingNavigation>,
  "callingOccupied" | "outboundPending" | "ownsSoftphone" | "startOutbound"
>

type PatientActionsProps = {
  engagement: EngagementSummary
  practiceID: string
  route: string
  callAction?: ReactNode
  callError?: string
  patientTasksOpen: boolean
  onPatientTasks: () => void
}

export function PatientActions(props: PatientActionsProps) {
  const calling = useCallingNavigation()
  const summary = useEngagementSummary({ practiceID: props.practiceID, phone: props.engagement.phone })
  const blocked = useBlockedSender({ practiceID: props.practiceID, phone: props.engagement.phone })
  const [blockError, setBlockError] = useState("")
  const [blockPending, setBlockPending] = useState(false)
  const setBlocked = async (next: boolean) => {
    setBlockError("")
    setBlockPending(true)
    const outcome = await blockSender({
      practiceID: props.practiceID,
      locationID: props.route,
      phone: props.engagement.phone,
      blocked: next,
    })
    setBlockPending(false)
    if (!outcome.ok) {
      setBlockError(next
        ? "Texts from this number could not be blocked. Nothing changed. Try again."
        : "Texts from this number could not be unblocked. Try again.")
      return
    }
    blocked.retry()
  }
  return (
    <PatientActionsView
      {...props}
      callError={props.callError || blockError}
      calling={calling}
      caller={summary.status === "ready" ? summary.data : undefined}
      textsBlocked={blocked.status === "ready" ? blocked.data.blocked : undefined}
      blockPending={blockPending}
      onTextsBlocked={(next) => void setBlocked(next)}
    />
  )
}

export function PatientActionsView({
  engagement,
  route,
  callAction,
  callError: actionError = "",
  patientTasksOpen,
  onPatientTasks,
  calling,
  caller,
  textsBlocked,
  blockPending = false,
  onTextsBlocked,
}: PatientActionsProps & {
  calling: PatientActionsCalling
  caller?: Caller
  textsBlocked?: boolean
  blockPending?: boolean
  onTextsBlocked?: (blocked: boolean) => void
}) {
  const openTaskCount = caller?.openTaskCount ?? 0
  const [callError, setCallError] = useState("")
  const { callingOccupied, outboundPending, ownsSoftphone, startOutbound } = calling
  const shownError = actionError || callError
  return (
    <div className="relative flex shrink-0 items-center gap-1.5">
      {callAction ?? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          title={`Call ${formatPhone(engagement.phone)}`}
          disabled={!route || !ownsSoftphone || callingOccupied}
          onClick={() => {
            setCallError("")
            void startOutbound(route, engagement.phone).then(
              (requestError) => setCallError(requestError ?? ""),
            )
          }}
        >
          {outboundPending ? (
            <>
              <Spinner data-icon="inline-start" /> Calling…
            </>
          ) : (
            <>
              <PhoneCallIcon data-icon="inline-start" /> Call
            </>
          )}
        </Button>
      )}
      {textsBlocked && (
        <span data-testid="texts-blocked" className="flex items-center gap-1 text-xs text-muted-foreground">
          <BanIcon aria-hidden="true" className="size-3.5" /> Texts blocked
        </span>
      )}
      <Button
        type="button"
        size="sm"
        variant="ghost"
        className="text-muted-foreground aria-pressed:text-foreground"
        aria-pressed={patientTasksOpen}
        onClick={onPatientTasks}
      >
        <PanelRightIcon data-icon="inline-start" />
        {openTaskCount === 0 ? "Patient" : openTaskCount === 1 ? "1 open Task" : `${openTaskCount} open Tasks`}
      </Button>
      {onTextsBlocked && textsBlocked !== undefined && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                type="button"
                size="icon-sm"
                variant="ghost"
                className="text-muted-foreground"
                aria-label="More patient actions"
                disabled={blockPending}
                aria-busy={blockPending || undefined}
              />
            }
          >
            {blockPending ? <Spinner /> : <EllipsisIcon />}
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-64">
            <DropdownMenuItem disabled={!route} onClick={() => onTextsBlocked(!textsBlocked)}>
              <BanIcon aria-hidden="true" />
              {textsBlocked ? "Unblock texts from this number" : "Block texts from this number"}
            </DropdownMenuItem>
            {!textsBlocked && (
              <p className="px-2 pt-0.5 pb-1.5 text-xs text-muted-foreground">
                Texts STOP to them from this office and closes their open texts. Their texts are still saved but stop creating Tasks. Calls are not blocked.
              </p>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {shownError && (
        <p role="alert" className="absolute top-[calc(100%+0.5rem)] right-0 z-20 w-72 rounded-lg border bg-popover px-3 py-2 text-xs text-destructive shadow-sm">
          {shownError}
        </p>
      )}
    </div>
  )
}

export function senderRoute(engagement: EngagementSummary, chosen: string) {
  if (engagement.locations.length === 1) return engagement.locations[0]!.id
  return engagement.locations.some((location) => location.id === chosen) ? chosen : ""
}
