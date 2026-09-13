"use client"

import {
  type ReactNode,
  useEffect,
  useRef,
} from "react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ArrowRightIcon,
  Building2Icon,
  ChartNoAxesCombinedIcon,
  CheckIcon,
  ChevronDownIcon,
  LogOutIcon,
  MonitorIcon,
  MoonIcon,
  PhoneIcon,
  SearchIcon,
  SunMoonIcon,
  SunIcon,
} from "lucide-react"

import { Button } from "@/components/ui/button"
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
  DropdownMenuSeparator,
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
import { WorkspaceWindowFailure } from "@/components/workspace/workspace-window-failure"
import type {
  Task,
  TaskFolderCounts,
} from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { canViewPracticeAnalytics } from "@/lib/booking-analytics"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import { resolveWorkspaceSearch } from "@/lib/workspace-search"
import type {
  WorkspaceConnectionState,
  WorkspaceProjectionIntent,
  WorkspaceProjectionState,
  WorkspaceRailSection,
} from "@/lib/workspace-projection"
import {
  taskCountForCategory,
  type TaskCategoryFilter,
} from "@/lib/workspace-triage"

export type ConnectionState = WorkspaceConnectionState

import { taskGroups, taskGroupLabel } from "@/lib/task-groups"


const taskFilterLabels: Partial<Record<TaskCategoryFilter, string>> = {
  documentation: "Medical records",
  medication: "Clinical & pharmacy",
  insurance: "Insurance",
  optical: "Optical",
}
const taskCategoryOptions: Array<{ value: TaskCategoryFilter; label: string }> = [
  { value: "all", label: "All categories" },
  { value: "texts", label: "Texts" },
  { value: "calls", label: "Missed calls & voicemails" },
  ...taskGroups.map((group) => ({ ...group, label: taskFilterLabels[group.value] ?? group.label })),
]

type WorkspaceRailProps = {
  projection: WorkspaceProjectionState
  workspaceControl: ReactNode
  availabilityControl: ReactNode
  onIntent: (intent: WorkspaceProjectionIntent) => void
}

export function WorkspaceRail({
  projection,
  workspaceControl,
  availabilityControl,
  onIntent,
}: WorkspaceRailProps) {
  const discovery = projection.discovery!
  const practice =
    discovery.practices.find(
      (item) => item.id === projection.scope.practiceID,
    ) ?? discovery.practices[0]!
  const tasks = projection.tasks.items
  const taskCounts = projection.tasks.counts
  const selectedTaskID = projection.selection.task?.id ?? ""
  const search = projection.search.input
  const engagementError = projection.search.error
  const loading = projection.tasks.loading
  const taskError = projection.tasks.error
  const nextCursor = projection.tasks.nextCursor
  const connection = projection.connection
  const analyticsActive = projection.selection.view === "analytics"
  const railStateKey = `${discovery.actor.subject}:${practice.id}`
  const expanded = projection.rail.expanded
  const taskCategory = projection.rail.taskCategory
  const pendingTaskID = projection.completion.pendingTaskID
  const completionError = projection.completion.error
    ? {
        taskID: projection.completion.errorTaskID,
        message: projection.completion.error,
      }
    : undefined
  const scrollContainer = useRef<HTMLDivElement | null>(null)
  const searchInput = useRef<HTMLInputElement | null>(null)
  const router = useRouter()
  const { setTheme, theme } = useTheme()
  const filteredTasks = tasks
  const selectedTaskCount = taskCountForCategory(taskCounts, taskCategory)
  const completed = projection.completedTasks
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
    const frame = window.requestAnimationFrame(() => {
      scrollContainer.current?.scrollTo({ top: projection.rail.scrollTop })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [projection.rail.scrollTop, railStateKey])

  function toggle(section: WorkspaceRailSection) {
    onIntent({ type: "toggle-rail-section", section })
  }

  function rememberScroll() {
    onIntent({
      type: "remember-rail-scroll",
      scrollTop: scrollContainer.current?.scrollTop ?? 0,
    })
  }

  function selectTaskCategory(value: TaskCategoryFilter) {
    onIntent({ type: "set-task-category", category: value })
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
              if (
                resolveWorkspaceSearch(search).kind === "tasks" &&
                !expanded.includes("tasks")
              ) {
                onIntent({ type: "toggle-rail-section", section: "tasks" })
              }
              onIntent({ type: "submit-search" })
            }}
          >
            <InputGroup className="h-8 rounded-md border-sidebar-border bg-sidebar-control shadow-none transition-[background-color,border-color,box-shadow] duration-150 hover:bg-background hover:shadow-sm focus-within:border-sidebar-ring focus-within:bg-background focus-within:ring-2 focus-within:ring-sidebar-ring/30">
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
                onChange={(event) =>
                  onIntent({ type: "set-search", value: event.target.value })
                }
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
            <div className="-mx-1 pb-1">
            <TaskViewMenu
              category={taskCategory}
              responsibility={projection.rail.taskResponsibility ?? "mine"}
              counts={taskCounts}
              onCategoryChange={selectTaskCategory}
              onResponsibilityChange={(responsibility) => onIntent({ type: "set-task-filters", responsibility, category: "all" })}
            />
            <div aria-label="Open workload" className="mb-2 grid grid-cols-2 gap-1 px-1.5">
              {taskCategoryOptions.filter((option) => ["all", "texts", "calls", "appointments", taskCategory].includes(option.value) || taskCountForCategory(taskCounts, option.value) > 0).map((option) => (
                <button key={option.value} type="button" aria-pressed={taskCategory === option.value}
                  onClick={() => selectTaskCategory(option.value)}
                  className={cn("flex min-h-8 items-center justify-between gap-2 rounded-md px-2 text-left text-xs transition-colors focus-visible:outline-2 focus-visible:outline-sidebar-ring", taskCategory === option.value ? "bg-sidebar-accent font-medium text-sidebar-foreground" : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground")}>
                  <span className="truncate">{option.value === "all" ? "All work" : option.value === "calls" ? "Calls & voicemail" : option.label}</span>
                  <span className="tabular-nums">{taskCountForCategory(taskCounts, option.value)}</span>
                </button>
              ))}
            </div>
            </div>
        </SidebarHeader>
        <SidebarContent
          ref={scrollContainer}
          className="gap-2 overflow-y-auto px-2 py-2"
          onScroll={rememberScroll}
        >
          <SidebarGroup className="p-0">
            <SidebarGroupContent>
              <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
            {filteredTasks.map((task) => (
              <TaskRow key={task.id} task={task} active={task.id === selectedTaskID || Boolean(task.groupMembers?.some((member) => member.id === selectedTaskID))} onSelect={() => onIntent({ type: "select-task", task })} completionDisabled={Boolean(pendingTaskID)} completionPending={pendingTaskID === task.id} completionError={completionError?.taskID === task.id ? completionError.message : ""} onComplete={() => onIntent({ type: "complete-task", task })} />
            ))}
            {loading && filteredTasks.length === 0 && (
              <RailLoading inMenu label="Loading tasks" />
            )}
            {!loading && selectedTaskCount === 0 && (
              <RailEmpty inMenu>
                {(projection.rail.taskResponsibility ?? "mine") === "mine"
                  ? "No Tasks match your responsibilities and filters. Use All tasks to help another group."
                  : "No Tasks match these filters"}
              </RailEmpty>
            )}
            {taskError && (
              <WorkspaceWindowFailure
                message={taskError}
                onRetry={() => onIntent({ type: "retry" })}
              />
            )}
            <RailShowMore
              cursor={nextCursor}
              loading={loading}
              onLoadMore={() =>
                onIntent({ type: "load-more", window: "tasks" })
              }
            />
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>

          <div className="mt-3">
            <CompletedGroup title="Recently completed" expanded={expanded.includes("completed")} onToggle={() => toggle("completed")}>
              {completed.items.map((task) => (
                <TaskRow key={task.id} task={task} active={task.id === selectedTaskID} onSelect={() => onIntent({ type: "select-task", task })} completionDisabled completionPending={false} completionError="" onComplete={() => {}} />
              ))}
              {completed.loading && completed.items.length === 0 && <RailLoading inMenu label="Loading completed Tasks" />}
              {!completed.loading && completed.items.length === 0 && <RailEmpty inMenu>No completed Tasks match these filters.</RailEmpty>}
              {completed.error && <WorkspaceWindowFailure message={completed.error} onRetry={() => onIntent({ type: "retry" })} />}
              <RailShowMore cursor={completed.nextCursor} loading={completed.loading} onLoadMore={() => onIntent({ type: "load-more", window: "completedTasks" })} />
            </CompletedGroup>
          </div>
        </SidebarContent>
        <SidebarFooter className="p-2">
          {availabilityControl && (
            <div className="mb-1 border-b border-sidebar-border px-2 py-2">
              {availabilityControl}
            </div>
          )}
          <SidebarMenu>
            {canViewPracticeAnalytics(discovery, practice.id) && (
              <SidebarMenuItem>
                <SidebarMenuButton
                  isActive={analyticsActive}
                  tooltip="Analytics"
                  onClick={() => onIntent({ type: "select-analytics" })}
                >
                  <ChartNoAxesCombinedIcon />
                  <span>Analytics</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            )}
            {discovery.platformOperator && (
              <SidebarMenuItem>
                <SidebarMenuButton
                  isActive={projection.selection.view === "operator-analytics"}
                  tooltip="AI diagnostics"
                  onClick={() => onIntent({ type: "select-operator-analytics" })}
                >
                  <ChartNoAxesCombinedIcon />
                  <span>AI diagnostics</span>
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

function CompletedGroup({
  title,
  expanded,
  onToggle,
  children,
}: {
  title: string
  expanded: boolean
  onToggle: () => void
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
            <Button
              variant="ghost"
              className="group/disclosure flex h-8 min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 text-xs font-normal text-muted-foreground hover:bg-transparent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            />
          }
        >
          <span className="truncate">{title}</span>
            <span aria-hidden="true" className="h-px flex-1 bg-sidebar-border" />
            <ChevronDownIcon aria-hidden="true" className={cn("size-3 shrink-0 transition-transform", !expanded && "-rotate-90")} />
        </CollapsibleTrigger>
      </div>
      <CollapsibleContent>
        <SidebarGroupContent>
          <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
            {children}
          </SidebarMenu>
        </SidebarGroupContent>
      </CollapsibleContent>
    </Collapsible>
  )
}

function TaskViewMenu({
  category,
  responsibility,
  counts,
  onCategoryChange,
  onResponsibilityChange,
}: {
  category: TaskCategoryFilter
  responsibility: "mine" | "all"
  counts: TaskFolderCounts
  onCategoryChange: (value: TaskCategoryFilter) => void
  onResponsibilityChange: (value: "mine" | "all") => void
}) {
  const activeLabel =
    taskCategoryOptions.find((option) => option.value === category)?.label ??
    "All types"
  const viewLabel = responsibility === "mine" ? "My Tasks" : "All tasks"
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            aria-label={`${viewLabel}${category === "all" ? "" : ` · ${activeLabel}`}`}
            title={`${viewLabel} · ${activeLabel}`}
            className="h-9 w-full justify-start gap-2 px-2.5 text-sm font-medium text-sidebar-foreground"
          />
        }
      >
        <span className="truncate">{viewLabel}{category !== "all" && <span className="font-normal text-muted-foreground"> · {activeLabel}</span>}</span>
        <span className="ml-auto text-xs font-normal tabular-nums text-muted-foreground">{taskCountForCategory(counts, category)}</span>

        <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuRadioGroup value={responsibility} onValueChange={(value) => {
          if (value === "mine" || value === "all") onResponsibilityChange(value)
        }}>
          <DropdownMenuLabel>View</DropdownMenuLabel>
          <DropdownMenuRadioItem value="mine" closeOnClick>My Tasks</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="all" closeOnClick>All tasks</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={category}
          onValueChange={(value) => onCategoryChange(value as TaskCategoryFilter)}
        >
          <DropdownMenuLabel>{viewLabel} by type</DropdownMenuLabel>
          {taskCategoryOptions.map((option) => (
            <DropdownMenuRadioItem key={option.value} value={option.value} closeOnClick className="min-h-7 py-1">
              <span className="flex-1 whitespace-nowrap">{option.value === "all" ? "All categories" : option.label}</span>
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
  const textReview = task.origin === "INBOUND_MESSAGE_REVIEW"
  const rowTitle = textReview ? (task.callerName ?? formatUSPhone(task.phone)) : task.title
  const groupCount = task.groupMembers?.length ?? 0
  const grouped = groupCount > 1
  return (
    <SidebarMenuItem
      data-testid={grouped ? "task-group-row" : "task-row"}
      data-task-id={task.id}
      className="group/task relative"
    >
      <RailHoverDetails
        eyebrow={grouped ? `${groupCount} related Tasks` : "Task"}
        title={task.title}
        phone={task.phone}
        office={task.locationName}
        meta={`${taskUrgencyLabel(task.urgency)} · Updated ${relativeTime(taskRelativeAt(task))}`}
      >
        <SidebarMenuButton
          aria-label={grouped ? `${rowTitle}, ${groupCount} requests` : rowTitle}
          isActive={active}
          className={cn(
            "h-auto items-start rounded-lg py-2 pr-10 pl-2 text-sidebar-foreground/90",
            task.state === "OPEN" ? "min-h-16" : "min-h-0",
          )}
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 flex-col items-start gap-1">
            <span className="line-clamp-2 text-sm leading-5 font-medium">{task.urgency === "high_priority" && <span className="mr-1.5 text-xs text-destructive">Urgent</span>}{rowTitle}</span>
            {textReview && task.state === "OPEN" && task.preview && <span className="line-clamp-2 text-xs font-normal leading-4 text-muted-foreground">{task.preview}</span>}
            <span className="w-full truncate text-[11px] font-normal text-muted-foreground">{task.state === "COMPLETED" ? `Completed by ${task.completedBy?.email ?? "the team"}` : (task.callerName ?? formatUSPhone(task.phone))} · {task.locationName}</span>
            {task.state === "OPEN" && <span className="w-full truncate text-[11px] font-normal text-muted-foreground">{task.origin === "APPOINTMENT_REVIEW" ? "Appointment verification" : task.origin === "INBOUND_MESSAGE_REVIEW" ? "Text review" : task.origin === "MISSED_CALL_RECOVERY" || task.origin === "VOICEMAIL_RECOVERY" ? "Return call" : taskGroupLabel(task.category)}</span>}
            {grouped && <span aria-label={`${groupCount} Tasks`} className="shrink-0 rounded bg-sidebar-accent px-1.5 text-[10px] font-medium tabular-nums leading-4">{groupCount} requests</span>}
          </span>
        </SidebarMenuButton>
      </RailHoverDetails>
      <span className="pointer-events-none absolute top-2 right-1 h-7 w-7 [@media(pointer:coarse)]:h-11">
        <time
          className={`absolute inset-0 flex items-center justify-center text-[10px] font-normal tabular-nums text-muted-foreground transition-opacity duration-150 ${grouped ? "" : "group-hover/task:opacity-0 group-focus-within/task:opacity-0"} motion-reduce:duration-0 motion-reduce:transition-none`}
          dateTime={taskRelativeAt(task)}
        >
          {relativeTime(taskRelativeAt(task))}
        </time>
        {task.state === "OPEN" && task.origin !== "APPOINTMENT_REVIEW" && task.origin !== "INBOUND_MESSAGE_REVIEW" && !grouped && (
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
  return task.state === "OPEN" ? task.createdAt : (task.completedAt ?? task.updatedAt)
}
