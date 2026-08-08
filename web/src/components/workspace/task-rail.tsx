"use client"

import { useEffect, useId, useMemo, useRef, useState } from "react"
import Image from "next/image"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  BotIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  LogOutIcon,
  MoonIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
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

type AttentionSection = "tasks" | "calls" | "texts" | "recent"

type TaskRailProps = {
  discovery: AccessDiscovery
  practice: PracticeAccess
  locationScopeID: string
  tasks: Task[]
  messages: MessageThreadSummary[]
  recent: EngagementSummary[]
  selectedTaskID: string
  selectedPhone: string
  search: string
  taskState: "OPEN" | "COMPLETED"
  engagementError: string
  loading: boolean
  messageLoading: boolean
  nextCursor: string
  messageNextCursor: string
  connection: ConnectionState
  onSearchChange: (search: string) => void
  onTaskStateChange: (state: "OPEN" | "COMPLETED") => void
  onSearchSubmit: () => void
  onEngagementSelect: (engagement: EngagementSummary) => void
  onTaskSelect: (task: Task) => void
  onLoadMore: () => void
  onMessageLoadMore: () => void
}

export function TaskRail({
  discovery,
  practice,
  locationScopeID,
  tasks,
  messages,
  recent,
  selectedTaskID,
  selectedPhone,
  search,
  taskState,
  engagementError,
  loading,
  messageLoading,
  nextCursor,
  messageNextCursor,
  connection,
  onSearchChange,
  onTaskStateChange,
  onSearchSubmit,
  onEngagementSelect,
  onTaskSelect,
  onLoadMore,
  onMessageLoadMore,
}: TaskRailProps) {
  const stateKey = sidebarStateKey(discovery.actor.subject, practice.id)
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
  const searchInput = useRef<HTMLInputElement | null>(null)
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()
  const showOffice = practice.locations.length > 1 && !locationScopeID
  const generalTasks = useMemo(
    () =>
      taskState === "COMPLETED"
        ? tasks
        : tasks.filter(
            (task) =>
              task.origin !== "MISSED_CALL_RECOVERY" &&
              task.origin !== "VOICEMAIL_RECOVERY",
          ),
    [taskState, tasks],
  )
  const recoveryRows = useMemo(() => aggregateRecovery(tasks), [tasks])
  const textRows = useMemo(() => aggregateTexts(messages), [messages])

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") {
        return
      }
      event.preventDefault()
      searchInput.current?.focus()
    }
    window.addEventListener("keydown", openSearch)
    return () => window.removeEventListener("keydown", openSearch)
  }, [])

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
    <>
      <Sidebar collapsible="offcanvas">
        <SidebarHeader className="gap-3 p-3 pb-2">
          <div className="flex items-center gap-2 px-1">
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
          <form
            onSubmit={(event) => {
              event.preventDefault()
              onSearchSubmit()
            }}
          >
            <InputGroup className="h-8 rounded-lg border-transparent bg-sidebar-accent/45 shadow-none focus-within:border-sidebar-ring/50 focus-within:bg-background">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                ref={searchInput}
                aria-label="Search phone number"
                aria-invalid={Boolean(engagementError)}
                autoComplete="off"
                inputMode="tel"
                placeholder="Search phone number"
                value={search}
                onChange={(event) => onSearchChange(event.target.value)}
              />
              <InputGroupAddon align="inline-end">
                <span className="text-[0.6875rem] text-muted-foreground">↵</span>
              </InputGroupAddon>
            </InputGroup>
            {engagementError && (
              <p role="alert" className="px-2 pt-1 text-[0.6875rem] text-destructive">
                {engagementError}
              </p>
            )}
          </form>
        </SidebarHeader>
        <div className="mx-3 grid grid-cols-2 rounded-lg bg-sidebar-accent/35 p-0.5" role="tablist" aria-label="Work state">
          {(["OPEN", "COMPLETED"] as const).map((state) => (
            <Button
              key={state}
              role="tab"
              size="sm"
              variant="ghost"
              className={cn(
                "h-7 rounded-md text-xs font-medium",
                taskState === state && "bg-background shadow-xs hover:bg-background",
              )}
              aria-selected={taskState === state}
              onClick={() => onTaskStateChange(state)}
            >
              {state === "OPEN" ? "Open" : "Completed"}
            </Button>
          ))}
        </div>
        <SidebarContent
          ref={scrollContainer}
          className="gap-1 overflow-y-auto px-2 py-2"
          onScroll={rememberScroll}
        >
          <AttentionGroup
            title={taskState === "OPEN" ? "Tasks" : "Completed Tasks"}
            count={generalTasks.length}
            expanded={expanded.tasks}
            onToggle={() => toggle("tasks")}
          >
            {generalTasks.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                active={task.id === selectedTaskID}
                showOffice={showOffice}
                onSelect={() => onTaskSelect(task)}
              />
            ))}
            {loading && generalTasks.length === 0 && (
              <RailLoading inMenu label="Loading tasks" />
            )}
            {!loading && generalTasks.length === 0 && (
              <RailEmpty inMenu>
                {taskState === "OPEN" ? "No open Tasks" : "No completed Tasks"}
              </RailEmpty>
            )}
            {expanded.tasks && (
              <RailLoadSentinel
                label="Loading more tasks"
                cursor={nextCursor}
                loading={loading}
                onLoadMore={onLoadMore}
              />
            )}
          </AttentionGroup>

          {taskState === "OPEN" && (
            <>
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
                    active={row.phone === selectedPhone}
                    onSelect={() => onEngagementSelect(recoveryEngagement(row))}
                  />
                ))}
                {!loading && recoveryRows.length === 0 && (
                  <RailEmpty inMenu>No callbacks waiting</RailEmpty>
                )}
              </AttentionGroup>
              <AttentionGroup
                title="Texts"
                count={textRows.length}
                expanded={expanded.texts}
                onToggle={() => toggle("texts")}
              >
                {textRows.map((row) => (
                  <TextRow
                    key={row.engagement.phone}
                    row={row}
                    active={row.engagement.phone === selectedPhone}
                    onSelect={() => onEngagementSelect(row.engagement)}
                  />
                ))}
                {messageLoading && textRows.length === 0 && (
                  <RailLoading inMenu label="Loading Texts" />
                )}
                {!messageLoading && textRows.length === 0 && (
                  <RailEmpty inMenu>No unread Texts</RailEmpty>
                )}
                {expanded.texts && (
                  <RailLoadSentinel
                    label="Loading more Texts"
                    cursor={messageNextCursor}
                    loading={messageLoading}
                    onLoadMore={onMessageLoadMore}
                  />
                )}
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
                    active={engagement.phone === selectedPhone}
                    onSelect={() => onEngagementSelect(engagement)}
                  />
                ))}
                {recent.length === 0 && (
                  <RailEmpty inMenu>No recent number inboxes</RailEmpty>
                )}
              </AttentionGroup>
            </>
          )}
        </SidebarContent>
        <SidebarFooter className="p-2">
          <SidebarMenu>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip={resolvedTheme === "dark" ? "Use light mode" : "Use dark mode"}
                onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
              >
                {resolvedTheme === "dark" ? <SunIcon /> : <MoonIcon />}
                <span>{resolvedTheme === "dark" ? "Light mode" : "Dark mode"}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton
                tooltip="Sign out"
                onClick={() => void authClient.signOut().then(() => router.push("/sign-in"))}
              >
                <LogOutIcon />
                <span className="truncate">{discovery.actor.email}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarFooter>
      </Sidebar>

    </>
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
  const contentID = useId()
  return (
    <SidebarGroup className="p-0">
      <button
        type="button"
        aria-controls={contentID}
        aria-expanded={expanded}
        className="group/disclosure flex h-9 w-full shrink-0 items-center rounded-lg px-2 text-left text-sm/5 font-medium text-sidebar-foreground/72 outline-hidden transition-colors hover:bg-sidebar-accent/55 hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
        onClick={onToggle}
      >
        {expanded ? (
          <FolderOpenIcon className="mr-2 size-4 shrink-0 stroke-[1.65]" />
        ) : (
          <FolderIcon className="mr-2 size-4 shrink-0 stroke-[1.65]" />
        )}
        <span className="truncate">{title}</span>
        {count !== undefined && (
          <span className="ml-auto text-[0.6875rem] tabular-nums text-sidebar-foreground/40">
            {count}
          </span>
        )}
        <ChevronRightIcon
          aria-hidden="true"
          className={cn(
            "ml-1 size-3.5 shrink-0 stroke-[1.5] text-sidebar-foreground/35 transition-transform motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
        />
      </button>
      <SidebarGroupContent id={contentID} hidden={!expanded}>
        <SidebarMenu className="mx-3 w-auto gap-0.5 border-l border-sidebar-border/70 py-1 pl-2">
          {children}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

function TaskRow({
  task,
  active,
  showOffice,
  onSelect,
}: {
  task: Task
  active: boolean
  showOffice: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-auto min-h-11 rounded-lg px-3 py-2"
        tooltip={task.title}
        onClick={onSelect}
      >
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex min-w-0 items-center gap-2">
            {task.unread && task.state === "OPEN" && (
              <span aria-label="Unread conversation" className="size-1.5 shrink-0 rounded-full bg-warning" />
            )}
            {task.state === "COMPLETED" && (
              <CheckCircle2Icon className="size-4 shrink-0 stroke-[1.75] text-success" />
            )}
            <span className="truncate text-sm font-medium">{task.title}</span>
            {task.origin === "ABITA_AI" && (
              <Badge variant="outline" className="h-4 gap-1 px-1 text-[0.6875rem]">
                <BotIcon className="size-2.5" aria-hidden="true" />
                AI
              </Badge>
            )}
          </span>
          <span className="flex min-w-0 items-center gap-1.5 text-[0.6875rem] tabular-nums text-muted-foreground">
            <span>{formatPhone(task.phone)}</span>
            <span aria-hidden="true">·</span>
            <time dateTime={taskRelativeAt(task)}>{relativeTime(taskRelativeAt(task))}</time>
            {showOffice && (
              <>
                <span aria-hidden="true">·</span>
                <span className="truncate">{task.locationName}</span>
              </>
            )}
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
        className="h-auto min-h-11 rounded-lg px-3 py-2"
        tooltip={row.phone}
        onClick={onSelect}
      >
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="truncate text-sm font-medium tabular-nums">
            {formatPhone(row.phone)}
          </span>
          <span className="truncate text-[0.6875rem] text-muted-foreground">
            {row.missedCount > 0 ? `${row.missedCount} missed` : ""}
            {row.missedCount > 0 && row.voicemailCount > 0 ? " · " : ""}
            {row.voicemailCount > 0 ? `${row.voicemailCount} voicemail` : ""}
          </span>
        </span>
        <time className="text-[0.6875rem] tabular-nums text-muted-foreground" dateTime={row.oldestAt}>
          {relativeTime(row.oldestAt)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

type TextAttentionRow = {
  engagement: EngagementSummary
  previewThread: MessageThreadSummary
}

function TextRow({
  row,
  active,
  onSelect,
}: {
  row: TextAttentionRow
  active: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        data-testid="text-attention-row"
        className="h-auto min-h-11 rounded-lg px-3 py-2"
        tooltip={row.engagement.phone}
        onClick={onSelect}
      >
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex min-w-0 items-center gap-2">
            {row.engagement.unread && (
              <span aria-label="Unread message" className="size-1.5 rounded-full bg-warning" />
            )}
            <span className="truncate text-sm font-medium tabular-nums">
              {formatPhone(row.engagement.phone)}
            </span>
          </span>
          <span className="truncate text-[0.6875rem] text-muted-foreground">
            {row.previewThread.preview || "Attachment"}
          </span>
        </span>
        <time className="text-[0.6875rem] tabular-nums text-muted-foreground" dateTime={row.engagement.latestActivity}>
          {relativeTime(row.engagement.latestActivity)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function RecentRow({
  engagement,
  active,
  onSelect,
}: {
  engagement: EngagementSummary
  active: boolean
  onSelect: () => void
}) {
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-8 rounded-lg px-3"
        tooltip={engagement.phone}
        onClick={onSelect}
      >
        <span className="truncate text-sm font-medium tabular-nums">
          {formatPhone(engagement.phone)}
        </span>
        <time className="ml-auto text-[0.6875rem] tabular-nums text-muted-foreground" dateTime={engagement.latestActivity}>
          {relativeTime(engagement.latestActivity)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function aggregateRecovery(tasks: Task[]): RecoveryRowValue[] {
  const rows = new Map<string, RecoveryRowValue>()
  for (const task of tasks) {
    if (task.origin !== "MISSED_CALL_RECOVERY" && task.origin !== "VOICEMAIL_RECOVERY") {
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
    const related = Math.max(1, task.relatedInteractionCount)
    if (task.origin === "VOICEMAIL_RECOVERY") existing.voicemailCount += related
    else existing.missedCount += related
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
  const newest = [...row.tasks].sort((left, right) => right.updatedAt.localeCompare(left.updatedAt))[0]!
  return {
    phone: row.phone,
    ...(newest.callerName ? { displayName: newest.callerName } : {}),
    locations: [
      ...new Map(row.tasks.map((task) => [task.locationId, { id: task.locationId, name: task.locationName }])).values(),
    ],
    latestActivity: row.latestAt,
    openTaskCount: row.tasks.length,
    unread: row.tasks.some((task) => task.unread),
  }
}

function aggregateTexts(messages: MessageThreadSummary[]): TextAttentionRow[] {
  const byPhone = new Map<string, MessageThreadSummary[]>()
  for (const thread of messages) {
    if (!thread.unread) continue
    byPhone.set(thread.externalPhone, [...(byPhone.get(thread.externalPhone) ?? []), thread])
  }
  return [...byPhone.entries()]
    .map(([phone, threads]) => {
      const newest = [...threads].sort((left, right) => right.latestActivity.localeCompare(left.latestActivity))[0]!
      return {
        previewThread: newest,
        engagement: {
          phone,
          ...(newest.displayName ? { displayName: newest.displayName } : {}),
          locations: [
            ...new Map(threads.map((thread) => [thread.locationId, { id: thread.locationId, name: thread.locationName }])).values(),
          ],
          latestActivity: newest.latestActivity,
          openTaskCount: 0,
          unread: true,
        },
      }
    })
    .sort((left, right) => left.engagement.latestActivity.localeCompare(right.engagement.latestActivity))
}

function RailLoading({ label, inMenu = false }: { label: string; inMenu?: boolean }) {
  const className = "flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground"
  if (inMenu) {
    return (
      <SidebarMenuItem className={className}>
        <Spinner />
        {label}
      </SidebarMenuItem>
    )
  }
  return <div className={className}><Spinner />{label}</div>
}

function RailEmpty({ children, inMenu = false }: { children: string; inMenu?: boolean }) {
  const className = "px-3 py-2 text-xs text-muted-foreground"
  if (inMenu) {
    return <SidebarMenuItem className={className}>{children}</SidebarMenuItem>
  }
  return <p className={className}>{children}</p>
}

function RailLoadSentinel({
  label,
  cursor,
  loading,
  onLoadMore,
}: {
  label: string
  cursor: string
  loading: boolean
  onLoadMore: () => void
}) {
  const sentinel = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    const element = sentinel.current
    if (!element || !cursor || loading) return
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((entry) => entry.isIntersecting)) onLoadMore()
      },
      { rootMargin: "160px 0px" },
    )
    observer.observe(element)
    return () => observer.disconnect()
  }, [cursor, loading, onLoadMore])
  if (!cursor) return null
  return (
    <div ref={sentinel} aria-label={label} className="flex h-8 items-center justify-center text-muted-foreground">
      {loading && <Spinner />}
    </div>
  )
}

function ConnectionMark({ state }: { state: ConnectionState }) {
  return (
    <span
      aria-label={
        state === "connected"
          ? "Live updates connected"
          : state === "connecting"
            ? "Connecting live updates"
            : "Live updates delayed"
      }
      className={cn(
        "size-2 shrink-0 rounded-full",
        state === "connected" && "bg-success",
        state === "connecting" && "bg-warning",
        state === "degraded" && "bg-destructive",
      )}
    />
  )
}

type SidebarState = {
  expanded: Record<AttentionSection, boolean>
  scrollTop: number
}

function sidebarStateKey(userSubject: string, practiceID: string) {
  return `acuity.attentionRail.${userSubject}.${practiceID}`
}

function readSidebarState(key: string): SidebarState | undefined {
  if (typeof window === "undefined") return undefined
  try {
    const value = JSON.parse(window.localStorage.getItem(key) ?? "") as SidebarState
    return value?.expanded ? value : undefined
  } catch {
    return undefined
  }
}

function writeSidebarState(key: string, value: SidebarState) {
  window.localStorage.setItem(key, JSON.stringify(value))
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

function taskRelativeAt(task: Task) {
  return task.state === "OPEN" ? task.createdAt : (task.completedAt ?? task.updatedAt)
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}
