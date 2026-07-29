"use client"

import { useEffect, useRef } from "react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ActivityIcon,
  BotIcon,
  CheckCircle2Icon,
  LogOutIcon,
  MessageSquareIcon,
  MoonIcon,
  PhoneCallIcon,
  PlusIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInput,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import type {
  AccessDiscovery,
  CallingOffer,
  MessageThreadSummary,
  PracticeAccess,
  Task,
} from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { cn } from "@/lib/utils"

export type ConnectionState = "connecting" | "connected" | "disconnected"
export type RailMode = "tasks" | "messages"

type TaskRailProps = {
  discovery: AccessDiscovery
  practice: PracticeAccess
  practiceID: string
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
  callingOffers: CallingOffer[]
  onPracticeChange: (practiceID: string) => void
  onLocationScopeChange: (locationID: string) => void
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
  practiceID,
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
  callingOffers,
  onPracticeChange,
  onLocationScopeChange,
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
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()

  return (
    <Sidebar collapsible="offcanvas" className="border-r">
      <SidebarHeader className="gap-3 border-b px-3 py-3">
        <div className="flex items-center gap-2">
          <span className="flex size-8 items-center justify-center rounded-sm bg-primary text-primary-foreground">
            <ActivityIcon className="size-4" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1">
            <p className="truncate text-sm font-semibold">Acuity Health</p>
            <p className="text-[0.625rem] uppercase tracking-[0.18em] text-muted-foreground">
              {mode === "tasks" ? "Follow-up work" : "Patient correspondence"}
            </p>
          </div>
          <ConnectionMark state={connection} />
        </div>
        <div
          aria-label="Workspace rail"
          className="grid grid-cols-2 rounded-sm border bg-muted/30 p-0.5"
        >
          <Button
            size="sm"
            variant={mode === "tasks" ? "secondary" : "ghost"}
            className="h-7 rounded-sm text-xs"
            onClick={() => onModeChange("tasks")}
          >
            Tasks
          </Button>
          <Button
            size="sm"
            variant={mode === "messages" ? "secondary" : "ghost"}
            className="h-7 rounded-sm text-xs"
            onClick={() => onModeChange("messages")}
          >
            Messages
          </Button>
        </div>
        <div className="grid grid-cols-2 gap-2">
          <NativeSelect
            aria-label="Practice"
            value={practiceID}
            onChange={(event) => onPracticeChange(event.target.value)}
          >
            {discovery.practices.map((item) => (
              <NativeSelectOption key={item.id} value={item.id}>
                {item.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <NativeSelect
            aria-label="Location"
            value={locationScopeID}
            onChange={(event) => onLocationScopeChange(event.target.value)}
          >
            {mode === "tasks" && practice.locations.length > 1 && (
              <NativeSelectOption value="">All offices</NativeSelectOption>
            )}
            {practice.locations.map((location) => (
              <NativeSelectOption key={location.id} value={location.id}>
                {location.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
          <div className="relative">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <SidebarInput
              aria-label={mode === "tasks" ? "Search tasks" : "Search messages"}
              autoComplete="off"
              inputMode={mode === "messages" ? "tel" : undefined}
              placeholder={
                mode === "tasks" ? "Search title or phone" : "Search phone"
              }
              value={search}
              onChange={(event) => onSearchChange(event.target.value)}
              className="pl-7"
            />
          </div>
          {mode === "tasks" ? (
            <NativeSelect
              aria-label="Order tasks"
              size="sm"
              value={ordering}
              onChange={(event) =>
                onOrderingChange(event.target.value as "time" | "priority")
              }
              className="w-20"
            >
              <NativeSelectOption value="time">Time</NativeSelectOption>
              <NativeSelectOption value="priority">Priority</NativeSelectOption>
            </NativeSelect>
          ) : (
            <Button size="sm" className="h-8" onClick={onNewText}>
              <PlusIcon />
              New text
            </Button>
          )}
        </div>
      </SidebarHeader>
      <SidebarContent className="gap-0">
        {callingOffers.length > 0 && (
          <div className="flex items-center gap-2 border-b px-3 py-2 text-xs">
            <PhoneCallIcon className="size-3.5 text-primary" />
            <span className="font-medium">Incoming call</span>
            <Badge
              data-testid="calling-queue-count"
              variant="secondary"
              className="ml-auto font-mono"
            >
              {callingOffers.length}
            </Badge>
          </div>
        )}
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
      <SidebarFooter className="border-t p-2">
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
  if (tasks.length === 0) return null
  return (
    <SidebarGroup className="p-0">
      <SidebarGroupLabel className="h-8 border-b px-3 text-[0.625rem] uppercase tracking-[0.16em]">
        {label}
        <span className="ml-auto font-mono tabular-nums">{tasks.length}</span>
      </SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu className="gap-0">
          {tasks.map((task) => (
            <SidebarMenuItem key={task.id}>
              <SidebarMenuButton
                isActive={task.id === selectedTaskID}
                className={cn(
                  "h-auto min-h-16 animate-in rounded-none border-b px-3 py-2 fade-in slide-in-from-top-1 duration-200",
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
                        className="size-1.5 shrink-0 rounded-full bg-primary"
                      />
                    )}
                    {task.state === "COMPLETED" && (
                      <CheckCircle2Icon className="size-3.5 shrink-0 text-muted-foreground" />
                    )}
                    <span className="truncate text-xs font-medium">
                      {task.title}
                    </span>
                    {task.origin === "ABITA_AI" && (
                      <Badge
                        variant="outline"
                        className="h-4 gap-1 px-1 font-mono text-[0.5625rem]"
                      >
                        <BotIcon className="size-2.5" aria-hidden="true" />
                        AI
                      </Badge>
                    )}
                  </span>
                  <span className="flex items-center gap-2 font-mono text-[0.625rem] text-muted-foreground">
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
                        <span className="truncate font-sans">
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
      <SidebarGroupLabel className="h-8 border-b px-3 text-[0.625rem] uppercase tracking-[0.16em]">
        Correspondence ledger
      </SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu className="gap-0">
          {threads.map((thread) => (
            <SidebarMenuItem key={thread.id}>
              <SidebarMenuButton
                isActive={thread.id === selectedThreadID}
                className={cn(
                  "h-auto min-h-20 animate-in rounded-none border-b px-3 py-2.5 fade-in slide-in-from-top-1 duration-200",
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
                        className="size-1.5 shrink-0 rounded-full bg-primary"
                      />
                    )}
                    <MessageSquareIcon
                      className="size-3.5 shrink-0 text-muted-foreground"
                      aria-hidden="true"
                    />
                    <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
                      {formatPhone(thread.externalPhone)}
                    </span>
                    <time
                      dateTime={thread.latestActivity}
                      className="font-mono text-[0.625rem] text-muted-foreground"
                    >
                      {relativeTime(thread.latestActivity)}
                    </time>
                  </span>
                  <span className="truncate text-xs text-muted-foreground">
                    {thread.displayName
                      ? `${thread.displayName} · ${formatNameSource(thread.nameSource)}`
                      : "No sourced name"}
                  </span>
                  <span className="flex items-center gap-1.5 font-mono text-[0.625rem] text-muted-foreground">
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
    <p className="px-4 py-8 text-center text-sm text-muted-foreground">
      {children}
    </p>
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
    <span
      aria-label={
        state === "connected"
          ? "Live updates connected"
          : state === "connecting"
            ? "Connecting live updates"
            : "Live updates disconnected"
      }
      className="flex items-center gap-1.5 font-mono text-[0.625rem] text-muted-foreground"
    >
      <span
        className={cn(
          "size-2 rounded-full",
          state === "connected" && "bg-primary",
          state === "connecting" && "animate-pulse bg-muted-foreground",
          state === "disconnected" && "bg-destructive",
        )}
      />
      {state === "connected"
        ? "Live"
        : state === "connecting"
          ? "Sync"
          : "Disconnected"}
    </span>
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
