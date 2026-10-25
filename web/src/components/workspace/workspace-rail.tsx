"use client"

import {
  useEffect,
  useEffectEvent,
  useRef,
  useState,
  type ReactNode,
} from "react"
import { ArrowRightIcon, SearchIcon } from "lucide-react"

import { AcuityMark } from "@/components/acuity-mark"
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
  SidebarHeader,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { ReviewFolder } from "./review-folder"
import { AnalyticsNavigation, commandCenterPages, manageAgentPages, practiceAnalyticsPages, type CommandCenterPage, type PracticeAnalyticsPage } from "./analytics-navigation"
import { WorkspaceWindowFailure } from "@/components/workspace/workspace-window-failure"
import type { Task } from "@/lib/api/generated/types.gen"
import {
  isWorkView,
  type WorkspaceProjectionIntent,
  type WorkspaceProjectionState,
  type WorkspaceRailSection,
} from "@/lib/workspace-projection"
import {
  taskCountForCategory,
  type TaskCategoryFilter,
} from "@/lib/workspace-triage"

import {
  AttentionGroup,
  CompletedGroup,
  RailEmpty,
  RailLoading,
  RailShowMore,
} from "./workspace-rail-groups"
import {
  TaskCategoryChip,
  TaskCategoryMenu,
  TaskResponsibilityToggle,
} from "./workspace-rail-filters"
import { ProlongedOutageNotice } from "./workspace-rail-outage-notice"
import { TaskRow } from "./workspace-rail-task-row"
import { currentPractice, workspaceFolders } from "./workspace-rail-folders"

type WorkspaceRailProps = {
  projection: WorkspaceProjectionState
  commandCenterPage?: CommandCenterPage
  onCommandCenterPage?: (page: CommandCenterPage) => void
  analyticsPage?: PracticeAnalyticsPage
  onAnalyticsPage?: (page: PracticeAnalyticsPage) => void
  callingStatus?: ReactNode
  locationControl?: ReactNode
  onIntent: (intent: WorkspaceProjectionIntent) => void
}

export function WorkspaceRail({
  projection,
  commandCenterPage = "calls",
  onCommandCenterPage,
  analyticsPage = "bookings",
  onAnalyticsPage,
  locationControl,
  callingStatus,
  onIntent,
}: WorkspaceRailProps) {
  const discovery = projection.discovery!
  const practice = currentPractice(projection)
  const tasks = projection.tasks.items
  const taskCounts = projection.tasks.counts
  const selectedTaskID = projection.selection.task?.id ?? ""
  const projectedSearch = projection.search.input
  const engagementError = projection.search.error
  const loading = projection.tasks.loading
  const taskError = projection.tasks.error
  const nextCursor = projection.tasks.nextCursor
  const connection = projection.connection
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
  const scrollPersistTimer = useRef<number | undefined>(undefined)
  const [search, setSearch] = useState(projectedSearch)
  const [syncedSearch, setSyncedSearch] = useState(projectedSearch)
  if (projectedSearch !== syncedSearch) {
    setSyncedSearch(projectedSearch)
    setSearch(projectedSearch)
  }
  const publishSearch = useEffectEvent((value: string) => {
    if (value !== projectedSearch) onIntent({ type: "set-search", value })
  })
  const selectedTaskCount = taskCountForCategory(taskCounts, taskCategory)
  const folders = workspaceFolders(projection)
  const completed = projection.completedTasks
  const view = projection.selection.view
  const manageAgent = view === "manage-agent"
  const workView = isWorkView(view)
  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if (
        !searchInput.current ||
        !(event.metaKey || event.ctrlKey) ||
        event.key.toLowerCase() !== "k"
      ) {
        return
      }
      event.preventDefault()
      searchInput.current.focus()
    }
    window.addEventListener("keydown", openSearch)
    return () => window.removeEventListener("keydown", openSearch)
  }, [])

  useEffect(() => {
    const timer = window.setTimeout(() => publishSearch(search), searchDraftDelayMilliseconds)
    return () => window.clearTimeout(timer)
  }, [search])

  useEffect(() => () => window.clearTimeout(scrollPersistTimer.current), [])

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      scrollContainer.current?.scrollTo({ top: projection.rail.scrollTop })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [projection.rail.scrollTop, railStateKey, workView])

  function toggle(section: WorkspaceRailSection) {
    onIntent({ type: "toggle-rail-section", section })
  }

  function rememberScroll() {
    const scrollTop = scrollContainer.current?.scrollTop ?? 0
    window.clearTimeout(scrollPersistTimer.current)
    scrollPersistTimer.current = window.setTimeout(() => {
      onIntent({ type: "remember-rail-scroll", scrollTop })
    }, scrollPersistDelayMilliseconds)
  }

  function selectTaskCategory(value: TaskCategoryFilter) {
    onIntent({ type: "set-task-category", category: value })
  }

  function selectTask(task: Task) {
    const selection = projection.selection
    const close = selection.view === "engagement" &&
      selection.contextView === "task" && selection.contextPanelOpen &&
      (selection.taskGroup?.id ?? selection.task?.id) === task.id
    onIntent(close ? { type: "close-context" } : { type: "select-task", task })
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
        onSelect={() => selectTask(task)}
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
      <Sidebar collapsible="offcanvas" className="absolute h-full [&_[data-slot=sidebar-inner]]:bg-canvas">
        <SidebarHeader className="gap-3 px-4 pt-5 pb-4">
          <div className="flex items-center gap-2 px-1">
            <AcuityMark className="size-7 shrink-0" />
            <span className="text-base font-semibold tracking-tight">Acuity Health</span>
          </div>
          {locationControl}
          {workView && <div className="flex items-start gap-2">
            <form className="min-w-0 flex-1"
              onSubmit={(event) => {
                event.preventDefault()
                onIntent({ type: "set-search", value: search })
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
                  onChange={(event) => {
                    setSearch(event.target.value)
                    if (engagementError) {
                      onIntent({ type: "set-search", value: event.target.value })
                    }
                  }}
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
          </div>}
          {connection === "degraded" && <ProlongedOutageNotice />}
        </SidebarHeader>
        {manageAgent ? (
          <SidebarContent className="overflow-y-auto px-2 py-2">
            <AnalyticsNavigation
              label="Manage agent"
              pages={manageAgentPages}
              page={projection.selection.agentPage}
              onPage={(page) => onIntent({ type: "select-manage-agent", page })}
            />
          </SidebarContent>
        ) : view === "operator-analytics" && onCommandCenterPage ? (
          <SidebarContent className="overflow-y-auto px-2 py-2">
            <AnalyticsNavigation label="Command Center" pages={commandCenterPages} page={commandCenterPage} onPage={onCommandCenterPage} />
          </SidebarContent>
        ) : view === "analytics" && onAnalyticsPage ? (
          <SidebarContent className="overflow-y-auto px-2 py-2">
            <AnalyticsNavigation label="Analytics" pages={practiceAnalyticsPages} page={analyticsPage} onPage={onAnalyticsPage} />
          </SidebarContent>
        ) : workView && (
        <SidebarContent
          ref={scrollContainer}
          className="gap-2 overflow-y-auto px-2 py-2"
          onScroll={rememberScroll}
        >
          <div role="group" aria-label="Workspace folders" className="space-y-2">
            {folders.map((folder) => (
              <AttentionGroup
                key={folder.section}
                title={folder.title}
                count={folder.count}
                expanded={expanded.includes(folder.section)}
                onToggle={() => toggle(folder.section)}
              >
                {folder.section === "tasks" ? (
                  <>
                    <SidebarMenuItem className="flex items-center gap-1 pb-1">
                      <TaskResponsibilityToggle
                        responsibility={projection.rail.taskResponsibility ?? "mine"}
                        onChange={(responsibility) =>
                          onIntent({ type: "set-task-filters", responsibility })
                        }
                      />
                      <TaskCategoryMenu
                        category={taskCategory}
                        responsibility={
                          projection.rail.taskResponsibility ?? "mine"
                        }
                        counts={taskCounts}
                        onCategoryChange={selectTaskCategory}
                      />
                    </SidebarMenuItem>
                    <TaskCategoryChip
                      category={taskCategory}
                      onClear={() => selectTaskCategory("all")}
                    />
                    {tasks.map(renderTask)}
                    {loading && tasks.length === 0 && (
                      <RailLoading label="Loading tasks" />
                    )}
                    {!loading && !taskError && selectedTaskCount === 0 && (
                      <RailEmpty>No open Tasks in this view.</RailEmpty>
                    )}
                    {taskError && (
                      <SidebarMenuItem>
                        <WorkspaceWindowFailure
                          message={taskError}
                          onRetry={() => onIntent({ type: "retry" })}
                        />
                      </SidebarMenuItem>
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
                  onSelect={() => selectTask(task)}
                  completionDisabled
                  completionPending={false}
                  completionError=""
                  onComplete={() => {}}
                />
              ))}
              {completed.loading && completed.items.length === 0 && (
                <RailLoading label="Loading completed Tasks" />
              )}
              {!completed.loading && !completed.error && completed.items.length === 0 && (
                <RailEmpty>No completed Tasks match these filters.</RailEmpty>
              )}
              {completed.error && (
                <SidebarMenuItem>
                  <WorkspaceWindowFailure
                    message={completed.error}
                    onRetry={() => onIntent({ type: "retry" })}
                  />
                </SidebarMenuItem>
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
        )}
        {workView && (
          <SidebarFooter className="border-t border-sidebar-border p-2 empty:hidden">{callingStatus}</SidebarFooter>
        )}
      </Sidebar>
    </>
  )
}

const searchDraftDelayMilliseconds = 250

const scrollPersistDelayMilliseconds = 150
