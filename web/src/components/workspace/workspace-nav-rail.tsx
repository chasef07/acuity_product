"use client"

import type { ReactNode } from "react"
import Image from "next/image"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ActivityIcon,
  ChartNoAxesCombinedIcon,
  HouseIcon,
  LogOutIcon,
  MonitorIcon,
  MoonIcon,
  SparklesIcon,
  SunIcon,
} from "lucide-react"

import { AcuityMark } from "@/components/acuity-mark"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useSidebar } from "@/components/ui/sidebar"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { workspaceFolders } from "@/components/workspace/workspace-rail"
import { authClient } from "@/lib/auth-client"
import { canViewPracticeAnalytics } from "@/lib/booking-analytics"
import type {
  WorkspaceProjectionIntent,
  WorkspaceProjectionState,
} from "@/lib/workspace-projection"

const practiceLogos: Record<string, string> = {
  "Abita Eye Group": "/practice-logos/abita-eye-group.png",
}

export function WorkspaceNavRail({
  projection,
  availabilityControl,
  onIntent,
}: {
  projection: WorkspaceProjectionState
  availabilityControl?: ReactNode
  onIntent: (intent: WorkspaceProjectionIntent) => void
}) {
  const { isMobile, open, openMobile, setOpen, setOpenMobile, toggleSidebar } = useSidebar()
  const discovery = projection.discovery!
  const view = projection.selection.view
  const workActive = view === "engagement" || view === "none"
  const panelOpen = isMobile ? openMobile : open
  const openCount = workspaceFolders(projection).reduce((total, folder) => total + folder.count, 0)
  const practiceName = discovery.practices.find((practice) => practice.id === projection.scope.practiceID)?.name

  function selectWithSidebar(active: boolean, intent: WorkspaceProjectionIntent) {
    if (active) {
      toggleSidebar()
      return
    }
    onIntent(intent)
    if (isMobile) setOpenMobile(true)
    else setOpen(true)
  }

  return (
    <nav
      aria-label="Workspace"
      className="sticky top-0 flex h-svh w-16 shrink-0 flex-col items-center gap-3 py-3"
    >
      <RailButton
        label="Home"
        hint={`${openCount} open`}
        active={workActive}
        expanded={workActive && panelOpen}
        onClick={() => selectWithSidebar(workActive, { type: "select-work" })}
      >
        <HouseIcon />
      </RailButton>
      <RailButton
        label="Manage agent"
        active={view === "manage-agent"}
        expanded={view === "manage-agent" && panelOpen}
        onClick={() => selectWithSidebar(view === "manage-agent", { type: "select-manage-agent" })}
      >
        <SparklesIcon />
      </RailButton>
      {canViewPracticeAnalytics(discovery, projection.scope.practiceID) && (
        <RailButton
          label="Analytics"
          active={view === "analytics"}
          onClick={() => onIntent({ type: "select-analytics" })}
        >
          <ChartNoAxesCombinedIcon />
        </RailButton>
      )}
      {discovery.platformOperator && (
        <RailButton
          label="AI diagnostics"
          active={view === "operator-analytics"}
          onClick={() => onIntent({ type: "select-operator-analytics" })}
        >
          <ActivityIcon />
        </RailButton>
      )}
      {availabilityControl}
      <div className="mt-auto">
        <AccountMenu
          email={discovery.actor.email}
          platformOperator={discovery.platformOperator}
          logo={practiceName ? practiceLogos[practiceName] : undefined}
        />
      </div>
    </nav>
  )
}

function RailButton({
  label,
  hint,
  active,
  expanded,
  onClick,
  children,
}: {
  label: string
  hint?: string
  active: boolean
  expanded?: boolean
  onClick: () => void
  children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label={label}
            aria-current={active ? "page" : undefined}
            aria-expanded={expanded}
            data-active={active}
            className="size-11 text-muted-foreground hover:bg-sidebar-accent hover:text-foreground data-[active=true]:bg-background data-[active=true]:text-foreground data-[active=true]:shadow-xs [&_svg]:stroke-[1.75]"
            onClick={onClick}
          />
        }
      >
        {children}
      </TooltipTrigger>
      <TooltipContent side="right">{hint ? `${label} · ${hint}` : label}</TooltipContent>
    </Tooltip>
  )
}

function AccountMenu({
  email,
  platformOperator,
  logo,
}: {
  email: string
  platformOperator: boolean
  logo?: string
}) {
  const router = useRouter()
  const { setTheme, theme } = useTheme()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-11 rounded-full hover:bg-sidebar-accent" aria-label="Account menu" />}
      >
        {platformOperator ? (
          <span aria-hidden="true" className="flex size-8 items-center justify-center rounded-full bg-background text-foreground ring-1 ring-sidebar-border">
            <AcuityMark className="size-5" />
          </span>
        ) : logo ? (
          <Image src={logo} alt="" width={128} height={128} className="size-8 rounded-full bg-white ring-1 ring-sidebar-border" />
        ) : (
          <span aria-hidden="true" className="flex size-8 items-center justify-center rounded-full bg-background text-sm font-medium text-foreground ring-1 ring-sidebar-border">
            {email.charAt(0).toUpperCase()}
          </span>
        )}
        <span className="sr-only">{email}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent side="right" align="end" className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Signed in as</DropdownMenuLabel>
          <p className="truncate px-2 pb-2 text-sm" title={email}>{email}</p>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <div className="flex items-center justify-between gap-4 px-2 py-2">
          <span className="text-xs text-muted-foreground">Theme</span>
          <ToggleGroup aria-label="Theme" variant="outline" size="sm" spacing={0}
            value={[theme ?? "system"]}
            onValueChange={(values) => { if (values[0]) setTheme(values[0]) }}>
            <ToggleGroupItem value="light" aria-label="Light" title="Light"><SunIcon /></ToggleGroupItem>
            <ToggleGroupItem value="dark" aria-label="Dark" title="Dark"><MoonIcon /></ToggleGroupItem>
            <ToggleGroupItem value="system" aria-label="System" title="System"><MonitorIcon /></ToggleGroupItem>
          </ToggleGroup>
        </div>
        <DropdownMenuSeparator />
        <DropdownMenuGroup>
          <DropdownMenuItem onClick={() => void authClient.signOut().then((result) => {
            if (!result.error) router.push("/sign-in")
          })}>
            <span>Sign out</span><LogOutIcon className="ml-auto" />
          </DropdownMenuItem>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
