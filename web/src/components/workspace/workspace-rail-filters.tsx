"use client"

import { Badge } from "@/components/ui/badge"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type {
  PatientWorkCounts,
  PatientWorkFilter,
  PatientWorkScope,
} from "@/lib/api/generated/types.gen"
import { shownPatientWorkFilters } from "@/lib/patient-work"
import { labelOptions, patientWorkScopeLabels } from "@/lib/vocabulary"
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
      aria-label="Whose work"
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
  filter: PatientWorkFilter
  counts: PatientWorkCounts
  appointmentsOffered: boolean
  onChange: (filter: PatientWorkFilter) => void
}) {
  return (
    <div role="group" aria-label="Filter patients" className="flex flex-wrap gap-1">
      {shownPatientWorkFilters(counts, filter, appointmentsOffered).map((option) => {
        const pressed = option.value === filter
        const unowned = option.value === "unowned"
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
            variant={unowned ? "destructive" : pressed ? "secondary" : "outline"}
            className={cn(
              "cursor-pointer rounded-full px-2 font-normal outline-hidden focus-visible:ring-2 focus-visible:ring-sidebar-ring",
              unowned && pressed && "border-destructive/40",
            )}
          >
            {option.label}
            <span className="tabular-nums opacity-70">{option.count}</span>
          </Badge>
        )
      })}
    </div>
  )
}
