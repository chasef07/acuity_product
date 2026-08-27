"use client"

import {
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ArrowRightIcon,
  Building2Icon,
  ChartNoAxesCombinedIcon,
  CheckIcon,
  CheckCircle2Icon,
  ChevronRightIcon,
  FolderIcon,
  FolderOpenIcon,
  ListFilterIcon,
  LogOutIcon,
  MonitorIcon,
  MoonIcon,
  PhoneIcon,
  SearchIcon,
  SunMoonIcon,
  SunIcon,
} from "lucide-react"

import { AcuityMark } from "@/components/acuity-mark"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
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
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { portalClient } from "@/lib/api/client"
import { completeTask } from "@/lib/api/generated/sdk.gen"
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
  appointmentOutcomeTitle,
} from "@/lib/ai-interactions"
import {
  appointmentOutcomeFolderKeys,
  appointmentOutcomeFolders,
  categorizeAIOutcomes,
  type AppointmentOutcomeCursors,
  type AppointmentOutcomeFolder,
} from "@/lib/ai-outcome-attention"
import { authClient, getAccessTokenResult } from "@/lib/auth-client"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import { newestFirst } from "@/lib/workspace-ordering"
import { resolveWorkspaceSearch } from "@/lib/workspace-search"
import {
  filterTasksByCategory,
  filterTaskQueue,
  sortRecoveryQueue,
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

type AppointmentSection = AppointmentOutcomeFolder

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
  availabilityControl: ReactNode
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
  outcomeNextCursors: AppointmentOutcomeCursors
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
  onTaskUpdated: (task: Task) => void
  onLoadMore: () => void
  onRecoveryLoadMore: () => void
  onMessageLoadMore: () => void
  onOutcomeLoadMore: (folder: AppointmentSection) => void
}

export function TaskRail({
  discovery,
  practice,
  workspaceControl,
  availabilityControl,
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
  outcomeNextCursors,
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
  onTaskUpdated,
  onLoadMore,
  onRecoveryLoadMore,
  onMessageLoadMore,
  onOutcomeLoadMore,
}: TaskRailProps) {
  const stateKey = sidebarStateKey(discovery.actor.subject, practice.id)
  const [expansion, setExpansion] = useState<{
    stateKey: string
    sections: AttentionSection[]
  }>()
  const expanded = expansion?.stateKey === stateKey ? expansion.sections : []
  const [appointmentExpansion, setAppointmentExpansion] = useState<{
    stateKey: string
    sections: AppointmentSection[]
  }>()
  const expandedAppointment =
    appointmentExpansion?.stateKey === stateKey
      ? appointmentExpansion.sections
      : []
  const [taskCategoryState, setTaskCategoryState] = useState<{
    stateKey: string
    value: TaskCategoryFilter
  }>()
  const taskCategory =
    taskCategoryState?.stateKey === stateKey
      ? taskCategoryState.value
      : "all"
  const [pendingTaskID, setPendingTaskID] = useState("")
  const [completionError, setCompletionError] = useState<{
    taskID: string
    message: string
  }>()
  const currentStateKey = useRef(stateKey)
  const scrollContainer = useRef<HTMLDivElement | null>(null)
  const searchInput = useRef<HTMLInputElement | null>(null)
  const router = useRouter()
  const { setTheme, theme } = useTheme()
  const taskRows = useMemo(
    () => newestFirst(filterTaskQueue(tasks), taskRelativeAt),
    [tasks],
  )
  const filteredTasks = useMemo(
    () =>
      filterTasksByCategory(taskRows, taskCategory),
    [taskRows, taskCategory],
  )
  const selectedTaskCount = taskCountForCategory(taskCounts, taskCategory)
  const categorizedAIOutcomes = useMemo(
    () =>
      categorizeAIOutcomes(
        newestFirst(aiOutcomes, aiOutcomeOccurredAt),
      ),
    [aiOutcomes],
  )
  const appointmentFolders = appointmentOutcomeFolderKeys.map((key) => ({
    key,
    title: appointmentOutcomeFolders[key].title,
    outcomes: categorizedAIOutcomes[key],
    count: outcomeCounts[key],
  }))
  const appointmentCount = appointmentFolders.reduce(
    (total, folder) => total + folder.count,
    0,
  )
  const recoveryRows = useMemo(
    () => aggregateRecovery(recoveryTasks),
    [recoveryTasks],
  )
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
    currentStateKey.current = stateKey
    const restored = readSidebarState(stateKey)
    const frame = window.requestAnimationFrame(() => {
      setTaskCategoryState({
        stateKey,
        value: restored?.taskCategory ?? "all",
      })
      setPendingTaskID("")
      setCompletionError(undefined)
      scrollContainer.current?.scrollTo({ top: restored?.scrollTop ?? 0 })
    })
    return () => {
      if (currentStateKey.current === stateKey) currentStateKey.current = ""
      window.cancelAnimationFrame(frame)
    }
  }, [stateKey])

  function toggle(section: AttentionSection) {
    setExpansion((current) => {
      const sections = current?.stateKey === stateKey ? current.sections : []
      return {
        stateKey,
        sections: sections.includes(section)
          ? sections.filter((candidate) => candidate !== section)
          : [...sections, section],
      }
    })
  }

  function toggleAppointment(section: AppointmentSection) {
    setAppointmentExpansion((current) => {
      const sections = current?.stateKey === stateKey ? current.sections : []
      return {
        stateKey,
        sections: sections.includes(section)
          ? sections.filter((candidate) => candidate !== section)
          : [...sections, section],
      }
    })
  }

  function rememberScroll() {
    writeSidebarState(stateKey, {
      scrollTop: scrollContainer.current?.scrollTop ?? 0,
      taskCategory,
    })
  }

  function selectTaskCategory(value: TaskCategoryFilter) {
    setTaskCategoryState({ stateKey, value })
    writeSidebarState(stateKey, {
      scrollTop: scrollContainer.current?.scrollTop ?? 0,
      taskCategory: value,
    })
  }

  async function complete(task: Task) {
    if (pendingTaskID) return
    const requestStateKey = stateKey
    setPendingTaskID(task.id)
    setCompletionError(undefined)
    const authentication = await getAccessTokenResult()
    if (currentStateKey.current !== requestStateKey) return
    if (authentication.status !== "authenticated") {
      setPendingTaskID("")
      setCompletionError({
        taskID: task.id,
        message:
          authentication.status === "unauthenticated"
            ? "Your session expired. Sign in again, then retry."
            : "Task completion is temporarily unavailable. Retry in a moment.",
      })
      return
    }
    const result = await completeTask({
      client: portalClient(authentication.token),
      path: { taskId: task.id },
      body: { expectedVersion: task.version },
    }).catch(() => undefined)
    if (currentStateKey.current !== requestStateKey) return
    setPendingTaskID("")
    if (result?.data) {
      onTaskUpdated(result.data)
      return
    }
    setCompletionError({
      taskID: task.id,
      message:
        result?.response?.status === 409
          ? "This Task changed elsewhere. Open it to review the latest state, then retry."
          : "This Task could not be completed. Retry from the row or open its details.",
    })
  }

  return (
    <>
      <Sidebar collapsible="offcanvas">
        <SidebarHeader className="gap-3 p-3 pb-2">
          <div className="flex items-center gap-2 px-1">
            <SidebarTrigger className="-ml-1 size-7 shrink-0 text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-foreground" />
            <AcuityMark className="size-7 shrink-0" />
            <div className="min-w-0 flex-1">{workspaceControl}</div>
            <ConnectionMark state={connection} />
          </div>
          <form
            onSubmit={(event) => {
              event.preventDefault()
              if (resolveWorkspaceSearch(search).kind === "tasks") {
                setExpansion((current) => {
                  const sections =
                    current?.stateKey === stateKey ? current.sections : []
                  return {
                    stateKey,
                    sections: sections.includes("tasks")
                      ? sections
                      : [...sections, "tasks"],
                  }
                })
              }
              onSearchSubmit()
            }}
          >
            <InputGroup className="h-8 rounded-lg border-sidebar-border bg-sidebar-control shadow-none transition-[background-color,border-color,box-shadow] duration-150 hover:bg-background hover:shadow-sm focus-within:border-sidebar-ring focus-within:bg-background focus-within:ring-2 focus-within:ring-sidebar-ring/30">
              <InputGroupAddon>
                <SearchIcon />
              </InputGroupAddon>
              <InputGroupInput
                ref={searchInput}
                aria-label="Search tasks, names, or phone"
                aria-invalid={Boolean(engagementError)}
                autoComplete="off"
                enterKeyHint="go"
                placeholder="Search"
                value={search}
                onChange={(event) => onSearchChange(event.target.value)}
              />
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
              <p role="alert" className="px-2 pt-1 text-xs text-destructive">
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
            count={selectedTaskCount}
            expanded={expanded.includes("tasks")}
            onToggle={() => toggle("tasks")}
            action={
              <TaskCategoryMenu
                value={taskCategory}
                counts={taskCounts}
                onChange={selectTaskCategory}
              />
            }
          >
            {filteredTasks.map((task) => (
              <TaskRow
                key={task.id}
                task={task}
                active={task.id === selectedTaskID}
                onSelect={() => onTaskSelect(task)}
                completionDisabled={Boolean(pendingTaskID)}
                completionPending={pendingTaskID === task.id}
                completionError={
                  completionError?.taskID === task.id
                    ? completionError.message
                    : ""
                }
                onComplete={() => void complete(task)}
              />
            ))}
            {loading && filteredTasks.length === 0 && (
              <RailLoading inMenu label="Loading tasks" />
            )}
            {!loading && selectedTaskCount === 0 && (
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
            expanded={expanded.includes("calls")}
            selectedTaskID={selectedTaskID}
            onToggle={() => toggle("calls")}
            onSelect={onTaskSelect}
            cursor={recoveryNextCursor}
            loading={recoveryLoading}
            onLoadMore={onRecoveryLoadMore}
          />
          {outcomesError && (
            <p role="alert" className="px-6 py-1 text-xs text-destructive">
              AI appointment updates are unavailable.
            </p>
          )}
          <AttentionGroup
            title="Appointments"
            count={appointmentCount}
            expanded={expanded.includes("appointments")}
            onToggle={() => toggle("appointments")}
          >
            {appointmentFolders.map((folder) => (
              <AppointmentFolder
                key={folder.key}
                title={folder.title}
                outcomes={folder.outcomes}
                count={folder.count}
                expanded={expandedAppointment.includes(folder.key)}
                selectedAIInteractionID={selectedAIInteractionID}
                loading={outcomesLoading}
                cursor={taskFolderCursor(
                  outcomeNextCursors[folder.key],
                  folder.outcomes.length,
                  folder.count,
                )}
                onToggle={() => toggleAppointment(folder.key)}
                onAIInteractionSelect={onAIInteractionSelect}
                onLoadMore={() => onOutcomeLoadMore(folder.key)}
              />
            ))}
          </AttentionGroup>
          <AttentionGroup
            title="Texts"
            count={textRows.length}
            expanded={expanded.includes("texts")}
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
            <RailShowMore
              cursor={messageNextCursor}
              loading={messageLoading}
              onLoadMore={onMessageLoadMore}
            />
          </AttentionGroup>
        </SidebarContent>
        <SidebarFooter className="p-2">
          {availabilityControl && (
            <div className="mb-1 border-b border-sidebar-border px-2 py-2">
              {availabilityControl}
            </div>
          )}
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
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <SidebarMenuButton
                      aria-label="Appearance"
                      className="data-popup-open:bg-sidebar-accent"
                    />
                  }
                >
                  <SunMoonIcon />
                  <span>Appearance</span>
                </DropdownMenuTrigger>
                <DropdownMenuContent side="right" align="end" className="w-40">
                  <DropdownMenuRadioGroup
                    value={theme ?? "system"}
                    onValueChange={setTheme}
                  >
                    <DropdownMenuLabel>Theme</DropdownMenuLabel>
                    <DropdownMenuRadioItem value="system" closeOnClick>
                      <MonitorIcon />
                      System
                    </DropdownMenuRadioItem>
                    <DropdownMenuRadioItem value="light" closeOnClick>
                      <SunIcon />
                      Light
                    </DropdownMenuRadioItem>
                    <DropdownMenuRadioItem value="dark" closeOnClick>
                      <MoonIcon />
                      Dark
                    </DropdownMenuRadioItem>
                  </DropdownMenuRadioGroup>
                </DropdownMenuContent>
              </DropdownMenu>
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
  action,
  children,
}: {
  title: string
  count?: number
  expanded: boolean
  onToggle: () => void
  action?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <Collapsible
      open={expanded}
      onOpenChange={(open) => {
        if (open !== expanded) onToggle()
      }}
      render={<SidebarGroup className="p-0" />}
    >
      <div className="flex min-w-0 items-center gap-0.5">
        <CollapsibleTrigger
          render={
            <button
              type="button"
              className="group/disclosure flex h-9 min-w-0 flex-1 shrink-0 items-center rounded-lg px-2 text-left text-sm/5 font-medium text-muted-foreground outline-hidden transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            />
          }
        >
          <ChevronRightIcon
            aria-hidden="true"
            className={cn(
              "mr-1 size-3.5 shrink-0 stroke-[1.5] text-muted-foreground transition-transform motion-reduce:transition-none",
              expanded && "rotate-90",
            )}
          />
          {expanded ? (
            <FolderOpenIcon className="mr-2 size-4 shrink-0 stroke-[1.65]" />
          ) : (
            <FolderIcon className="mr-2 size-4 shrink-0 stroke-[1.65]" />
          )}
          <span className="truncate">{title}</span>
          {count !== undefined && (
            <span className="ml-auto text-xs tabular-nums text-muted-foreground">
              {count}
            </span>
          )}
        </CollapsibleTrigger>
        {action}
      </div>
      <CollapsibleContent>
        <SidebarGroupContent>
          <SidebarMenu className="mx-3 w-auto gap-0.5 border-l border-sidebar-border py-1 pl-2">
            {children}
          </SidebarMenu>
        </SidebarGroupContent>
      </CollapsibleContent>
    </Collapsible>
  )
}

function TaskCategoryMenu({
  value,
  counts,
  onChange,
}: {
  value: TaskCategoryFilter
  counts: TaskFolderCounts
  onChange: (value: TaskCategoryFilter) => void
}) {
  const activeLabel =
    taskCategoryOptions.find((option) => option.value === value)?.label ??
    "All types"
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            aria-label={`Filter Tasks: ${activeLabel}`}
            className="flex h-8 max-w-24 shrink-0 items-center gap-1 rounded-md px-2 text-xs font-medium text-muted-foreground outline-hidden transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring data-popup-open:bg-sidebar-accent data-popup-open:text-sidebar-foreground"
          />
        }
      >
        <ListFilterIcon aria-hidden="true" className="size-3.5 shrink-0" />
        <span className="truncate">{activeLabel}</span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuRadioGroup
          value={value}
          onValueChange={(nextValue) =>
            onChange(nextValue as TaskCategoryFilter)
          }
        >
          <DropdownMenuLabel>Task type</DropdownMenuLabel>
          {taskCategoryOptions.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value}>
              <span className="flex-1">{option.label}</span>
              <span className="mr-5 tabular-nums text-muted-foreground">
                {taskCountForCategory(counts, option.value)}
              </span>
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function AppointmentFolder({
  title,
  outcomes,
  count,
  expanded,
  selectedAIInteractionID,
  loading,
  cursor,
  onToggle,
  onAIInteractionSelect,
  onLoadMore,
}: {
  title: string
  outcomes: AiOutcomeItem[]
  count: number
  expanded: boolean
  selectedAIInteractionID: string
  loading: boolean
  cursor: string
  onToggle: () => void
  onAIInteractionSelect: (interaction: AiOutcomeItem) => void
  onLoadMore: () => void
}) {
  return (
    <Collapsible
      open={expanded}
      onOpenChange={(open) => {
        if (open !== expanded) onToggle()
      }}
      render={<SidebarMenuItem />}
    >
      <CollapsibleTrigger
        render={
          <button
            type="button"
            className="flex h-8 w-full items-center rounded-md px-2 text-left text-xs font-medium text-muted-foreground outline-hidden transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
          />
        }
      >
        <ChevronRightIcon
          aria-hidden="true"
          className={cn(
            "mr-1.5 size-3.5 shrink-0 stroke-[1.5] transition-transform motion-reduce:transition-none",
            expanded && "rotate-90",
          )}
        />
        <span className="truncate">{title}</span>
        <span className="ml-auto text-xs tabular-nums text-muted-foreground">
          {count}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <SidebarMenu className="ml-3 w-auto gap-0.5 border-l border-sidebar-border py-1 pl-2">
          {outcomes.map((interaction) => (
            <AIOutcomeRow
              key={interaction.id}
              interaction={interaction}
              active={interaction.id === selectedAIInteractionID}
              onSelect={() => onAIInteractionSelect(interaction)}
            />
          ))}
          {loading && outcomes.length === 0 && (
            <RailLoading inMenu label={`Loading ${title.toLowerCase()}`} />
          )}
          {!loading && count === 0 && (
            <RailEmpty inMenu>{`No ${title.toLowerCase()}`}</RailEmpty>
          )}
          <RailShowMore
            cursor={cursor}
            loading={loading}
            onLoadMore={onLoadMore}
          />
        </SidebarMenu>
      </CollapsibleContent>
    </Collapsible>
  )
}

function AIOutcomeRow({
  interaction,
  active,
  onSelect,
}: {
  interaction: AiOutcomeItem
  active: boolean
  onSelect: () => void
}) {
  const occurredAt = aiOutcomeOccurredAt(interaction)
  return (
    <SidebarMenuItem>
      <RailHoverDetails
        eyebrow="Appointment"
        title={appointmentOutcomeTitle(interaction.appointmentOutcome)}
        phone={interaction.phone}
        office={interaction.locationName}
        meta={`${aiCallCompletionLabel(interaction.status)} · ${relativeTime(occurredAt)}`}
      >
        <SidebarMenuButton
          isActive={active}
          className="h-9 rounded-md px-2.5"
          onClick={onSelect}
        >
          <span className="min-w-0 flex-1 truncate text-sm font-medium tabular-nums">
            {formatUSPhone(interaction.phone)}
          </span>
          <time
            className="ml-2 shrink-0 text-xs tabular-nums text-muted-foreground"
            dateTime={occurredAt}
          >
            {relativeTime(occurredAt)}
          </time>
        </SidebarMenuButton>
      </RailHoverDetails>
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
  onSelect,
  completionDisabled,
  completionPending,
  completionError,
  onComplete,
}: {
  task: Task
  active: boolean
  onSelect: () => void
  completionDisabled: boolean
  completionPending: boolean
  completionError: string
  onComplete: () => void
}) {
  return (
    <SidebarMenuItem
      data-testid="task-row"
      className="group/task relative"
    >
      <RailHoverDetails
        eyebrow="Task"
        title={task.title}
        phone={task.phone}
        office={task.locationName}
        meta={`${taskUrgencyLabel(task.urgency)} · Updated ${relativeTime(taskRelativeAt(task))}`}
      >
        <SidebarMenuButton
          isActive={active}
          className="h-9 rounded-md py-0 pr-10 pl-2.5"
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 items-center gap-2">
            {task.unread && task.state === "OPEN" && (
              <span aria-label="Unread conversation" className="size-1.5 shrink-0 rounded-full bg-warning" />
            )}
            {task.state === "COMPLETED" && (
              <CheckCircle2Icon className="size-4 shrink-0 stroke-[1.75] text-success" />
            )}
            <span className="truncate text-sm font-medium">{task.title}</span>
          </span>
        </SidebarMenuButton>
      </RailHoverDetails>
      <span className="pointer-events-none absolute top-1 right-1 size-7">
        <time
          className="absolute inset-0 flex items-center justify-center text-xs tabular-nums text-muted-foreground transition-opacity duration-150 group-hover/task:opacity-0 group-focus-within/task:opacity-0 motion-reduce:duration-0 motion-reduce:transition-none"
          dateTime={taskRelativeAt(task)}
        >
          {relativeTime(taskRelativeAt(task))}
        </time>
        {task.state === "OPEN" && (
          <Tooltip>
            <TooltipTrigger
              render={
                <button
                  type="button"
                  aria-label={`Complete Task: ${task.title}`}
                  aria-busy={completionPending || undefined}
                  disabled={completionDisabled}
                  className="pointer-events-none absolute inset-0 flex items-center justify-center rounded-md text-muted-foreground opacity-0 outline-hidden transition-[color,background-color,opacity] duration-150 hover:bg-sidebar-accent hover:text-success focus-visible:ring-2 focus-visible:ring-sidebar-ring group-hover/task:pointer-events-auto group-hover/task:opacity-100 group-focus-within/task:pointer-events-auto group-focus-within/task:opacity-100 disabled:pointer-events-none motion-reduce:duration-0 motion-reduce:transition-none"
                  onClick={(event) => {
                    event.stopPropagation()
                    onComplete()
                  }}
                />
              }
            >
              {completionPending ? (
                <Spinner className="size-3.5" />
              ) : (
                <CheckIcon aria-hidden="true" className="size-4" />
              )}
            </TooltipTrigger>
            <TooltipContent side="right">Complete Task</TooltipContent>
          </Tooltip>
        )}
      </span>
      {completionError && (
        <p
          role="alert"
          className="px-3 pt-1 pb-1.5 text-xs leading-snug text-destructive"
        >
          {completionError}
        </p>
      )}
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
  onSelect,
}: {
  row: RecoveryRowValue
  active: boolean
  onSelect: () => void
}) {
  const kind = row.voicemailCount > 0 ? "Voicemail" : "Missed call"
  const count = row.voicemailCount || row.missedCount
  return (
    <SidebarMenuItem>
      <RailHoverDetails
        eyebrow="Call recovery"
        title={kind}
        phone={row.phone}
        office={row.locationName}
        meta={`${count} ${kind.toLowerCase()}${count === 1 ? "" : "s"} · ${relativeTime(row.latestAt)}`}
      >
        <SidebarMenuButton
          isActive={active}
          className="h-9 rounded-md px-2.5"
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
            <span className="shrink-0 text-sm font-medium tabular-nums">
              {formatUSPhone(row.phone)}
            </span>
            <span className="truncate text-xs text-muted-foreground">
              {kind}
            </span>
          </span>
          <time
            className="ml-2 shrink-0 text-xs tabular-nums text-muted-foreground"
            dateTime={row.latestAt}
          >
            {relativeTime(row.latestAt)}
          </time>
        </SidebarMenuButton>
      </RailHoverDetails>
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
      <RailHoverDetails
        eyebrow="Unread text"
        title={row.engagement.displayName || row.previewThread.preview || "Attachment"}
        phone={row.engagement.phone}
        office={row.engagement.locations.map((location) => location.name).join(", ")}
        meta={`Received ${relativeTime(row.engagement.latestActivity)}`}
      >
        <SidebarMenuButton
          isActive={active}
          data-testid="text-attention-row"
          className="h-9 rounded-md px-2.5"
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 items-baseline gap-1.5">
            <span className="shrink-0 text-sm font-medium tabular-nums">
              {formatUSPhone(row.engagement.phone)}
            </span>
            <span className="truncate text-xs text-muted-foreground">
              {row.previewThread.preview || "Attachment"}
            </span>
          </span>
          <time
            className="ml-2 shrink-0 text-xs tabular-nums text-muted-foreground"
            dateTime={row.engagement.latestActivity}
          >
            {relativeTime(row.engagement.latestActivity)}
          </time>
        </SidebarMenuButton>
      </RailHoverDetails>
    </SidebarMenuItem>
  )
}

function RailHoverDetails({
  eyebrow,
  title,
  phone,
  office,
  meta,
  children,
}: {
  eyebrow: string
  title: string
  phone: string
  office: string
  meta: string
  children: React.ReactElement
}) {
  return (
    <Tooltip>
      <TooltipTrigger render={children} />
      <TooltipContent
        data-testid="rail-hover-details"
        side="right"
        align="start"
        sideOffset={4}
        showArrow={false}
        className="w-80 max-w-[calc(100vw-1rem)] flex-col items-stretch gap-0 overflow-hidden rounded-xl border border-border bg-popover px-0 py-0 text-popover-foreground shadow-[0_16px_40px_rgba(0,0,0,0.14)]"
      >
        <div className="px-4 pt-4 pb-3.5">
          <p className="text-[10px] leading-3 font-semibold tracking-[0.1em] text-muted-foreground uppercase">
            {eyebrow}
          </p>
          <p className="mt-1 line-clamp-2 text-sm leading-5 font-medium text-popover-foreground">
            {title}
          </p>
        </div>
        <div className="grid gap-2 border-t border-border px-4 py-3">
          <HoverDetail
            icon={<PhoneIcon />}
            label="Phone"
            value={formatUSPhone(phone)}
            tabular
          />
          <HoverDetail icon={<Building2Icon />} label="Office" value={office} />
        </div>
        <p className="truncate border-t border-border px-4 py-2.5 text-xs leading-4 text-muted-foreground">
          {meta}
        </p>
      </TooltipContent>
    </Tooltip>
  )
}

function HoverDetail({
  icon,
  label,
  value,
  tabular = false,
}: {
  icon: React.ReactNode
  label: string
  value: string
  tabular?: boolean
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <span className="[&_svg]:size-4 [&_svg]:stroke-[1.7] text-muted-foreground">
        {icon}
      </span>
      <span className="w-10 shrink-0 text-xs leading-4 font-medium text-muted-foreground">
        {label}
      </span>
      <span
        className={cn(
          "min-w-0 flex-1 truncate text-sm leading-5 text-popover-foreground",
          tabular && "tabular-nums",
        )}
      >
        {value}
      </span>
    </div>
  )
}

function aggregateRecovery(tasks: Task[]): RecoveryRowValue[] {
  return sortRecoveryQueue(
    tasks.filter(
      (task) =>
        task.origin === "MISSED_CALL_RECOVERY" ||
        task.origin === "VOICEMAIL_RECOVERY",
    ),
  ).map((task) => {
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
}

function aggregateTexts(messages: MessageThreadSummary[]): TextAttentionRow[] {
  const byPhone = new Map<string, MessageThreadSummary[]>()
  for (const thread of messages) {
    if (!thread.unread || thread.openTaskCount > 0) continue
    byPhone.set(thread.externalPhone, [...(byPhone.get(thread.externalPhone) ?? []), thread])
  }
  return newestFirst(
    [...byPhone.entries()].map(([phone, threads]) => {
      const newest = newestFirst(threads, (thread) => thread.latestActivity)[0]!
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
    }),
    (row) => row.engagement.latestActivity,
  )
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
        className="flex h-8 w-full items-center justify-center rounded-md text-xs font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:pointer-events-none disabled:opacity-60"
        disabled={loading}
        onClick={onLoadMore}
      >
        {loading ? <Spinner /> : "Show more"}
      </button>
    </SidebarMenuItem>
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
  taskCategory?: TaskCategoryFilter
}

function sidebarStateKey(userSubject: string, practiceID: string) {
  return `acuity.attentionRail.${userSubject}.${practiceID}`
}

function readSidebarState(key: string): SidebarState | undefined {
  if (typeof window === "undefined") return undefined
  try {
    const value = JSON.parse(window.localStorage.getItem(key) ?? "") as SidebarState
    if (!Number.isFinite(value?.scrollTop)) return undefined
    return {
      scrollTop: value.scrollTop,
      ...(isTaskCategoryFilter(value.taskCategory)
        ? { taskCategory: value.taskCategory }
        : {}),
    }
  } catch {
    return undefined
  }
}

function isTaskCategoryFilter(value: unknown): value is TaskCategoryFilter {
  return taskCategoryOptions.some((option) => option.value === value)
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

function taskUrgencyLabel(urgency: Task["urgency"]) {
  switch (urgency) {
    case "high_priority":
      return "High priority"
    case "non_urgent":
      return "Non-urgent"
    default:
      return "Normal priority"
  }
}

function taskRelativeAt(task: Task) {
  return task.state === "OPEN" ? task.updatedAt : (task.completedAt ?? task.updatedAt)
}

function aiOutcomeOccurredAt(interaction: AiOutcomeItem) {
  return (
    interaction.appointmentOccurredAt ??
    interaction.endedAt ??
    interaction.startedAt
  )
}
