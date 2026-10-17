import type { CallIntent, CallOutcome, CallLabelingStatus } from "@/lib/api/generated/types.gen"
import { Badge } from "@/components/ui/badge"
import { OptionSelect } from "./manage-agent-page"

const intentLabels: Record<CallIntent, string> = {
  new_patient_booking: "New patient booking",
  existing_patient_booking: "Existing patient booking",
  reschedule_appointment: "Reschedule appointment",
  cancel_appointment: "Cancel appointment",
  appointment_information: "Appointment confirmation/details",
  office_information: "Office information",
  insurance_question: "Insurance question",
  billing_question: "Billing question",
  prescription_or_refill: "Prescription/refill",
  records_or_forms: "Records/forms",
  glasses_or_contacts: "Glasses/contact lenses",
  returning_call: "Returning a missed call",
  speak_to_staff: "Speak to staff",
  other: "Other",
}

const outcomeLabels: Record<CallOutcome, string> = {
  resolved: "Resolved",
  transferred: "Transferred",
  abandoned: "Abandoned",
}

type Labels = {
  intent?: CallIntent
  outcome?: CallOutcome
  labelingStatus?: CallLabelingStatus
}
export type IntentFilter = CallIntent | "all"
export type OutcomeFilter = CallOutcome | "all"

const intents: { value: IntentFilter; label: string }[] = [
  { value: "all", label: "All intents" },
  ...Object.entries(intentLabels).map(([value, label]) => ({ value: value as CallIntent, label })),
]
const outcomes: { value: OutcomeFilter; label: string }[] = [
  { value: "all", label: "All outcomes" },
  ...Object.entries(outcomeLabels).map(([value, label]) => ({ value: value as CallOutcome, label })),
]

export function CallLabelFilters({ intent, outcome, onIntent, onOutcome }: {
  intent: IntentFilter
  outcome: OutcomeFilter
  onIntent: (intent: IntentFilter) => void
  onOutcome: (outcome: OutcomeFilter) => void
}) {
  return (
    <>
      <OptionSelect label="Intent" value={intent} items={intents} onChange={onIntent} />
      <OptionSelect label="Outcome" value={outcome} items={outcomes} onChange={onOutcome} />
    </>
  )
}

export function CallLabel({ call, kind }: { call: Labels; kind: "intent" | "outcome" }) {
  const label = kind === "intent"
    ? call.intent && intentLabels[call.intent]
    : call.outcome && outcomeLabels[call.outcome]
  if (label) return <Badge variant="secondary">{label}</Badge>
  const state = call.labelingStatus
  const missing = state === "pending" || state === "processing" ? "Pending"
    : state ? "Unavailable" : "Not labeled"
  return <span className="text-muted-foreground">{missing}</span>
}

export function CallLabels({ call }: { call: Labels }) {
  return (
    <dl className="flex flex-wrap gap-x-5 gap-y-2 text-xs">
      <div className="flex items-center gap-2">
        <dt>Intent</dt><dd><CallLabel call={call} kind="intent" /></dd>
      </div>
      <div className="flex items-center gap-2">
        <dt>Outcome</dt><dd><CallLabel call={call} kind="outcome" /></dd>
      </div>
    </dl>
  )
}
