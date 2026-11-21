"use client"

import { ChevronDown } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { PatientWorkCounts, StaffTaskCategory } from "@/lib/api/generated/types.gen"
import { shownPatientWorkFilters } from "@/lib/patient-work"
import { taskGroupLabel, taskGroups, type RailFilter } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

function groupPickerLabel(groups: StaffTaskCategory[]) {
  if (groups.length === 0) return "All groups"
  const first = taskGroupLabel(groups[0])
  return groups.length === 1 ? first : `${first} +${groups.length - 1}`
}

export function PatientGroupPicker({
  groups,
  onChange,
}: {
  groups: StaffTaskCategory[]
  onChange: (groups: StaffTaskCategory[]) => void
}) {
  const toggle = (group: StaffTaskCategory, checked: boolean) =>
    onChange(taskGroups.map(({ value }) => value).filter((value) =>
      value === group ? checked : groups.includes(value)))
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            size="xs"
            variant={groups.length > 0 ? "secondary" : "ghost"}
            data-testid="group-picker"
            className="-ml-1 h-6 max-w-full min-w-0 px-1.5 text-xs font-normal"
          />
        }
      >
        <span className="truncate">{groupPickerLabel(groups)}</span>
        <ChevronDown aria-hidden="true" />
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Show Tasks in these groups</DropdownMenuLabel>
          {taskGroups.map((group) => (
            <DropdownMenuCheckboxItem
              key={group.value}
              checked={groups.includes(group.value)}
              closeOnClick={false}
              onCheckedChange={(checked) => toggle(group.value, checked)}
            >
              {group.label}
            </DropdownMenuCheckboxItem>
          ))}
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem disabled={groups.length === 0} onClick={() => onChange([])}>
          Show all groups
        </DropdownMenuItem>
        <p className="px-2 pt-1 pb-1.5 text-xs text-muted-foreground">
          Calls, voicemails, texts and Tasks with no group always show.
        </p>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function PatientFilterChips({
  filter,
  counts,
  appointmentsOffered,
  onChange,
}: {
  filter: RailFilter
  counts: PatientWorkCounts
  appointmentsOffered: boolean
  onChange: (filter: RailFilter) => void
}) {
  return (
    <div role="group" aria-label="Filter patients" className="flex min-h-12 flex-wrap content-start gap-1 [@media(pointer:coarse)]:min-h-24">
      {shownPatientWorkFilters(counts, filter, appointmentsOffered).map((option) => {
        const pressed = option.value === filter
        return (
          <Badge
            key={option.value}
            render={
              <button
                type="button"
                data-filter={option.value}
                aria-pressed={pressed}
                onClick={() => onChange(option.value)}
              />
            }
            variant={pressed ? "default" : "outline"}
            className={cn(
              "cursor-pointer rounded-full px-2 font-normal outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring",
              pressed && "font-medium",
            )}
          >
            {option.label}
            <span className={cn("tabular-nums", !pressed && "opacity-70")}>{option.count}</span>
          </Badge>
        )
      })}
    </div>
  )
}
