"use client"

import { type ComponentProps, type ReactNode, useId } from "react"
import { PhoneIcon, PhoneOffIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useSidebar } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useCallingNavigation } from "@/components/workspace/calling-dock"
import { projectCallingFailure } from "@/lib/calling/calling-card"
import { cn } from "@/lib/utils"

export function CallingRailControl({
  workActive,
  onOpenWork,
}: {
  workActive: boolean
  onOpenWork: () => void
}) {
  const {
    activeCall,
    availabilityPending,
    available,
    callingFailure,
    ownsSoftphone,
    callingEnabled,
    setAvailability,
  } = useCallingNavigation()
  const { isMobile, setOpen, setOpenMobile } = useSidebar()
  if (!callingEnabled && !callingFailure) return null
  const failure = callingFailure ? projectCallingFailure(callingFailure) : undefined
  const attention = failure
    ? failure.action && !workActive
      ? `${failure.title}. Fix it from Work.`
      : failure.title
    : undefined
  const state = availabilityPending
    ? "Updating availability"
    : available
      ? "Receiving calls"
      : "Not receiving calls"
  const icon = availabilityPending ? <Spinner /> : available ? <PhoneIcon /> : <PhoneOffIcon />
  if (!callingEnabled) {
    return (
      <RailButton
        label="Calls"
        tooltip={attention}
        attention={attention}
        onClick={() => {
          onOpenWork()
          if (isMobile) setOpenMobile(true)
          else setOpen(true)
        }}
      >
        {icon}
      </RailButton>
    )
  }
  return (
    <RailButton
      label="Receive calls"
      tooltip={attention ? `${state} · ${attention}` : state}
      attention={attention}
      role="switch"
      aria-checked={available}
      active={available}
      disabled={availabilityPending || (Boolean(activeCall) && ownsSoftphone)}
      onClick={() => setAvailability(!available)}
    >
      {icon}
    </RailButton>
  )
}

export function RailButton({
  label,
  tooltip,
  active = false,
  attention,
  children,
  ...props
}: Omit<ComponentProps<typeof Button>, "children"> & {
  label: string
  tooltip?: string
  active?: boolean
  attention?: string
  children: ReactNode
}) {
  const attentionID = useId()
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            aria-label={label}
            aria-current={active && props.role !== "switch" ? "page" : undefined}
            aria-describedby={attention ? attentionID : undefined}
            data-active={active}
            className="group/rail size-11 p-0 text-muted-foreground hover:bg-transparent hover:text-foreground aria-expanded:bg-transparent data-[active=true]:text-foreground dark:hover:bg-transparent"
            {...props}
          />
        }
      >
        <span className={cn(
          "relative flex size-9 items-center justify-center rounded-lg transition-colors group-hover/rail:bg-sidebar-accent [&_svg:not([class*='size-'])]:size-[18px]",
          active && props.role !== "switch" && "bg-background shadow-xs group-hover/rail:bg-background",
        )}>
          {children}
          {attention && (
            <span aria-hidden="true" className="absolute top-1 right-1 size-2 rounded-full bg-warning ring-2 ring-sidebar" />
          )}
        </span>
        {attention && <span id={attentionID} className="sr-only">{attention}</span>}
      </TooltipTrigger>
      <TooltipContent side="right">{tooltip ?? label}</TooltipContent>
    </Tooltip>
  )
}
