"use client"

import { useEffect, useId, useRef, useState } from "react"
import Image from "next/image"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  BotIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  ListTodoIcon,
  LogOutIcon,
  MessageSquareIcon,
  MoonIcon,
  PlusIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from "@/components/ui/input-group"
import { Kbd } from "@/components/ui/kbd"
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
import { Switch } from "@/components/ui/switch"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { CallingOutboundNavigation } from "@/components/workspace/calling-dock"
import type {
  AccessDiscovery,
  MessageThreadSummary,
  PracticeAccess,
  Task,
} from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { cn } from "@/lib/utils"

export type ConnectionState = "connecting" | "connected" | "degraded"
export type RailMode = "tasks" | "messages"

type TaskRailProps = {
  discovery: AccessDiscovery
  practice: PracticeAccess
  locationScopeID: string
  tasks: Task[]
  messages: MessageThreadSummary[]
  mode: RailMode
  selectedTaskID: string
  selectedThreadID: string
  search: string
  ordering: "time" | "priority"
  loading: boolean
  messageLoading: boolean
  nextCursor: string
  messageNextCursor: string
  connection: ConnectionState
  onModeChange: (mode: RailMode) => void
  onSearchChange: (search: string) => void
  onOrderingChange: (ordering: "time" | "priority") => void
  onTaskSelect: (task: Task) => void
  onThreadSelect: (thread: MessageThreadSummary) => void
  onNewText: () => void
  onLoadMore: () => void
  onMessageLoadMore: () => void
}

export function TaskRail({
  discovery,
  practice,
  locationScopeID,
  tasks,
  messages,
  mode,
  selectedTaskID,
  selectedThreadID,
  search,
  ordering,
  loading,
  messageLoading,
  nextCursor,
  messageNextCursor,
  connection,
  onModeChange,
  onSearchChange,
  onOrderingChange,
  onTaskSelect,
  onThreadSelect,
  onNewText,
  onLoadMore,
  onMessageLoadMore,
}: TaskRailProps) {
  const open = tasks.filter((task) => task.state === "OPEN")
  const completed = tasks.filter((task) => task.state === "COMPLETED")
  const showOffice = practice.locations.length > 1 && !locationScopeID
  const searchInputRef = useRef<HTMLInputElement>(null)
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()

  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") {
        return
      }
      event.preventDefault()
      searchInputRef.current?.focus()
    }
    window.addEventListener("keydown", focusSearch)
    return () => window.removeEventListener("keydown", focusSearch)
  }, [])

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
        <InputGroup>
          <InputGroupInput
            ref={searchInputRef}
            aria-label={mode === "tasks" ? "Search tasks" : "Search messages"}
            autoComplete="off"
            inputMode={mode === "messages" ? "tel" : undefined}
            placeholder="Search"
            value={search}
            onChange={(event) => onSearchChange(event.target.value)}
          />
          <InputGroupAddon>
            <SearchIcon />
          </InputGroupAddon>
          <InputGroupAddon align="inline-end">
            <Kbd>⌘K</Kbd>
          </InputGroupAddon>
        </InputGroup>
        <SidebarMenu aria-label="Workspace">
          <SidebarMenuItem
            className={cn(
              "flex items-center rounded-[calc(var(--radius-sm)+2px)] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              mode === "tasks" &&
                "bg-sidebar-accent text-sidebar-accent-foreground",
            )}
          >
            <SidebarMenuButton
              isActive={mode === "tasks"}
              className="w-auto! min-w-0 flex-1 hover:bg-transparent! data-active:bg-transparent!"
              onClick={() => onModeChange("tasks")}
            >
              <ListTodoIcon />
              <span>Tasks</span>
            </SidebarMenuButton>
            {mode === "tasks" && (
              <div className="mr-2 flex shrink-0 items-center gap-2">
                <span className="text-sm font-medium">Urgency</span>
                <Switch
                  aria-label="Urgency"
                  size="sm"
                  checked={ordering === "priority"}
                  onCheckedChange={(checked) =>
                    onOrderingChange(checked ? "priority" : "time")
                  }
                />
              </div>
            )}
          </SidebarMenuItem>
          <SidebarMenuItem
            className={cn(
              "flex items-center rounded-[calc(var(--radius-sm)+2px)] hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              mode === "messages" &&
                "bg-sidebar-accent text-sidebar-accent-foreground",
            )}
          >
            <SidebarMenuButton
              isActive={mode === "messages"}
              className="w-auto! min-w-0 flex-1 hover:bg-transparent! data-active:bg-transparent!"
              onClick={() => onModeChange("messages")}
            >
              <MessageSquareIcon />
              <span>Messages</span>
            </SidebarMenuButton>
            <Tooltip>
              <TooltipTrigger
                render={
                  <Button
                    aria-label="New message"
                    size="icon-sm"
                    variant="ghost"
                    className="mr-1 min-h-0! opacity-100 transition-opacity motion-reduce:transition-none md:opacity-0 md:group-hover/menu-item:opacity-100 md:group-focus-within/menu-item:opacity-100"
                    onClick={() => {
                      if (mode !== "messages") onModeChange("messages")
                      onNewText()
                    }}
                  >
                    <PlusIcon />
                  </Button>
                }
              />
              <TooltipContent role="tooltip" side="right">
                New message
              </TooltipContent>
            </Tooltip>
          </SidebarMenuItem>
          <CallingOutboundNavigation />
        </SidebarMenu>
      </SidebarHeader>
      <Separator />
      <SidebarContent className="gap-0">
        {mode === "tasks" ? (
          <>
            <TaskGroup
              label="Open"
              tasks={open}
              selectedTaskID={selectedTaskID}
              showOffice={showOffice}
              onTaskSelect={onTaskSelect}
            />
            <TaskGroup
              label="Completed"
              tasks={completed}
              selectedTaskID={selectedTaskID}
              showOffice={showOffice}
              onTaskSelect={onTaskSelect}
            />
            {loading && <RailLoading label="Refreshing tasks" />}
            {!loading && tasks.length === 0 && (
              <RailEmpty>No follow-up tasks</RailEmpty>
            )}
            <RailLoadSentinel
              label="Loading more tasks"
              cursor={nextCursor}
              loading={loading}
              onLoadMore={onLoadMore}
            />
          </>
        ) : (
          <>
            <MessageThreadGroup
              threads={messages}
              selectedThreadID={selectedThreadID}
              onThreadSelect={onThreadSelect}
            />
            {messageLoading && <RailLoading label="Refreshing messages" />}
            {!messageLoading && messages.length === 0 && (
              <RailEmpty>No conversations at this office</RailEmpty>
            )}
            <RailLoadSentinel
              label="Loading more conversations"
              cursor={messageNextCursor}
              loading={messageLoading}
              onLoadMore={onMessageLoadMore}
            />
          </>
        )}
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
    </Sidebar>
  )
}

function TaskGroup({
  label,
  tasks,
  selectedTaskID,
  showOffice,
  onTaskSelect,
}: {
  label: "Open" | "Completed"
  tasks: Task[]
  selectedTaskID: string
  showOffice: boolean
  onTaskSelect: (task: Task) => void
}) {
  const [expanded, setExpanded] = useState(false)
  const contentID = useId()

  if (tasks.length === 0) return null
  return (
    <SidebarGroup className="p-0">
      <button
        type="button"
        aria-controls={contentID}
        aria-expanded={expanded}
        className="group/disclosure flex h-8 w-full shrink-0 items-center px-3 text-left text-sm/5 font-medium text-sidebar-foreground/70 outline-hidden transition-colors hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
        onClick={() => setExpanded((current) => !current)}
      >
        <span>{label}</span>
        <ChevronRightIcon
          aria-hidden="true"
          className={cn(
            "ml-auto size-4 shrink-0 stroke-[1.75] transition-[opacity,transform] motion-reduce:transition-none",
            expanded
              ? "rotate-90 opacity-0 group-hover/disclosure:opacity-100 group-focus-visible/disclosure:opacity-100"
              : "opacity-100",
          )}
        />
      </button>
      <SidebarGroupContent id={contentID} hidden={!expanded}>
        <SidebarMenu className="gap-0">
          {tasks.map((task) => (
            <SidebarMenuItem key={task.id}>
              <SidebarMenuButton
                isActive={task.id === selectedTaskID}
                className={cn(
                  "h-auto min-h-16 animate-in rounded-none border-b border-sidebar-border px-3 py-2.5 fade-in slide-in-from-top-1 duration-200",
                  "transition-colors motion-reduce:animate-none motion-reduce:transition-none",
                )}
                tooltip={task.title}
                onClick={() => onTaskSelect(task)}
              >
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="flex min-w-0 items-center gap-2">
                    {task.unread && task.state === "OPEN" && (
                      <span
                        aria-label="Unread conversation"
                        className="size-1.5 shrink-0 rounded-full bg-warning"
                      />
                    )}
                    {task.state === "COMPLETED" && (
                      <CheckCircle2Icon className="size-4 shrink-0 stroke-[1.75] text-success" />
                    )}
                    <span className="truncate text-sm font-medium">
                      {task.title}
                    </span>
                    {task.origin === "ABITA_AI" && (
                      <Badge
                        variant="outline"
                        className="h-4 gap-1 px-1 text-[0.6875rem]"
                      >
                        <BotIcon className="size-2.5" aria-hidden="true" />
                        AI
                      </Badge>
                    )}
                  </span>
                  <span className="flex items-center gap-2 text-xs tabular-nums text-muted-foreground">
                    <span>{formatPhone(task.phone)}</span>
                    {task.category && (
                      <>
                        <span aria-hidden="true">·</span>
                        <span className="capitalize">{task.category}</span>
                      </>
                    )}
                    {task.origin === "ABITA_AI" && (
                      <>
                        <span aria-hidden="true">·</span>
                        <span
                          className={cn(
                            task.urgency === "high_priority" &&
                              "text-destructive",
                          )}
                        >
                          {railUrgency(task.urgency)}
                        </span>
                      </>
                    )}
                    <span aria-hidden="true">·</span>
                    <time dateTime={taskRelativeAt(task)}>
                      {relativeTime(taskRelativeAt(task))}
                    </time>
                    {showOffice && (
                      <>
                        <span aria-hidden="true">·</span>
                        <span className="truncate">
                          {task.locationName}
                        </span>
                      </>
                    )}
                  </span>
                </span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

function MessageThreadGroup({
  threads,
  selectedThreadID,
  onThreadSelect,
}: {
  threads: MessageThreadSummary[]
  selectedThreadID: string
  onThreadSelect: (thread: MessageThreadSummary) => void
}) {
  if (threads.length === 0) return null
  return (
    <SidebarGroup className="p-0">
      <SidebarGroupContent>
        <SidebarMenu className="gap-0">
          {threads.map((thread) => (
            <SidebarMenuItem key={thread.id}>
              <SidebarMenuButton
                isActive={thread.id === selectedThreadID}
                className={cn(
                  "h-auto min-h-20 animate-in rounded-none border-b border-sidebar-border px-3 py-2.5 fade-in slide-in-from-top-1 duration-200",
                  "transition-colors motion-reduce:animate-none motion-reduce:transition-none",
                )}
                tooltip={thread.externalPhone}
                onClick={() => onThreadSelect(thread)}
              >
                <span className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <span className="flex min-w-0 items-center gap-2">
                    {thread.unread && (
                      <span
                        aria-label="Unread message"
                        className="size-1.5 shrink-0 rounded-full bg-warning"
                      />
                    )}
                    <MessageSquareIcon
                      className="size-4 shrink-0 stroke-[1.75] text-muted-foreground"
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1 truncate text-sm font-medium tabular-nums">
                      {formatPhone(thread.externalPhone)}
                    </span>
                    <time
                      dateTime={thread.latestActivity}
                      className="text-xs tabular-nums text-muted-foreground"
                    >
                      {relativeTime(thread.latestActivity)}
                    </time>
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {thread.displayName
                      ? `${thread.displayName} · ${formatNameSource(thread.nameSource)}`
                      : "No sourced name"}
                  </span>
                  <span className="flex items-center gap-1.5 text-xs tabular-nums text-muted-foreground">
                    <span>
                      {thread.latestDirection === "OUTBOUND"
                        ? "Outbound"
                        : "Inbound"}
                    </span>
                    <span aria-hidden="true">·</span>
                    <span className="truncate">
                      {thread.preview || "Attachment"}
                    </span>
                    {thread.latestDirection === "OUTBOUND" && (
                      <>
                        <span aria-hidden="true">·</span>
                        <span>{thread.latestDelivery}</span>
                      </>
                    )}
                  </span>
                </span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  )
}

function RailLoading({ label }: { label: string }) {
  return (
    <div className="flex items-center gap-2 px-4 py-3 text-xs text-muted-foreground">
      <Spinner />
      {label}
    </div>
  )
}

function RailEmpty({ children }: { children: string }) {
  return (
    <Empty className="min-h-32">
      <EmptyHeader>
        <EmptyTitle>{children}</EmptyTitle>
      </EmptyHeader>
    </Empty>
  )
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
    <div
      ref={sentinel}
      aria-label={label}
      className="flex h-8 items-center justify-center text-muted-foreground"
    >
      {loading && <Spinner />}
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
      {state === "connected"
        ? "Live"
        : state === "connecting"
          ? "Sync"
          : "Updates delayed"}
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
  const days = Math.floor(hours / 24)
  return `${days}d`
}

function taskRelativeAt(task: Task) {
  return task.state === "OPEN"
    ? task.createdAt
    : (task.completedAt ?? task.updatedAt)
}

function formatPhone(phone: string) {
  const match = phone.match(/^\+1(\d{3})(\d{3})(\d{4})$/)
  if (!match) return phone
  return `(${match[1]}) ${match[2]}-${match[3]}`
}

function formatNameSource(source: string | undefined) {
  if (!source) return "source unavailable"
  return source.replaceAll("_", " ").toLowerCase()
}

function railUrgency(urgency: Task["urgency"]) {
  if (urgency === "high_priority") return "High"
  if (urgency === "non_urgent") return "Non-urgent"
  return "Normal"
}
