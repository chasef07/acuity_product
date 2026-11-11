"use client"

import { useId } from "react"
import { Building2Icon, PhoneIcon, PlayIcon } from "lucide-react"

import { SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { PatientWorkRow, Task } from "@/lib/api/generated/types.gen"
import { useEngagementSummary } from "@/lib/clients/engagement"
import { patientRowName, patientRowReason } from "@/lib/patient-work"
import {
  formatPhone,
  formatRelativeTime,
  taskCloseSummary,
  taskRelativeTime,
  taskUrgencyLabels,
} from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

const hoverItemLimit = 5

export function PatientRow({
  row,
  practiceID,
  active,
  onSelect,
}: {
  row: PatientWorkRow
  practiceID: string
  active: boolean
  onSelect: () => void
}) {
  const priorityID = useId()
  const name = patientRowName(row)
  const reason = patientRowReason(row)
  return (
    <SidebarMenuItem
      data-testid="patient-row"
      data-phone={row.phone}
      data-task-id={row.top.id}
      className="relative"
    >
      <RailHoverDetails
        eyebrow={`${row.openCount} open`}
        title={name}
        phone={row.phone}
        office={row.top.locationName}
        body={<PatientOpenItems practiceID={practiceID} row={row} />}
        meta={`Newest activity ${formatRelativeTime(row.activityAt)}`}
      >
        <SidebarMenuButton
          aria-describedby={row.highPriority ? priorityID : undefined}
          isActive={active}
          className="h-auto rounded-md px-2 py-1.5 pr-9 text-sidebar-foreground"
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="flex min-w-0 items-center gap-1.5 text-sm leading-5">
              {row.highPriority && (
                <span className="size-1.5 shrink-0 rounded-full bg-destructive">
                  <span id={priorityID} className="sr-only">High priority</span>
                </span>
              )}
              <span data-testid="patient-row-name" className="truncate">{name}</span>
              {row.openCount > 1 && (
                <span
                  data-testid="patient-row-count"
                  title={`${row.openCount} open for this patient`}
                  className="shrink-0 rounded-full bg-sidebar-accent px-1.5 text-[10px] leading-4 tabular-nums text-muted-foreground"
                >
                  {row.openCount}
                  <span className="sr-only"> open</span>
                </span>
              )}
            </span>
            <span
              data-testid="patient-row-reason"
              className="flex min-w-0 items-center gap-1 text-xs leading-4 text-muted-foreground"
            >
              {reason.voicemail && (
                <PlayIcon aria-hidden="true" className="size-3 shrink-0 fill-current" />
              )}
              <span className="truncate">{reason.text}</span>
            </span>
          </span>
        </SidebarMenuButton>
      </RailHoverDetails>
      <RowTime at={row.activityAt} />
    </SidebarMenuItem>
  )
}

export function CompletedTaskRow({
  task,
  active,
  onSelect,
}: {
  task: Task
  active: boolean
  onSelect: () => void
}) {
  const textReview = task.origin === "INBOUND_MESSAGE_REVIEW"
  const rowTitle = textReview
    ? (task.callerName ?? formatPhone(task.phone))
    : task.title
  const urgent = task.urgency === "high_priority"
  const urgencyID = useId()
  const relative = taskRelativeTime(task)
  const closed = taskCloseSummary(task)
  const detail = textReview ? task.preview : closed
  return (
    <SidebarMenuItem
      data-testid="completed-task-row"
      data-task-id={task.id}
      className="relative"
    >
      <RailHoverDetails
        eyebrow="Task"
        title={textReview && task.preview ? task.preview : task.title}
        phone={task.phone}
        office={task.locationName}
        body={textReview && closed ? (
          <p className="truncate border-t border-border px-4 py-2.5 text-xs leading-4 text-popover-foreground">
            {closed}
          </p>
        ) : undefined}
        meta={`${taskUrgencyLabels[task.urgency]} · ${relative.label} ${formatRelativeTime(relative.at)}`}
      >
        <SidebarMenuButton
          aria-label={rowTitle}
          aria-describedby={urgent ? urgencyID : undefined}
          isActive={active}
          className={cn(
            "rounded-md px-2 pr-9 text-sidebar-foreground",
            detail ? "h-auto py-1.5" : "h-8",
          )}
          onClick={onSelect}
        >
          <span className="flex min-w-0 flex-1 flex-col gap-0.5">
            <span className="flex min-w-0 items-center gap-1.5 text-sm leading-5">
              {urgent && (
                <span className="size-1.5 shrink-0 rounded-full bg-destructive">
                  <span id={urgencyID} className="sr-only">Urgent</span>
                </span>
              )}
              <span className="truncate">{rowTitle}</span>
            </span>
            {detail && (
              <span className="truncate text-xs leading-4 text-muted-foreground">
                {detail}
              </span>
            )}
          </span>
        </SidebarMenuButton>
      </RailHoverDetails>
      <RowTime at={relative.at} />
    </SidebarMenuItem>
  )
}

function RowTime({ at }: { at: string }) {
  return (
    <time
      className="pointer-events-none absolute top-0 right-1 flex h-8 w-7 items-center justify-center text-[10px] font-normal tabular-nums text-muted-foreground [@media(pointer:coarse)]:h-11"
      dateTime={at}
    >
      {formatRelativeTime(at)}
    </time>
  )
}

function PatientOpenItems({
  practiceID,
  row,
}: {
  practiceID: string
  row: PatientWorkRow
}) {
  const summary = useEngagementSummary({ practiceID, phone: row.phone })
  const tasks = summary.status === "ready" ? summary.data.openTasks : [row.top]
  const more = Math.max(
    0,
    (summary.status === "ready" ? summary.data.openTaskCount : row.openCount) -
      Math.min(tasks.length, hoverItemLimit),
  )
  return (
    <ul className="grid gap-1 border-t border-border px-4 py-2.5">
      {tasks.slice(0, hoverItemLimit).map((task) => (
        <li
          key={task.id}
          className="flex min-w-0 items-baseline gap-2 text-xs leading-4 text-popover-foreground"
        >
          <span className="min-w-0 flex-1 truncate">
            {patientRowReason({ top: task }).text}
          </span>
          <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">
            {formatRelativeTime(taskRelativeTime(task).at)}
          </span>
        </li>
      ))}
      {more > 0 && (
        <li className="text-xs leading-4 text-muted-foreground">{more} more</li>
      )}
    </ul>
  )
}

function RailHoverDetails({
  eyebrow,
  title,
  phone,
  office,
  body,
  meta,
  children,
}: {
  eyebrow: string
  title: string
  phone: string
  office: string
  body?: React.ReactNode
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
            value={formatPhone(phone)}
            tabular
          />
          <HoverDetail icon={<Building2Icon />} label="Office" value={office} />
        </div>
        {body}
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
