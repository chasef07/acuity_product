"use client"

import { useEffect, useRef, type ReactNode } from "react"
import { useTheme } from "next-themes"
import { useRouter } from "next/navigation"
import {
  ArrowRightIcon,
  Building2Icon,
  SparklesIcon,
  ChartNoAxesCombinedIcon,
  CheckIcon,
  ChevronDownIcon,
  FolderIcon,
  FolderOpenIcon,
  LogOutIcon,
  MonitorIcon,
  MoonIcon,
  PhoneIcon,
  SearchIcon,
  SunIcon,
} from "lucide-react"

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
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
  DropdownMenuGroup,
  DropdownMenuItem,
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
import { ReviewFolder } from "./review-folder"
import { WorkspaceWindowFailure } from "@/components/workspace/workspace-window-failure"
import type { Task, TaskFolderCounts } from "@/lib/api/generated/types.gen"
import { authClient } from "@/lib/auth-client"
import { canViewPracticeAnalytics } from "@/lib/booking-analytics"
import { formatUSPhone } from "@/lib/phone"
import { cn } from "@/lib/utils"
import { taskGroups } from "@/lib/task-groups"
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

const taskFilterLabels: Partial<Record<TaskCategoryFilter, string>> = {
  appointments: "Scheduling follow-up",
  documentation: "Medical records",
  medication: "Clinical & pharmacy",
  insurance: "Insurance",
  optical: "Optical",
}
const taskCategoryOptions: Array<{ value: TaskCategoryFilter; label: string }> =
  [
    { value: "all", label: "All categories" },
    ...taskGroups.map((group) => ({
      ...group,
      label: taskFilterLabels[group.value] ?? group.label,
    })),
  ]

type WorkspaceRailProps = {
  projection: WorkspaceProjectionState
  availabilityControl?: ReactNode
  locationControl?: ReactNode
  onIntent: (intent: WorkspaceProjectionIntent) => void
}

export function WorkspaceRail({
  projection,
  locationControl,
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
  const appointmentLocation = practice.locations.find(
    (location) =>
      location.name.trim().toLowerCase() === "spring hill" &&
      (projection.scope.locationScopeID === location.id ||
        practice.locations.length === 1),
  )
  const selectedTaskCount = taskCountForCategory(taskCounts, taskCategory)
  const folders: Array<{
    section: Exclude<WorkspaceRailSection, "completed">
    title: string
    count: number
  }> = [
    {
      section: "tasks",
      title:
        (projection.rail.taskResponsibility ?? "mine") === "mine"
          ? "My Tasks"
          : "All Tasks",
      count: selectedTaskCount,
    },
    {
      section: "calls",
      title: "Missed Calls & Voicemails",
      count: taskCounts.callRecovery ?? 0,
    },
    ...(appointmentLocation || (taskCounts.appointmentReviews ?? 0) > 0
      ? [
          {
            section: "appointments" as const,
            title: "Appointments",
            count: taskCounts.appointmentReviews ?? 0,
          },
        ]
      : []),
    { section: "texts", title: "Texts", count: taskCounts.texts ?? 0 },
  ]
  const completed = projection.completedTasks
  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if (
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== "k"
      ) {
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

  function renderTask(task: Task) {
    return (
      <TaskRow
        key={task.id}
        task={task}
        active={
          projection.selection.view === "engagement" && (
            task.id === selectedTaskID ||
            Boolean(task.groupMembers?.some((member) => member.id === selectedTaskID))
          )
        }
        onSelect={() => onIntent({ type: "select-task", task })}
        completionDisabled={Boolean(pendingTaskID)}
        completionPending={pendingTaskID === task.id}
        completionError={
          completionError?.taskID === task.id ? completionError.message : ""
        }
        onComplete={() => onIntent({ type: "complete-task", task })}
      />
    )
  }

  return (
    <>
      <Sidebar collapsible="offcanvas" className="group/rail">
        <SidebarHeader className="gap-3 p-3 pb-2">
          <div className="flex items-center gap-2 px-1">
            <AcuityMark className="size-7 shrink-0" />
            <span className="flex-1 text-base font-semibold tracking-tight">Acuity Health</span>
            <SidebarTrigger className="size-7 shrink-0 text-muted-foreground [@media(hover:hover)]:opacity-0 group-hover/rail:opacity-100 group-focus-within/rail:opacity-100 focus-visible:opacity-100" />
          </div>
          <div className="flex items-start gap-2">
            <form className="min-w-0 flex-1"
              onSubmit={(event) => {
                event.preventDefault()
                onIntent({ type: "submit-search" })
              }}
            >
              <InputGroup className="h-9 rounded-md border-transparent bg-transparent! shadow-none transition-[background-color,border-color,box-shadow] duration-150 hover:border-sidebar-border hover:bg-background! focus-within:border-sidebar-ring focus-within:bg-background! focus-within:ring-2 focus-within:ring-sidebar-ring/30">
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
            {locationControl}
          </div>
          {connection === "degraded" && <p role="status" className="px-1 text-xs text-destructive">Live updates delayed. Reconnecting…</p>}
          <SidebarMenu className="pt-1">
            <SidebarMenuItem>
              <SidebarMenuButton
                isActive={projection.selection.view === "manage-agent"}
                tooltip="Manage agent"
                onClick={() => onIntent({ type: "select-manage-agent" })}
              >
                <SparklesIcon />
                <span>Manage agent</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
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
                  onClick={() =>
                    onIntent({ type: "select-operator-analytics" })
                  }
                >
                  <ChartNoAxesCombinedIcon />
                  <span>AI diagnostics</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            )}
          </SidebarMenu>
        </SidebarHeader>
        <SidebarContent
          ref={scrollContainer}
          className="gap-2 overflow-y-auto px-2 py-2"
          onScroll={rememberScroll}
        >
          <div aria-label="Workspace folders" className="space-y-2">
            {folders.map((folder) => (
              <AttentionGroup
                key={folder.section}
                title={folder.title}
                count={folder.count}
                expanded={expanded.includes(folder.section)}
                onToggle={() => toggle(folder.section)}
                action={
                  folder.section === "tasks" ? (
                    <TaskViewMenu
                      category={taskCategory}
                      responsibility={
                        projection.rail.taskResponsibility ?? "mine"
                      }
                      counts={taskCounts}
                      onCategoryChange={selectTaskCategory}
                      onResponsibilityChange={(responsibility) =>
                        onIntent({ type: "set-task-filters", responsibility })
                      }
                    />
                  ) : undefined
                }
              >
                {folder.section === "tasks" ? (
                  <>
                    {tasks.map(renderTask)}
                    {loading && tasks.length === 0 && (
                      <RailLoading label="Loading tasks" />
                    )}
                    {!loading && selectedTaskCount === 0 && (
                      <RailEmpty>No open Tasks in this view.</RailEmpty>
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
                  </>
                ) : (
                  <ReviewFolder
                    key={`${railStateKey}:${projection.scope.locationScopeID}:${projection.search.applied}:${folder.section}`}
                    practiceID={practice.id}
                    locationID={projection.scope.locationScopeID}
                    search={projection.search.applied}
                    kind={folder.section}
                    revision={projection.workspace?.version ?? 0}
                    count={folder.count}
                    renderTask={renderTask}
                  />
                )}
              </AttentionGroup>
            ))}
          </div>

          <div className="mt-3">
            <CompletedGroup
              title="Recently completed"
              expanded={expanded.includes("completed")}
              onToggle={() => toggle("completed")}
            >
              {completed.items.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  active={task.id === selectedTaskID}
                  onSelect={() => onIntent({ type: "select-task", task })}
                  completionDisabled
                  completionPending={false}
                  completionError=""
                  onComplete={() => {}}
                />
              ))}
              {completed.loading && completed.items.length === 0 && (
                <RailLoading label="Loading completed Tasks" />
              )}
              {!completed.loading && completed.items.length === 0 && (
                <RailEmpty>No completed Tasks match these filters.</RailEmpty>
              )}
              {completed.error && (
                <WorkspaceWindowFailure
                  message={completed.error}
                  onRetry={() => onIntent({ type: "retry" })}
                />
              )}
              <RailShowMore
                cursor={completed.nextCursor}
                loading={completed.loading}
                onLoadMore={() =>
                  onIntent({ type: "load-more", window: "completedTasks" })
                }
              />
            </CompletedGroup>
          </div>
        </SidebarContent>
        <SidebarFooter className="border-t border-sidebar-border p-2">
          {availabilityControl}
          <DropdownMenu>
            <DropdownMenuTrigger
              render={<Button variant="ghost" className="h-auto w-full min-w-0 justify-start gap-2 px-2 py-2" aria-label="Account menu" />}
            >
              <span aria-hidden="true" className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-sm text-muted-foreground">
                {discovery.actor.email.charAt(0).toUpperCase()}
              </span>
              <span className="truncate text-xs">{discovery.actor.email}</span>
            </DropdownMenuTrigger>
            <DropdownMenuContent side="top" align="start" className="w-64">
              <DropdownMenuGroup>
                <DropdownMenuLabel>Signed in as</DropdownMenuLabel>
                <p className="truncate px-2 pb-2 text-sm" title={discovery.actor.email}>{discovery.actor.email}</p>
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
  const FolderStateIcon = expanded ? FolderOpenIcon : FolderIcon

  return (
    <Collapsible
      open={expanded}
      onOpenChange={(open) => {
        if (open !== expanded) onToggle()
      }}
      render={<SidebarGroup className="p-0" role="group" aria-label={title} />}
    >
      <div className="flex min-w-0 items-center gap-0.5">
        <CollapsibleTrigger
          render={
            <button
              type="button"
              className="group/disclosure flex h-8 min-w-0 flex-1 shrink-0 items-center gap-2 rounded-md px-2.5 text-left text-sm/5 font-medium text-sidebar-foreground/90 outline-hidden transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            />
          }
        >
          <FolderStateIcon
            aria-hidden="true"
            className="size-4 shrink-0 text-[var(--sidebar-icon-color)] group-hover/disclosure:text-sidebar-foreground"
          />
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
          <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
            {children}
          </SidebarMenu>
        </SidebarGroupContent>
      </CollapsibleContent>
    </Collapsible>
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
          <ChevronDownIcon
            aria-hidden="true"
            className={cn(
              "size-3 shrink-0 transition-transform",
              !expanded && "-rotate-90",
            )}
          />
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
    taskFilterLabels[category] ??
    taskCategoryOptions.find((option) => option.value === category)?.label ??
    "All types"
  const visibleCategories = taskCategoryOptions.filter(
    (option) =>
      responsibility === "all" ||
      option.value === "all" ||
      option.value === category ||
      taskCountForCategory(counts, option.value) > 0,
  )
  const viewLabel = responsibility === "mine" ? "My Tasks" : "All Tasks"
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Filter ${viewLabel}${category === "all" ? "" : ` · ${activeLabel}`}`}
            title={`${viewLabel} · ${activeLabel}`}
            className="size-7 shrink-0 p-0 text-muted-foreground"
          />
        }
      >
        <ChevronDownIcon aria-hidden="true" className="size-3 shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
        <DropdownMenuRadioGroup
          value={responsibility}
          onValueChange={(value) => {
            if (value === "mine" || value === "all")
              onResponsibilityChange(value)
          }}
        >
          <DropdownMenuLabel>View</DropdownMenuLabel>
          <DropdownMenuRadioItem value="mine" closeOnClick>
            My Tasks
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="all" closeOnClick>
            All Tasks
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuRadioGroup
          value={category}
          onValueChange={(value) =>
            onCategoryChange(value as TaskCategoryFilter)
          }
        >
          <DropdownMenuLabel>{viewLabel} by type</DropdownMenuLabel>
          {visibleCategories.map((option) => (
            <DropdownMenuRadioItem
              key={option.value}
              value={option.value}
              closeOnClick
              className="min-h-7 py-1"
            >
              <span className="flex-1 whitespace-nowrap">{option.label}</span>
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
  const rowTitle = textReview
    ? (task.callerName ?? formatUSPhone(task.phone))
    : task.title
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
        title={textReview && task.preview ? task.preview : task.title}
        phone={task.phone}
        office={task.locationName}
        meta={`${taskUrgencyLabel(task.urgency)} · Updated ${relativeTime(taskRelativeAt(task))}`}
      >
        <SidebarMenuButton
          aria-label={
            grouped ? `${rowTitle}, ${groupCount} requests` : rowTitle
          }
          isActive={active}
          className="h-8 rounded-md px-2 pr-9 text-sidebar-foreground"
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="flex min-w-0 items-center gap-1.5 text-sm leading-5">
              {task.urgency === "high_priority" && (
                <span
                  className="size-1.5 shrink-0 rounded-full bg-destructive"
                  aria-label="Urgent"
                />
              )}
              <span className="truncate">{rowTitle}</span>
              {grouped && (
                <span
                  aria-label={`${groupCount} Tasks`}
                  className="shrink-0 text-xs tabular-nums text-muted-foreground"
                >
                  {groupCount}
                </span>
              )}
            </span>
          </span>
        </SidebarMenuButton>
      </RailHoverDetails>
      <span className="pointer-events-none absolute top-0 right-1 h-7 w-7 [@media(pointer:coarse)]:h-11">
        <time
          className={`absolute inset-0 flex items-center justify-center text-[10px] font-normal tabular-nums text-muted-foreground transition-opacity duration-150 ${grouped ? "" : "group-hover/task:opacity-0 group-focus-within/task:opacity-0"} motion-reduce:duration-0 motion-reduce:transition-none`}
          dateTime={taskRelativeAt(task)}
        >
          {relativeTime(taskRelativeAt(task))}
        </time>
        {task.state === "OPEN" &&
          task.origin !== "APPOINTMENT_REVIEW" &&
          task.origin !== "INBOUND_MESSAGE_REVIEW" &&
          !grouped && (
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

function RailLoading({ label }: { label: string }) {
  return (
    <SidebarMenuItem className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
      <Spinner />
      {label}
    </SidebarMenuItem>
  )
}

function RailEmpty({ children }: { children: string }) {
  return (
    <SidebarMenuItem className="px-3 py-2 text-xs text-muted-foreground">
      {children}
    </SidebarMenuItem>
  )
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
  return task.state === "OPEN"
    ? task.createdAt
    : (task.completedAt ?? task.updatedAt)
}
