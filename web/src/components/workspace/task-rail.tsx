"use client"

import {
  type ReactNode,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  BotIcon,
  ChartNoAxesCombinedIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  LogOutIcon,
  MoonIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { AcuityMark } from "@/components/acuity-mark"
import { Badge } from "@/components/ui/badge"
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
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select"
import type {
  AccessDiscovery,
  AiOutcomeItem,
  EngagementSummary,
  MessageThreadSummary,
  PracticeAccess,
  Task,
} from "@/lib/api/generated/types.gen"
import {
  aiCallCompletionLabel,
  appointmentFolder,
  appointmentOutcomeLabel,
} from "@/lib/ai-interactions"
import { authClient } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import {
  filterTasksByCategory,
  recoveryGroupKey,
  type TaskCategoryFilter,
} from "@/lib/workspace-triage"

export type ConnectionState = "connecting" | "connected" | "degraded"

type AttentionSection =
  | "tasks"
  | "calls"
  | "bookings"
  | "cancellations"
  | "reschedules"
  | "texts"
  | "recent"

const taskCategoryOptions: Array<{
  value: TaskCategoryFilter
  label: string
}> = [
  { value: "all", label: "All" },
  { value: "billing", label: "Billing" },
  { value: "appointments", label: "Appointments" },
  { value: "documentation", label: "Documentation" },
  { value: "optical", label: "Optical" },
  { value: "medication", label: "Medication" },
  { value: "referrals", label: "Referrals" },
  { value: "other", label: "Other" },
]

type TaskRailProps = {
  discovery: AccessDiscovery
  practice: PracticeAccess
  workspaceControl: ReactNode
  locationScopeID: string
  tasks: Task[]
  aiOutcomes: AiOutcomeItem[]
  messages: MessageThreadSummary[]
  recent: EngagementSummary[]
  selectedTaskID: string
  selectedAIInteractionID: string
  selectedPhone: string
  search: string
  engagementError: string
  loading: boolean
  outcomesLoading: boolean
  outcomesError: string
  messageLoading: boolean
  nextCursor: string
  messageNextCursor: string
  connection: ConnectionState
  analyticsActive: boolean
  onSearchChange: (search: string) => void
  onSearchSubmit: () => void
  onAnalyticsSelect: () => void
  onEngagementSelect: (engagement: EngagementSummary) => void
  onAIInteractionSelect: (interaction: AiOutcomeItem) => void
  onTaskSelect: (task: Task) => void
  onLoadMore: () => void
  onMessageLoadMore: () => void
}

export function TaskRail({
  discovery,
  practice,
  workspaceControl,
  locationScopeID,
  tasks,
  aiOutcomes,
  messages,
  recent,
  selectedTaskID,
  selectedAIInteractionID,
  selectedPhone,
  search,
  engagementError,
  loading,
  outcomesLoading,
  outcomesError,
  messageLoading,
  nextCursor,
  messageNextCursor,
  connection,
  analyticsActive,
  onSearchChange,
  onSearchSubmit,
  onAnalyticsSelect,
  onEngagementSelect,
  onAIInteractionSelect,
  onTaskSelect,
  onLoadMore,
  onMessageLoadMore,
}: TaskRailProps) {
  const stateKey = sidebarStateKey(discovery.actor.subject, practice.id)
  const [expanded, setExpanded] = useState<Record<AttentionSection, boolean>>(
    () => ({
      tasks: true,
      calls: false,
      bookings: false,
      cancellations: false,
      reschedules: false,
      texts: false,
      recent: false,
      ...readSidebarState(stateKey)?.expanded,
    }),
  )
  const [taskCategory, setTaskCategory] =
    useState<TaskCategoryFilter>("all")
  const scrollContainer = useRef<HTMLDivElement | null>(null)
  const searchInput = useRef<HTMLInputElement | null>(null)
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()
  const showOffice = practice.locations.length > 1 && !locationScopeID
  const categorizedTasks = useMemo(() => categorizeTasks(tasks), [tasks])
  const filteredTasks = useMemo(
    () =>
      filterTasksByCategory(categorizedTasks.general, taskCategory),
    [categorizedTasks.general, taskCategory],
  )
  const categorizedAIOutcomes = useMemo(
    () => categorizeAIOutcomes(aiOutcomes),
    [aiOutcomes],
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
            <AcuityMark className="size-7 shrink-0" />
            <div className="min-w-0 flex-1">{workspaceControl}</div>
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
        <SidebarContent
          ref={scrollContainer}
          className="gap-1 overflow-y-auto px-2 py-2"
          onScroll={rememberScroll}
        >
          <AttentionGroup
            title="Tasks"
            count={filteredTasks.length}
            expanded={expanded.tasks}
            onToggle={() => toggle("tasks")}
          >
            <SidebarMenuItem className="px-1 py-1">
              <NativeSelect
                aria-label="Task category"
                size="sm"
                value={taskCategory}
                onChange={(event) =>
                  setTaskCategory(event.target.value as TaskCategoryFilter)
                }
              >
                {taskCategoryOptions.map((option) => (
                  <NativeSelectOption key={option.value} value={option.value}>
                    {option.label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </SidebarMenuItem>
            {filteredTasks.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                active={task.id === selectedTaskID}
                showOffice={showOffice}
                onSelect={() => onTaskSelect(task)}
              />
            ))}
            {loading && filteredTasks.length === 0 && (
              <RailLoading inMenu label="Loading tasks" />
            )}
            {!loading && filteredTasks.length === 0 && (
              <RailEmpty inMenu>
                {taskCategory === "all"
                  ? "No open Tasks"
                  : "No Tasks of this type"}
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

          <RecoveryGroup
            title="Missed Calls"
            empty="No missed calls"
            rows={recoveryRows}
            expanded={expanded.calls}
            selectedTaskID={selectedTaskID}
            showOffice={showOffice}
            onToggle={() => toggle("calls")}
            onSelect={onTaskSelect}
          />
          {outcomesError && (
            <p role="alert" className="px-6 py-1 text-[0.6875rem] text-destructive">
              AI appointment updates are unavailable.
            </p>
          )}
          <AppointmentGroup
            title="Bookings"
            tasks={categorizedTasks.bookings}
            outcomes={categorizedAIOutcomes.bookings}
            expanded={expanded.bookings}
            selectedTaskID={selectedTaskID}
            selectedAIInteractionID={selectedAIInteractionID}
            showOffice={showOffice}
            loading={outcomesLoading}
            onToggle={() => toggle("bookings")}
            onTaskSelect={onTaskSelect}
            onAIInteractionSelect={onAIInteractionSelect}
          />
          <AppointmentGroup
            title="Cancellations"
            tasks={categorizedTasks.cancellations}
            outcomes={categorizedAIOutcomes.cancellations}
            expanded={expanded.cancellations}
            selectedTaskID={selectedTaskID}
            selectedAIInteractionID={selectedAIInteractionID}
            showOffice={showOffice}
            loading={outcomesLoading}
            onToggle={() => toggle("cancellations")}
            onTaskSelect={onTaskSelect}
            onAIInteractionSelect={onAIInteractionSelect}
          />
          <AppointmentGroup
            title="Reschedules"
            tasks={categorizedTasks.reschedules}
            outcomes={categorizedAIOutcomes.reschedules}
            expanded={expanded.reschedules}
            selectedTaskID={selectedTaskID}
            selectedAIInteractionID={selectedAIInteractionID}
            showOffice={showOffice}
            loading={outcomesLoading}
            onToggle={() => toggle("reschedules")}
            onTaskSelect={onTaskSelect}
            onAIInteractionSelect={onAIInteractionSelect}
          />
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
        </SidebarContent>
        <SidebarFooter className="p-2">
          <SidebarMenu>
            {discovery.platformOperator && (
              <SidebarMenuItem>
                <SidebarMenuButton
                  isActive={analyticsActive}
                  tooltip="Analytics"
                  onClick={onAnalyticsSelect}
                >
                  <ChartNoAxesCombinedIcon />
                  <span>Analytics</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            )}
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
                onClick={() =>
                  void authClient.signOut().then((result) => {
                    if (!result.error) router.push("/sign-in")
                  })
                }
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

function AppointmentGroup({
  title,
  tasks,
  outcomes,
  expanded,
  selectedTaskID,
  selectedAIInteractionID,
  showOffice,
  loading,
  onToggle,
  onTaskSelect,
  onAIInteractionSelect,
}: {
  title: string
  tasks: Task[]
  outcomes: AiOutcomeItem[]
  expanded: boolean
  selectedTaskID: string
  selectedAIInteractionID: string
  showOffice: boolean
  loading: boolean
  onToggle: () => void
  onTaskSelect: (task: Task) => void
  onAIInteractionSelect: (interaction: AiOutcomeItem) => void
}) {
  return (
    <AttentionGroup
      title={title}
      count={tasks.length + outcomes.length}
      expanded={expanded}
      onToggle={onToggle}
    >
      {[...outcomes].reverse().map((interaction) => (
        <AIOutcomeRow
          key={interaction.id}
          interaction={interaction}
          active={interaction.id === selectedAIInteractionID}
          showOffice={showOffice}
          onSelect={() => onAIInteractionSelect(interaction)}
        />
      ))}
      {tasks.map((task) => (
        <TaskRow
          key={task.id}
          task={task}
          active={task.id === selectedTaskID}
          showOffice={showOffice}
          onSelect={() => onTaskSelect(task)}
        />
      ))}
      {loading && tasks.length === 0 && outcomes.length === 0 && (
        <RailLoading inMenu label={`Loading ${title.toLowerCase()}`} />
      )}
      {!loading && tasks.length === 0 && outcomes.length === 0 && (
        <RailEmpty inMenu>{`No ${title.toLowerCase()}`}</RailEmpty>
      )}
    </AttentionGroup>
  )
}

function AIOutcomeRow({
  interaction,
  active,
  showOffice,
  onSelect,
}: {
  interaction: AiOutcomeItem
  active: boolean
  showOffice: boolean
  onSelect: () => void
}) {
  const occurredAt =
    interaction.appointmentOccurredAt ??
    interaction.endedAt ??
    interaction.startedAt
  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        isActive={active}
        className="h-auto min-h-11 rounded-lg px-3 py-2"
        tooltip={`${formatUSPhone(interaction.phone)} · ${aiCallCompletionLabel(interaction.status)}`}
        onClick={onSelect}
      >
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate text-sm font-medium tabular-nums">
              {formatUSPhone(interaction.phone)}
            </span>
            <Badge variant="outline" className="h-4 gap-1 px-1 text-[0.6875rem]">
              <BotIcon className="size-2.5" aria-hidden="true" />
              AI
            </Badge>
          </span>
          <span className="flex min-w-0 items-center gap-1.5 text-[0.6875rem] text-muted-foreground">
            <span>{appointmentOutcomeLabel(interaction.appointmentOutcome)}</span>
            <span aria-hidden="true">·</span>
            <span>{aiCallCompletionLabel(interaction.status)}</span>
            <span aria-hidden="true">·</span>
            <time className="tabular-nums" dateTime={occurredAt}>
              {relativeTime(occurredAt)}
            </time>
            {showOffice && (
              <>
                <span aria-hidden="true">·</span>
                <span className="truncate">{interaction.locationName}</span>
              </>
            )}
          </span>
        </span>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function RecoveryGroup({
  title,
  empty,
  rows,
  expanded,
  selectedTaskID,
  showOffice,
  onToggle,
  onSelect,
}: {
  title: string
  empty: string
  rows: RecoveryRowValue[]
  expanded: boolean
  selectedTaskID: string
  showOffice: boolean
  onToggle: () => void
  onSelect: (task: Task) => void
}) {
  return (
    <AttentionGroup
      title={title}
      count={rows.length}
      expanded={expanded}
      onToggle={onToggle}
    >
      {rows.map((row) => (
        <RecoveryRow
          key={recoveryGroupKey(row.locationID, row.phone)}
          row={row}
          active={row.tasks.some((task) => task.id === selectedTaskID)}
          showOffice={showOffice}
          onSelect={() => onSelect(recoveryTask(row))}
        />
      ))}
      {rows.length === 0 && <RailEmpty inMenu>{empty}</RailEmpty>}
    </AttentionGroup>
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
            <span>{formatUSPhone(task.phone)}</span>
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
  locationID: string
  locationName: string
  tasks: Task[]
  voicemailCount: number
  missedCount: number
  oldestAt: string
  latestAt: string
}

function RecoveryRow({
  row,
  active,
  showOffice,
  onSelect,
}: {
  row: RecoveryRowValue
  active: boolean
  showOffice: boolean
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
            {formatUSPhone(row.phone)}
          </span>
          <span className="truncate text-[0.6875rem] text-muted-foreground">
            {row.missedCount > 0 ? `${row.missedCount} missed` : ""}
            {row.missedCount > 0 && row.voicemailCount > 0 ? " · " : ""}
            {row.voicemailCount > 0 ? `${row.voicemailCount} voicemail` : ""}
            {showOffice ? ` · ${row.locationName}` : ""}
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
              {formatUSPhone(row.engagement.phone)}
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
          {formatUSPhone(engagement.phone)}
        </span>
        <time className="ml-auto text-[0.6875rem] tabular-nums text-muted-foreground" dateTime={engagement.latestActivity}>
          {relativeTime(engagement.latestActivity)}
        </time>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function categorizeTasks(tasks: Task[]) {
  const categorized = {
    general: [] as Task[],
    bookings: [] as Task[],
    cancellations: [] as Task[],
    reschedules: [] as Task[],
  }
  for (const task of tasks) {
    if (
      task.origin === "MISSED_CALL_RECOVERY" ||
      task.origin === "VOICEMAIL_RECOVERY"
    ) {
      continue
    }
    const intent = appointmentIntent(task)
    if (intent) categorized[intent].push(task)
    else categorized.general.push(task)
  }
  return categorized
}

function categorizeAIOutcomes(outcomes: AiOutcomeItem[]) {
  const categorized = {
    bookings: [] as AiOutcomeItem[],
    cancellations: [] as AiOutcomeItem[],
    reschedules: [] as AiOutcomeItem[],
  }
  for (const outcome of outcomes) {
    const folder = appointmentFolder(outcome.appointmentOutcome)
    if (folder) categorized[folder].push(outcome)
  }
  return categorized
}

function appointmentIntent(
  task: Task,
): "bookings" | "cancellations" | "reschedules" | undefined {
  if (task.category !== "appointments") return undefined
  const text = `${task.title} ${task.sourceMessage ?? ""}`.toLowerCase()
  if (/\b(cancel|cancellation)\b/.test(text)) return "cancellations"
  if (/\b(reschedule|rescheduling|move appointment|change appointment)\b/.test(text)) {
    return "reschedules"
  }
  if (/\b(book|booking|schedule|new appointment|appointment request)\b/.test(text)) {
    return "bookings"
  }
  return undefined
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
    const key = recoveryGroupKey(task.locationId, task.phone)
    const existing = rows.get(key) ?? {
      phone: task.phone,
      locationID: task.locationId,
      locationName: task.locationName,
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
    rows.set(key, existing)
  }
  return [...rows.values()].sort(
    (left, right) =>
      left.oldestAt.localeCompare(right.oldestAt) ||
      right.latestAt.localeCompare(left.latestAt),
  )
}

function recoveryTask(row: RecoveryRowValue): Task {
  return [...row.tasks].sort((left, right) =>
    right.updatedAt.localeCompare(left.updatedAt),
  )[0]!
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
  expanded: Partial<Record<AttentionSection, boolean>>
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
