"use client"

import { Badge } from "@/components/ui/badge"
import type { PatientWorkCounts } from "@/lib/api/generated/types.gen"
import { shownPatientWorkFilters } from "@/lib/patient-work"
import type { RailFilter } from "@/lib/vocabulary"
import { cn } from "@/lib/utils"

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
