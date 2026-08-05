"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Image from "next/image"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ChevronDownIcon,
  ChevronRightIcon,
  Clock3Icon,
  LogOutIcon,
  MessageSquareIcon,
  MoonIcon,
  PhoneMissedIcon,
  PlusIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import type {
  AccessDiscovery,
  EngagementSummary,
  MessageThreadSummary,
  PracticeAccess,
  Task,
} from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { cn } from "@/lib/utils"

export type ConnectionState = "connecting" | "connected" | "degraded"

type TaskRailProps = {
  discovery: AccessDiscovery
  practice: PracticeAccess
  tasks: Task[]
  messages: MessageThreadSummary[]
  engagements: EngagementSummary[]
  recent: EngagementSummary[]
  pendingHandledTextThreadIDs: string[]
  selectedTaskID: string
  selectedThreadID: string
  search: string
  engagementLoading: boolean
  loading: boolean
  messageLoading: boolean
  connection: ConnectionState
  onSearchChange: (search: string) => void
  onSearchSubmit: () => void
  onEngagementSelect: (engagement: EngagementSummary) => void
  onTaskSelect: (task: Task) => void
  onThreadSelect: (thread: MessageThreadSummary) => void
  onNewText: () => void
}

type AttentionSection = "tasks" | "calls" | "texts" | "recent"

export function TaskRail({
  discovery,
  tasks,
  messages,
  engagements,
  recent,
  pendingHandledTextThreadIDs,
  practice,
  selectedTaskID,
  selectedThreadID,
  search,
  engagementLoading,
  loading,
  messageLoading,
  connection,
  onSearchChange,
  onSearchSubmit,
  onEngagementSelect,
  onTaskSelect,
  onThreadSelect,
  onNewText,
}: TaskRailProps) {
  const stateKey = sidebarStateKey(discovery.actor.subject, practice.id)
  const [searchOpen, setSearchOpen] = useState(false)
  const [expanded, setExpanded] = useState<Record<AttentionSection, boolean>>(
    () =>
      readSidebarState(stateKey)?.expanded ?? {
        tasks: true,
        calls: true,
        texts: true,
        recent: false,
      },
  )
  const scrollContainer = useRef<HTMLDivElement | null>(null)
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()
  const openTasks = tasks.filter((task) => task.state === "OPEN")
  const recoveryRows = useMemo(() => aggregateRecovery(openTasks), [openTasks])
  const textRows = messages
    .filter(
      (thread) =>
        thread.latestDirection === "INBOUND" ||
        thread.unread ||
        pendingHandledTextThreadIDs.includes(thread.id),
    )
    .sort(
      (left, right) =>
        left.latestActivity.localeCompare(right.latestActivity) ||
        right.updatedAt.localeCompare(left.updatedAt),
    )

  useEffect(() => {
    const restored = readSidebarState(stateKey)
    window.requestAnimationFrame(() => {
      scrollContainer.current?.scrollTo({ top: restored?.scrollTop ?? 0 })
    })
  }, [stateKey])

  useEffect(() => {
    writeSidebarState(stateKey, {
      expanded,
      scrollTop: scrollContainer.current?.scrollTop ?? 0,
    })
  }, [expanded, stateKey])

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") {
        return
      }
      event.preventDefault()
      setSearchOpen(true)
    }
    window.addEventListener("keydown", openSearch)
    return () => window.removeEventListener("keydown", openSearch)
  }, [])

  function toggle(section: AttentionSection) {
    setExpanded((current) => ({ ...current, [section]: !current[section] }))
  }

  function rememberScroll() {
    writeSidebarState(stateKey, {
      expanded,
      scrollTop: scrollContainer.current?.scrollTop ?? 0,
    })
  }

  return (
    <Sidebar collapsible="offcanvas">
      <SidebarHeader className="gap-2 p-2">
        <div className="flex items-center gap-2">
          <Image
            src="/acuity-health-mark.png"
            alt=""
            width={28}
            height={28}
            className="size-7 shrink-0 rounded-sm object-contain"
            priority
          />
          <p className="min-w-0 flex-1 truncate text-sm font-semibold tracking-[-0.01em]">
            Acuity Health
          </p>
          <ConnectionMark state={connection} />
        </div>
        <div className="grid grid-cols-[1fr_auto] gap-1.5">
          <Button
            variant="outline"
            className="justify-start text-muted-foreground"
            onClick={() => setSearchOpen(true)}
          >
            <SearchIcon />
            Search numbers
            <span className="ml-auto text-[0.6875rem]">⌘K</span>
          </Button>
          <Button
            aria-label="New message"
            size="icon"
            variant="outline"
            onClick={onNewText}
          >
            <PlusIcon />
          </Button>
        </div>
      </SidebarHeader>
      <Separator />
      <SidebarContent
        ref={scrollContainer}
        className="gap-0 overflow-y-auto"
        onScroll={rememberScroll}
      >
        <AttentionGroup
          title="Tasks"
          count={openTasks.length}
          expanded={expanded.tasks}
          onToggle={() => toggle("tasks")}
        >
          {openTasks.map((task) => (
            <TaskRow
              key={task.id}
              task={task}
              active={task.id === selectedTaskID}
              onSelect={() => onTaskSelect(task)}
            />
          ))}
          {loading && openTasks.length === 0 && <RailLoading />}
        </AttentionGroup>
        <AttentionGroup
          title="Missed Calls & Voicemails"
          count={recoveryRows.length}
          expanded={expanded.calls}
          onToggle={() => toggle("calls")}
        >
          {recoveryRows.map((row) => (
            <RecoveryRow
              key={row.phone}
              row={row}
              active={false}
              onSelect={() => onEngagementSelect(recoveryEngagement(row))}
            />
          ))}
        </AttentionGroup>
        <AttentionGroup
          title="Texts"
          count={textRows.length}
          expanded={expanded.texts}
          onToggle={() => toggle("texts")}
        >
          {textRows.map((thread) => (
            <TextRow
              key={thread.id}
              thread={thread}
              active={thread.id === selectedThreadID}
              onSelect={() => onThreadSelect(thread)}
            />
          ))}
          {messageLoading && textRows.length === 0 && <RailLoading />}
        </AttentionGroup>
        <AttentionGroup
          title="Recent"
          expanded={expanded.recent}
          onToggle={() => toggle("recent")}
        >
          {recent.map((engagement) => (
            <RecentRow
              key={engagement.phone}
              engagement={engagement}
              onSelect={() => onEngagementSelect(engagement)}
            />
          ))}
        </AttentionGroup>
      </SidebarContent>
      <Separator />
      <SidebarFooter className="p-2">
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip={
                resolvedTheme === "dark" ? "Use light mode" : "Use dark mode"
              }
              onClick={() =>
                setTheme(resolvedTheme === "dark" ? "light" : "dark")
              }
            >
              {resolvedTheme === "dark" ? <SunIcon /> : <MoonIcon />}
              <span>
                {resolvedTheme === "dark" ? "Light mode" : "Dark mode"}
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip="Sign out"
              onClick={() =>
                void authClient.signOut().then(() => router.push("/sign-in"))
              }
            >
              <LogOutIcon />
              <span className="truncate">{discovery.actor.email}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
      <Dialog
        open={searchOpen}
        onOpenChange={(open) => {
          setSearchOpen(open)
          if (!open) onSearchChange("")
        }}
      >
        <DialogContent className="top-[18%] translate-y-0 sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>Find a number inbox</DialogTitle>
            <DialogDescription>
              Search exact authorized communication history by phone number.
            </DialogDescription>
          </DialogHeader>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              onSearchSubmit()
            }}
          >
            <Input
              autoFocus
              aria-label="Search phone histories"
              inputMode="tel"
              placeholder="(727) 555-0100"
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
            />
          </form>
          <div className="max-h-72 overflow-y-auto border-t pt-2">
            {engagementLoading && <RailLoading />}
            {!engagementLoading && search.trim() && engagements.length === 0 && (
              <p className="px-2 py-6 text-center text-sm text-muted-foreground">
                No authorized recorded activity for that number.
              </p>
            )}
            {search.trim() && engagements.map((engagement) => (
              <Button
                key={engagement.phone}
                variant="ghost"
                className="h-auto w-full justify-start px-2 py-2.5"
                onClick={() => {
                  onEngagementSelect(engagement)
                  setSearchOpen(false)
                }}
              >
                <span className="flex min-w-0 flex-1 flex-col items-start">
                  <span className="font-medium tabular-nums">
                    {formatPhone(engagement.phone)}
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {engagement.locations.map((location) => location.name).join(" · ")}
                  </span>
                </span>
              </Button>
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </Sidebar>
  )
}

function AttentionGroup({
  title,
  count,
  expanded,
  onToggle,
  children,
}: {
  title: string
  count?: number
  expanded: boolean
  onToggle: () => void
  children: React.ReactNode
}) {
  return (
    <SidebarGroup className="border-b p-0">
      <Button
        variant="ghost"
        className="h-9 w-full justify-start rounded-none px-3 text-xs font-semibold"
        aria-expanded={expanded}
        onClick={onToggle}
      >
        {expanded ? <ChevronDownIcon /> : <ChevronRightIcon />}
        <span>{title}</span>
        {count !== undefined && (
          <span className="ml-auto tabular-nums text-muted-foreground">
            {count}
          </span>
        )}
      </Button>
      <SidebarGroupContent hidden={!expanded}>
        <SidebarMenu className="gap-0">{children}</SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

function TaskRow({
  task,
  active,
  onSelect,
}: {
  task: Task
  active: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-auto min-h-14 rounded-none border-t px-3 py-2"
        tooltip={task.title}
        onClick={onSelect}
      >
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex items-center gap-2">
            {task.unread && (
              <span aria-label="Unread conversation" className="size-1.5 rounded-full bg-warning" />
            )}
            <span className="truncate text-sm font-medium">{task.title}</span>
            {task.urgency === "high_priority" && (
              <Badge variant="destructive" className="ml-auto h-4 px-1 text-[0.625rem]">
                Urgent
              </Badge>
            )}
          </span>
          <span className="text-xs tabular-nums text-muted-foreground">
            {formatPhone(task.phone)}
          </span>
        </span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

type RecoveryRowValue = {
  phone: string
  tasks: Task[]
  voicemailCount: number
  missedCount: number
  oldestAt: string
  latestAt: string
}

function RecoveryRow({
  row,
  active,
  onSelect,
}: {
  row: RecoveryRowValue
  active: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-auto min-h-14 rounded-none border-t px-3 py-2"
        tooltip={row.phone}
        onClick={onSelect}
      >
        <PhoneMissedIcon className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="truncate text-sm font-medium tabular-nums">
            {formatPhone(row.phone)}
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {row.missedCount > 0 ? `${row.missedCount} missed` : ""}
            {row.missedCount > 0 && row.voicemailCount > 0 ? " · " : ""}
            {row.voicemailCount > 0 ? `${row.voicemailCount} voicemail` : ""}
          </span>
        </span>
        <time className="text-xs tabular-nums text-muted-foreground" dateTime={row.oldestAt}>
          {relativeTime(row.oldestAt)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function TextRow({
  thread,
  active,
  onSelect,
}: {
  thread: MessageThreadSummary
  active: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-auto min-h-16 rounded-none border-t px-3 py-2"
        tooltip={thread.externalPhone}
        onClick={onSelect}
      >
        <MessageSquareIcon className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex items-center gap-2">
            {thread.unread && (
              <span aria-label="Unread message" className="size-1.5 rounded-full bg-warning" />
            )}
            <span className="truncate text-sm font-medium tabular-nums">
              {formatPhone(thread.externalPhone)}
            </span>
          </span>
          <span className="truncate text-xs text-muted-foreground">
            {thread.preview || "Attachment"}
          </span>
        </span>
        <time className="text-xs tabular-nums text-muted-foreground" dateTime={thread.latestActivity}>
          {relativeTime(thread.latestActivity)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function RecentRow({
  engagement,
  onSelect,
}: {
  engagement: EngagementSummary
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        className="h-auto min-h-12 rounded-none border-t px-3 py-2"
        tooltip={engagement.phone}
        onClick={onSelect}
      >
        <Clock3Icon className="size-4 text-muted-foreground" />
        <span className="truncate tabular-nums">{formatPhone(engagement.phone)}</span>
        <time className="ml-auto text-xs text-muted-foreground" dateTime={engagement.latestActivity}>
          {relativeTime(engagement.latestActivity)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function aggregateRecovery(tasks: Task[]): RecoveryRowValue[] {
  const rows = new Map<string, RecoveryRowValue>()
  for (const task of tasks) {
    if (
      task.origin !== "MISSED_CALL_RECOVERY" &&
      task.origin !== "VOICEMAIL_RECOVERY"
    ) {
      continue
    }
    const existing = rows.get(task.phone) ?? {
      phone: task.phone,
      tasks: [],
      voicemailCount: 0,
      missedCount: 0,
      oldestAt: task.createdAt,
      latestAt: task.updatedAt,
    }
    existing.tasks.push(task)
    const voicemailCount = task.interactions.filter(
      (interaction) => interaction.type === "VOICEMAIL",
    ).length
    const missedCount = task.interactions.filter(
      (interaction) => interaction.type === "CALL",
    ).length
    existing.voicemailCount +=
      voicemailCount || (task.origin === "VOICEMAIL_RECOVERY" ? 1 : 0)
    existing.missedCount +=
      missedCount || (task.origin === "MISSED_CALL_RECOVERY" ? 1 : 0)
    if (task.createdAt < existing.oldestAt) existing.oldestAt = task.createdAt
    if (task.updatedAt > existing.latestAt) existing.latestAt = task.updatedAt
    rows.set(task.phone, existing)
  }
  return [...rows.values()].sort(
    (left, right) =>
      left.oldestAt.localeCompare(right.oldestAt) ||
      right.latestAt.localeCompare(left.latestAt),
  )
}

function recoveryEngagement(row: RecoveryRowValue): EngagementSummary {
  const locations = new Map<string, { id: string; name: string }>()
  for (const task of row.tasks) {
    locations.set(task.locationId, {
      id: task.locationId,
      name: task.locationName,
    })
  }
  const callerName = row.tasks.find((task) => task.callerName)?.callerName
  return {
    phone: row.phone,
    ...(callerName ? { displayName: callerName } : {}),
    locations: [...locations.values()],
    latestActivity: row.latestAt,
    openTaskCount: row.tasks.length,
    unread: row.tasks.some((task) => task.unread),
  }
}

type SidebarState = {
  expanded: Record<AttentionSection, boolean>
  scrollTop: number
}

function sidebarStateKey(userSubject: string, practiceID: string) {
  return `acuity.attentionSidebar.${userSubject}.${practiceID}`
}

function readSidebarState(key: string): SidebarState | undefined {
  try {
    const value = window.localStorage.getItem(key)
    return value ? (JSON.parse(value) as SidebarState) : undefined
  } catch {
    return undefined
  }
}

function writeSidebarState(key: string, value: SidebarState) {
  window.localStorage.setItem(key, JSON.stringify(value))
}

function RailLoading() {
  return (
    <div className="flex items-center justify-center gap-2 border-t px-3 py-3 text-xs text-muted-foreground">
      <Spinner />
      Loading
    </div>
  )
}

function ConnectionMark({ state }: { state: ConnectionState }) {
  return (
    <Badge
      aria-label={
        state === "connected"
          ? "Live updates connected"
          : state === "connecting"
            ? "Connecting live updates"
            : "Live updates delayed"
      }
      variant="outline"
      className={cn(
        state === "connected" && "border-success/30 text-success",
        state === "connecting" && "border-warning/30 text-warning",
        state === "degraded" && "border-destructive/30 text-destructive",
      )}
    >
      {state === "connected" ? "Live" : state === "connecting" ? "Sync" : "Delayed"}
    </Badge>
  )
}

function relativeTime(value: string) {
  const elapsed = Date.now() - new Date(value).getTime()
  const minutes = Math.max(0, Math.floor(elapsed / 60_000))
  if (minutes < 1) return "now"
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}
