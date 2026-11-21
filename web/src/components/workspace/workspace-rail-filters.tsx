"use client"

import { Badge } from "@/components/ui/badge"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { PatientWorkCounts, PatientWorkScope } from "@/lib/api/generated/types.gen"
import { shownPatientWorkFilters } from "@/lib/patient-work"
import { labelOptions, patientWorkScopeLabels, type RailFilter } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

const patientWorkScopes = labelOptions(patientWorkScopeLabels)

export function PatientScopeToggle({
  scope,
  onChange,
}: {
  scope: PatientWorkScope
  onChange: (scope: PatientWorkScope) => void
}) {
  return (
    <ToggleGroup
      aria-label="Whose patients"
      variant="segmented"
      size="sm"
      spacing={0}
      className="w-full"
      value={[scope]}
      onValueChange={(values) => {
        const next = patientWorkScopes.find((option) => option.value === values[0])
        if (next) onChange(next.value)
      }}
    >
      {patientWorkScopes.map((option) => (
        <ToggleGroupItem
          key={option.value}
          value={option.value}
          className="h-7 flex-1 text-xs data-pressed:bg-background data-pressed:shadow-xs"
        >
          {option.label}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
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
