"use client"

import { useId } from "react"
import { Building2Icon, CheckIcon, PhoneIcon } from "lucide-react"

import { SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import type { Task } from "@/lib/api/generated/types.gen"
import {
  formatPhone,
  formatRelativeTime,
  taskUrgencyLabels,
} from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

export function TaskRow({
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
    ? (task.callerName ?? formatPhone(task.phone))
    : task.title
  const groupCount = task.groupMembers?.length ?? 0
  const grouped = groupCount > 1
  const urgent = task.urgency === "high_priority"
  const urgencyID = useId()
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
        meta={`${taskUrgencyLabels[task.urgency]} · Updated ${formatRelativeTime(taskRelativeAt(task))}`}
      >
        <SidebarMenuButton
          aria-label={
            grouped ? `${rowTitle}, ${groupCount} requests` : rowTitle
          }
          aria-describedby={urgent ? urgencyID : undefined}
          isActive={active}
          className="h-8 rounded-md px-2 pr-9 text-sidebar-foreground"
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
      <span className="pointer-events-none absolute top-0 right-1 h-8 w-7 [@media(pointer:coarse)]:h-11">
        <time
          className={`absolute inset-0 flex items-center justify-center text-[10px] font-normal tabular-nums text-muted-foreground transition-opacity duration-150 ${grouped ? "" : "group-hover/task:opacity-0 group-focus-within/task:opacity-0"} motion-reduce:duration-0 motion-reduce:transition-none`}
          dateTime={taskRelativeAt(task)}
        >
          {formatRelativeTime(taskRelativeAt(task))}
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
            value={formatPhone(phone)}
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

function taskRelativeAt(task: Task) {
  return task.state === "OPEN"
    ? task.createdAt
    : (task.completedAt ?? task.updatedAt)
}
