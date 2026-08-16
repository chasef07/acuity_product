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
  ArrowRightIcon,
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
  InputGroupButton,
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
  AiOutcomeCounts,
  AiOutcomeItem,
  EngagementSummary,
  MessageThreadSummary,
  PracticeAccess,
  Task,
  TaskFolderCounts,
} from "@/lib/api/generated/types.gen"
import {
  aiCallCompletionLabel,
  appointmentOutcomeLabel,
} from "@/lib/ai-interactions"
import { appointmentFolderForAction } from "@/lib/ai-outcome-attention"
import { authClient } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import { resolveWorkspaceSearch } from "@/lib/workspace-search"
import {
  appointmentFolderForTask,
  filterTasksByCategory,
  taskCountForCategory,
  taskFolderCursor,
  type TaskCategoryFilter,
} from "@/lib/workspace-triage"

export type ConnectionState = "connecting" | "connected" | "degraded"

type AttentionSection =
  | "tasks"
  | "calls"
  | "appointments"
  | "texts"

type AppointmentSection = "bookings" | "cancellations" | "reschedules"

const taskCategoryOptions: Array<{
  value: TaskCategoryFilter
  label: string
}> = [
  { value: "all", label: "All types" },
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
  recoveryTasks: Task[]
  taskCounts: TaskFolderCounts
  aiOutcomes: AiOutcomeItem[]
  outcomeCounts: AiOutcomeCounts
  messages: MessageThreadSummary[]
  selectedTaskID: string
  selectedAIInteractionID: string
  selectedPhone: string
  search: string
  engagementError: string
  loading: boolean
  recoveryLoading: boolean
  outcomesLoading: boolean
  outcomesError: string
  outcomeNextCursor: string
  messageLoading: boolean
  nextCursor: string
  recoveryNextCursor: string
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
  onRecoveryLoadMore: () => void
  onMessageLoadMore: () => void
  onOutcomeLoadMore: () => void
}

export function TaskRail({
  discovery,
  practice,
  workspaceControl,
  locationScopeID,
  tasks,
  recoveryTasks,
  taskCounts,
  aiOutcomes,
  outcomeCounts,
  messages,
  selectedTaskID,
  selectedAIInteractionID,
  selectedPhone,
  search,
  engagementError,
  loading,
  recoveryLoading,
  outcomesLoading,
  outcomesError,
  outcomeNextCursor,
  messageLoading,
  nextCursor,
  recoveryNextCursor,
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
  onRecoveryLoadMore,
  onMessageLoadMore,
  onOutcomeLoadMore,
}: TaskRailProps) {
  const stateKey = sidebarStateKey(discovery.actor.subject, practice.id)
  const [expansion, setExpansion] = useState<{
    stateKey: string
    section: AttentionSection
  }>()
  const expanded = expansion?.stateKey === stateKey ? expansion.section : undefined
  const [appointmentExpansion, setAppointmentExpansion] = useState<{
    stateKey: string
    section: AppointmentSection
  }>()
  const expandedAppointment =
    appointmentExpansion?.stateKey === stateKey
      ? appointmentExpansion.section
      : undefined
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
  const selectedTaskCount = taskCountForCategory(taskCounts, taskCategory)
  const categorizedAIOutcomes = useMemo(
    () => categorizeAIOutcomes(aiOutcomes),
    [aiOutcomes],
  )
  const appointmentFolders = [
    {
      key: "bookings" as const,
      title: "Bookings",
      tasks: categorizedTasks.bookings,
      outcomes: categorizedAIOutcomes.bookings,
      taskCount: taskCounts.bookings,
      count: taskCounts.bookings + outcomeCounts.bookings,
    },
    {
      key: "cancellations" as const,
      title: "Cancellations",
      tasks: categorizedTasks.cancellations,
      outcomes: categorizedAIOutcomes.cancellations,
      taskCount: taskCounts.cancellations,
      count: taskCounts.cancellations + outcomeCounts.cancellations,
    },
    {
      key: "reschedules" as const,
      title: "Reschedules",
      tasks: categorizedTasks.reschedules,
      outcomes: categorizedAIOutcomes.reschedules,
      taskCount: taskCounts.reschedules,
      count: taskCounts.reschedules + outcomeCounts.reschedules,
    },
  ]
  const appointmentCount = appointmentFolders.reduce(
    (total, folder) => total + folder.count,
    0,
  )
  const recoveryRows = useMemo(
    () => aggregateRecovery(recoveryTasks),
    [recoveryTasks],
  )
  const textRows = useMemo(() => aggregateTexts(messages), [messages])
  const outcomeFolderExpanded =
    (expanded === "tasks" && taskCategory === "all") ||
    (expanded === "appointments" && Boolean(expandedAppointment))

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

  function toggle(section: AttentionSection) {
    const closing = expanded === section
    setExpansion(closing ? undefined : { stateKey, section })
    if (section === "appointments" || expanded === "appointments") {
      setAppointmentExpansion(undefined)
    }
  }

  function toggleAppointment(section: AppointmentSection) {
    setAppointmentExpansion((current) =>
      current?.stateKey === stateKey && current.section === section
        ? undefined
        : { stateKey, section },
    )
  }

  function rememberScroll() {
    writeSidebarState(stateKey, {
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
              if (resolveWorkspaceSearch(search).kind === "tasks") {
                setExpansion({ stateKey, section: "tasks" })
                setAppointmentExpansion(undefined)
              }
              onSearchSubmit()
            }}
          >
            <InputGroup className="h-8 rounded-lg border-transparent bg-sidebar-accent/45 shadow-none focus-within:border-sidebar-ring/50 focus-within:bg-background">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                ref={searchInput}
                aria-label="Search tasks, names, or phone"
                aria-invalid={Boolean(engagementError)}
                autoComplete="off"
                enterKeyHint="go"
                placeholder="Search tasks, names, or phone"
                value={search}
                onChange={(event) => onSearchChange(event.target.value)}
              />
              <InputGroupAddon align="inline-end" className="hidden md:flex">
                <span className="text-[0.6875rem] text-muted-foreground">↵</span>
              </InputGroupAddon>
              <InputGroupAddon align="inline-end" className="md:hidden">
                <InputGroupButton
                  type="submit"
                  size="icon-xs"
                  aria-label="Search"
                >
                  <ArrowRightIcon />
                </InputGroupButton>
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
            count={
              selectedTaskCount +
              (taskCategory === "all" ? outcomeCounts.tasks : 0)
            }
            expanded={expanded === "tasks"}
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
            {taskCategory === "all" &&
              categorizedAIOutcomes.tasks.map((interaction) => (
                <AIOutcomeRow
                  key={interaction.id}
                  interaction={interaction}
                  active={interaction.id === selectedAIInteractionID}
                  showOffice={showOffice}
                  onSelect={() => onAIInteractionSelect(interaction)}
                />
              ))}
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
            {!loading &&
              selectedTaskCount === 0 &&
              (taskCategory !== "all" || outcomeCounts.tasks === 0) && (
              <RailEmpty inMenu>
                {taskCategory === "all"
                  ? "No open Tasks"
                  : "No Tasks of this type"}
              </RailEmpty>
            )}
            <RailShowMore
              cursor={taskFolderCursor(
                nextCursor,
                filteredTasks.length,
                selectedTaskCount,
              )}
              loading={loading}
              onLoadMore={onLoadMore}
            />
          </AttentionGroup>

          <RecoveryGroup
            title="Missed Calls"
            empty="No missed calls"
            rows={recoveryRows}
            count={taskCounts.missedCalls}
            expanded={expanded === "calls"}
            selectedTaskID={selectedTaskID}
            showOffice={showOffice}
            onToggle={() => toggle("calls")}
            onSelect={onTaskSelect}
            cursor={recoveryNextCursor}
            loading={recoveryLoading}
            onLoadMore={onRecoveryLoadMore}
          />
          {outcomesError && (
            <p role="alert" className="px-6 py-1 text-[0.6875rem] text-destructive">
              AI appointment updates are unavailable.
            </p>
          )}
          <AttentionGroup
            title="Appointments"
            count={appointmentCount}
            expanded={expanded === "appointments"}
            onToggle={() => toggle("appointments")}
          >
            {appointmentFolders.map((folder) => (
              <AppointmentFolder
                key={folder.key}
                title={folder.title}
                tasks={folder.tasks}
                outcomes={folder.outcomes}
                count={folder.count}
                taskCount={folder.taskCount}
                expanded={expandedAppointment === folder.key}
                selectedTaskID={selectedTaskID}
                selectedAIInteractionID={selectedAIInteractionID}
                showOffice={showOffice}
                loading={outcomesLoading}
                pageLoading={loading}
                cursor={nextCursor}
                onToggle={() => toggleAppointment(folder.key)}
                onTaskSelect={onTaskSelect}
                onAIInteractionSelect={onAIInteractionSelect}
                onLoadMore={onLoadMore}
              />
            ))}
          </AttentionGroup>
          {outcomeFolderExpanded && (
            <RailLoadSentinel
              label="Loading older appointment updates"
              cursor={outcomeNextCursor}
              loading={outcomesLoading}
              onLoadMore={onOutcomeLoadMore}
            />
          )}
          <AttentionGroup
            title="Texts"
            count={textRows.length}
            expanded={expanded === "texts"}
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
            {expanded === "texts" && (
              <RailLoadSentinel
                label="Loading more Texts"
                cursor={messageNextCursor}
                loading={messageLoading}
                onLoadMore={onMessageLoadMore}
              />
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

function AppointmentFolder({
  title,
  tasks,
  outcomes,
  count,
  taskCount,
  expanded,
  selectedTaskID,
  selectedAIInteractionID,
  showOffice,
  loading,
  pageLoading,
  cursor,
  onToggle,
  onTaskSelect,
  onAIInteractionSelect,
  onLoadMore,
}: {
  title: string
  tasks: Task[]
  outcomes: AiOutcomeItem[]
  count: number
  taskCount: number
  expanded: boolean
  selectedTaskID: string
  selectedAIInteractionID: string
  showOffice: boolean
  loading: boolean
  pageLoading: boolean
  cursor: string
  onToggle: () => void
  onTaskSelect: (task: Task) => void
  onAIInteractionSelect: (interaction: AiOutcomeItem) => void
  onLoadMore: () => void
}) {
  const contentID = useId()
  return (
    <SidebarMenuItem>
      <button
        type="button"
        aria-controls={contentID}
        aria-expanded={expanded}
        className="flex h-8 w-full items-center rounded-md px-2 text-left text-xs font-medium text-sidebar-foreground/70 outline-hidden transition-colors hover:bg-sidebar-accent/55 hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
        onClick={onToggle}
      >
        <ChevronRightIcon
          aria-hidden="true"
          className={cn(
            "mr-1.5 size-3.5 shrink-0 stroke-[1.5] transition-transform motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
        />
        <span className="truncate">{title}</span>
        <span className="ml-auto text-[0.6875rem] tabular-nums text-sidebar-foreground/40">
          {count}
        </span>
      </button>
      <SidebarMenu
        id={contentID}
        hidden={!expanded}
        className="ml-3 w-auto gap-0.5 border-l border-sidebar-border/70 py-1 pl-2"
      >
        {outcomes.map((interaction) => (
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
        {!loading && count === 0 && (
          <RailEmpty inMenu>{`No ${title.toLowerCase()}`}</RailEmpty>
        )}
        <RailShowMore
          cursor={taskFolderCursor(cursor, tasks.length, taskCount)}
          loading={pageLoading}
          onLoadMore={onLoadMore}
        />
      </SidebarMenu>
    </SidebarMenuItem>
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
  count,
  expanded,
  selectedTaskID,
  showOffice,
  onToggle,
  onSelect,
  cursor,
  loading,
  onLoadMore,
}: {
  title: string
  empty: string
  rows: RecoveryRowValue[]
  count: number
  expanded: boolean
  selectedTaskID: string
  showOffice: boolean
  onToggle: () => void
  onSelect: (task: Task) => void
  cursor: string
  loading: boolean
  onLoadMore: () => void
}) {
  return (
    <AttentionGroup
      title={title}
      count={count}
      expanded={expanded}
      onToggle={onToggle}
    >
      {rows.map((row) => (
        <RecoveryRow
          key={row.task.id}
          row={row}
          active={row.task.id === selectedTaskID}
          showOffice={showOffice}
          onSelect={() => onSelect(row.task)}
        />
      ))}
      {loading && rows.length === 0 && (
        <RailLoading inMenu label="Loading missed calls" />
      )}
      {!loading && count === 0 && <RailEmpty inMenu>{empty}</RailEmpty>}
      <RailShowMore
        cursor={taskFolderCursor(cursor, rows.length, count)}
        loading={loading}
        onLoadMore={onLoadMore}
      />
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
  task: Task
  voicemailCount: number
  missedCount: number
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
            {row.voicemailCount > 0 ? "Voicemail" : "Missed call"}
            {showOffice ? ` · ${row.locationName}` : ""}
          </span>
        </span>
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
    const intent = appointmentFolderForTask(task)
    if (intent) categorized[intent].push(task)
    else categorized.general.push(task)
  }
  return categorized
}

function categorizeAIOutcomes(outcomes: AiOutcomeItem[]) {
  const categorized = {
    tasks: [] as AiOutcomeItem[],
    bookings: [] as AiOutcomeItem[],
    cancellations: [] as AiOutcomeItem[],
    reschedules: [] as AiOutcomeItem[],
  }
  for (const outcome of outcomes) {
    const folder = appointmentFolderForAction(outcome.appointmentAction)
    if (folder) categorized[folder].push(outcome)
    else categorized.tasks.push(outcome)
  }
  return categorized
}

function aggregateRecovery(tasks: Task[]): RecoveryRowValue[] {
  return tasks
    .filter(
      (task) =>
        task.origin === "MISSED_CALL_RECOVERY" ||
        task.origin === "VOICEMAIL_RECOVERY",
    )
    .map((task) => {
      const related = Math.max(1, task.relatedInteractionCount)
      return {
        phone: task.phone,
        locationID: task.locationId,
        locationName: task.locationName,
        task,
        voicemailCount: task.origin === "VOICEMAIL_RECOVERY" ? related : 0,
        missedCount: task.origin === "MISSED_CALL_RECOVERY" ? related : 0,
        latestAt: task.updatedAt,
      }
    })
    .sort((left, right) => right.latestAt.localeCompare(left.latestAt))
}

function aggregateTexts(messages: MessageThreadSummary[]): TextAttentionRow[] {
  const byPhone = new Map<string, MessageThreadSummary[]>()
  for (const thread of messages) {
    if (!thread.unread || thread.openTaskCount > 0) continue
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

function RailShowMore({
  cursor,
  loading,
  onLoadMore,
}: {
  cursor: string
  loading: boolean
  onLoadMore: () => void
}) {
  if (!cursor) return null
  return (
    <SidebarMenuItem className="px-1 py-1">
      <button
        type="button"
        className="flex h-8 w-full items-center justify-center rounded-md text-xs font-medium text-sidebar-foreground/70 transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:pointer-events-none disabled:opacity-60"
        disabled={loading}
        onClick={onLoadMore}
      >
        {loading ? <Spinner /> : "Show more Tasks"}
      </button>
    </SidebarMenuItem>
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
  scrollTop: number
}

function sidebarStateKey(userSubject: string, practiceID: string) {
  return `acuity.attentionRail.${userSubject}.${practiceID}`
}

function readSidebarState(key: string): SidebarState | undefined {
  if (typeof window === "undefined") return undefined
  try {
    const value = JSON.parse(window.localStorage.getItem(key) ?? "") as SidebarState
    return Number.isFinite(value?.scrollTop) ? value : undefined
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
