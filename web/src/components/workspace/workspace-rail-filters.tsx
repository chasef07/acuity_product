"use client"

import { ListFilterIcon, XIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SidebarMenuItem } from "@/components/ui/sidebar"
import type { TaskFolderCounts } from "@/lib/api/generated/types.gen"
import { taskFilterLabels, taskGroups } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"
import {
  taskCountForCategory,
  type TaskCategoryFilter,
  type TaskResponsibility,
} from "@/lib/workspace-triage"

const taskCategoryOptions: Array<{ value: TaskCategoryFilter; label: string }> =
  [
    { value: "all", label: "All categories" },
    ...taskGroups.map(({ value }) => ({ value, label: taskFilterLabels[value] })),
  ]

export function TaskResponsibilityToggle({
  responsibility,
  onChange,
}: {
  responsibility: TaskResponsibility
  onChange: (value: TaskResponsibility) => void
}) {
  return (
    <ToggleGroup
      aria-label="Task view"
      variant="segmented"
      size="sm"
      spacing={0}
      className="flex-1"
      value={[responsibility]}
      onValueChange={(values) => {
        const value = values[0]
        if (value === "mine" || value === "all") onChange(value)
      }}
    >
      <ToggleGroupItem value="mine" aria-label="Show My Tasks" className="h-7 flex-1 text-xs data-pressed:bg-background data-pressed:shadow-xs">
        Mine
      </ToggleGroupItem>
      <ToggleGroupItem value="all" aria-label="Show All Tasks" className="h-7 flex-1 text-xs data-pressed:bg-background data-pressed:shadow-xs">
        All
      </ToggleGroupItem>
    </ToggleGroup>
  )
}

export function TaskCategoryMenu({
  category,
  responsibility,
  counts,
  onCategoryChange,
}: {
  category: TaskCategoryFilter
  responsibility: TaskResponsibility
  counts: TaskFolderCounts
  onCategoryChange: (value: TaskCategoryFilter) => void
}) {
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
            aria-label={`Filter ${viewLabel}${category === "all" ? "" : ` · ${taskFilterLabels[category]}`}`}
            title="Filter by type"
            className={cn("size-8 shrink-0 p-0 text-muted-foreground", category !== "all" && "text-foreground")}
          />
        }
      >
        <ListFilterIcon aria-hidden="true" className="size-3.5 shrink-0" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-64">
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

export function TaskCategoryChip({
  category,
  onClear,
}: {
  category: TaskCategoryFilter
  onClear: () => void
}) {
  if (category === "all") return null
  const label = taskFilterLabels[category]
  return (
    <SidebarMenuItem className="px-1 pb-1">
      <span className="inline-flex h-6 items-center gap-1 rounded-full bg-sidebar-accent pr-1 pl-2.5 text-xs text-sidebar-foreground">
        {label}
        <button
          type="button"
          aria-label={`Clear ${label} filter`}
          className="flex size-4 items-center justify-center rounded-full text-muted-foreground hover:bg-background hover:text-foreground"
          onClick={onClear}
        >
          <XIcon aria-hidden="true" className="size-3" />
        </button>
      </span>
    </SidebarMenuItem>
  )
}
