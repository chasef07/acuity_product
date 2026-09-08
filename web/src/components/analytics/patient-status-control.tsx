"use client"

import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  isReviewPatientStatus,
  patientStatusLabel,
  type ReviewPatientStatus,
} from "@/lib/call-patient-status"

export function PatientStatusControl({
  original,
  correction,
  label,
  onChange,
}: {
  original: ReviewPatientStatus
  correction?: ReviewPatientStatus
  label: string
  onChange: (value: ReviewPatientStatus) => void
}) {
  return (
    <div className="space-y-1.5" onClick={(event) => event.stopPropagation()}>
      <Select
        value={correction ?? original}
        onValueChange={(value) => {
          if (isReviewPatientStatus(value)) onChange(value)
        }}
        items={[
          { value: "new", label: "New" },
          { value: "existing", label: "Existing" },
          { value: "unknown", label: "Unknown" },
        ]}
      >
        <SelectTrigger aria-label={label} className="w-36">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="new">New</SelectItem>
          <SelectItem value="existing">Existing</SelectItem>
          <SelectItem value="unknown">Unknown</SelectItem>
        </SelectContent>
      </Select>
      {correction && correction !== original && (
        <div className="text-xs text-muted-foreground">
          <p>Original: {patientStatusLabel(original)} · local correction</p>
          <Button
            variant="link"
            size="sm"
            className="h-auto px-0 py-0 text-xs"
            onClick={() => onChange(original)}
          >
            Use original
          </Button>
        </div>
      )}
    </div>
  )
}
