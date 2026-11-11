"use client"

import {
  useEffect,
  useEffectEvent,
  useLayoutEffect,
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
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { AnalyticsNavigation, commandCenterPages, manageAgentPages, practiceAnalyticsPages, type CommandCenterPage, type PracticeAnalyticsPage } from "./analytics-navigation"
import { WorkspaceWindowFailure } from "@/components/workspace/workspace-window-failure"
import type { PatientWorkRow, Task } from "@/lib/api/generated/types.gen"
import {
  offersAppointmentChecks,
  patientWorkFooter,
  rowChangedAtAfter,
  rowClickAllowed,
  rowClickGuardMilliseconds,
} from "@/lib/patient-work"
import {
  currentPractice,
  isWorkView,
  type WorkspaceProjectionIntent,
  type WorkspaceProjectionState,
} from "@/lib/workspace-projection"

import {
  CompletedGroup,
  RailEmpty,
  RailLoading,
  RailShowMore,
} from "./workspace-rail-groups"
import { PatientFilterChips, PatientScopeToggle } from "./workspace-rail-filters"
import { ProlongedOutageNotice } from "./workspace-rail-outage-notice"
import { CompletedTaskRow, PatientRow } from "./workspace-rail-patient-row"

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
  const patients = projection.patients
  const selectedTaskID = projection.selection.task?.id ?? ""
  const selectedPhone =
    projection.selection.view === "engagement"
      ? (projection.selection.engagement?.phone ?? "")
      : ""
  const projectedSearch = projection.search.input
  const engagementError = projection.search.error
  const connection = projection.connection
  const railStateKey = `${discovery.actor.subject}:${practice.id}`
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
  const listedPhones = useRef<readonly string[]>([])
  const [rowChangedAt, setRowChangedAt] = useState<readonly (number | undefined)[]>([])
  const [refused, setRefused] = useState<{ index: number; changedAt: number } | undefined>(undefined)
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

  useLayoutEffect(() => {
    const before = listedPhones.current
    const after = patients.items.map((row) => row.phone)
    const now = performance.now()
    listedPhones.current = after
    setRowChangedAt((changedAt) => rowChangedAtAfter(changedAt, before, after, now))
  }, [patients.items])

  useEffect(() => {
    const guarded = rowChangedAt.filter((at) => at !== undefined)
    if (guarded.length === 0) return
    const earliest = Math.min(...guarded)
    const timer = window.setTimeout(
      () => setRowChangedAt((changedAt) => changedAt.map((at) => (at === earliest ? undefined : at))),
      earliest + rowClickGuardMilliseconds - performance.now(),
    )
    return () => window.clearTimeout(timer)
  }, [rowChangedAt])

  useEffect(() => {
    const frame = window.requestAnimationFrame(() => {
      scrollContainer.current?.scrollTo({ top: projection.rail.scrollTop })
    })
    return () => window.cancelAnimationFrame(frame)
  }, [projection.rail.scrollTop, railStateKey, workView])

  function rememberScroll() {
    const scrollTop = scrollContainer.current?.scrollTop ?? 0
    window.clearTimeout(scrollPersistTimer.current)
    scrollPersistTimer.current = window.setTimeout(() => {
      onIntent({ type: "remember-rail-scroll", scrollTop })
    }, scrollPersistDelayMilliseconds)
  }

  function selectTask(task: Task) {
    const selection = projection.selection
    const close = selection.view === "engagement" &&
      selection.contextView === "task" && selection.contextPanelOpen &&
      selection.task?.id === task.id
    onIntent(close ? { type: "close-context" } : { type: "select-task", task })
  }

  function renderPatient(row: PatientWorkRow, index: number) {
    const changedAt = rowChangedAt[index]
    return (
      <PatientRow
        key={row.phone}
        row={row}
        practiceID={practice.id}
        active={row.phone === selectedPhone}
        guarded={changedAt !== undefined}
        refused={changedAt !== undefined && refused?.index === index && refused.changedAt === changedAt}
        onSelect={(event) => {
          const byPointer = event.detail > 0
          if (byPointer && changedAt !== undefined && !rowClickAllowed(changedAt, performance.now())) {
            setRefused({ index, changedAt })
            return
          }
          selectTask(row.top)
        }}
      />
    )
  }

  return (
    <>
      <Sidebar className="absolute h-full [&_[data-slot=sidebar-inner]]:bg-canvas">
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
          className="gap-2 overflow-y-auto px-2 pt-0 pb-2"
          onScroll={rememberScroll}
        >
          <div className="space-y-2 px-1 pt-2">
            <PatientScopeToggle
              scope={projection.rail.scope}
              onChange={(scope) => onIntent({ type: "set-rail-view", scope })}
            />
            <PatientFilterChips
              filter={projection.rail.filter}
              counts={patients.counts}
              appointmentsOffered={offersAppointmentChecks(
                practice.locations,
                projection.scope.locationScopeID,
              )}
              onChange={(filter) => onIntent({ type: "set-rail-view", filter })}
            />
          </div>
          <SidebarGroup className="p-0" role="group" aria-label="Patients with open work">
            <p
              role="status"
              data-testid="patient-list-updated"
              className="sticky top-0 z-10 -mt-2 flex h-5 items-center bg-sidebar px-3.5 text-xs leading-4 font-medium text-sidebar-foreground md:bg-canvas"
            >
              {rowChangedAt.some((at) => at !== undefined) && "List just updated"}
            </p>
            <SidebarGroupContent>
              <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
                {patients.items.map(renderPatient)}
                {patients.loading && patients.items.length === 0 && (
                  <RailLoading label="Loading patients" />
                )}
                {!patients.loading && !patients.error && patients.items.length === 0 && (
                  <RailEmpty>Nothing waiting here.</RailEmpty>
                )}
                {patients.error && (
                  <SidebarMenuItem>
                    <WorkspaceWindowFailure
                      message={patients.error}
                      onRetry={() => onIntent({ type: "retry" })}
                    />
                  </SidebarMenuItem>
                )}
                <RailShowMore
                  cursor={patients.nextCursor}
                  loading={patients.loading}
                  onLoadMore={() =>
                    onIntent({ type: "load-more", window: "patients" })
                  }
                />
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>

          <div className="mt-3">
            <CompletedGroup
              title="Recently completed"
              expanded={projection.rail.completedExpanded}
              onToggle={() => onIntent({ type: "toggle-completed" })}
            >
              {completed.items.map((task) => (
                <CompletedTaskRow
                  key={task.id}
                  task={task}
                  active={task.id === selectedTaskID}
                  onSelect={() => selectTask(task)}
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
          <SidebarFooter className="border-t border-sidebar-border p-2">
            {connection === "degraded" && <ProlongedOutageNotice />}
            <p data-testid="patient-work-footer" className="px-2 text-xs leading-5 text-muted-foreground">
              {patientWorkFooter(patients.totals, projection.rail.scope)}
            </p>
            {callingStatus}
          </SidebarFooter>
        )}
      </Sidebar>
    </>
  )
}

const searchDraftDelayMilliseconds = 250

const scrollPersistDelayMilliseconds = 150
