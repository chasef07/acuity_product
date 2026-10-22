import type {
  AgentCallIssueReason,
  AgentCallVerdict,
  AiAppointmentAction,
  AiAppointmentFacts,
  AiAppointmentOutcome,
  CallIntent,
  CallOutcome,
  CallState,
  CallingCall,
  InsuranceCoverage,
  InsuranceOutcome,
  InsurancePlanRule,
  MessageAttachment,
  OperatorAiAnalyticsRange,
  OperatorAiCallIssueOutcome,
  StaffTaskCategory,
  StaffTaskUrgency,
  Task,
} from "./api/generated/types.gen.ts"

export function labelOptions<Value extends string>(labels: Record<Value, string>) {
  return (Object.keys(labels) as Value[]).map((value) => ({ value, label: labels[value] }))
}

export const callStateLabels = {
  PREPARING: "Preparing call",
  RINGING: "Ringing",
  CONNECTING: "Connecting",
  CONNECTED: "Connected",
  VOICEMAIL_GREETING: "Voicemail greeting",
  VOICEMAIL_RECORDING: "Recording voicemail",
  UNANSWERED: "No answer",
  VOICEMAIL: "Voicemail",
  MISSED: "Missed call",
  NEEDS_DISPOSITION: "Outcome needed",
  RESOLVED: "Resolved on call",
  FOLLOW_UP_REQUIRED: "Follow-up needed",
} satisfies Record<CallState, string>

export const callDirectionLabels = {
  INBOUND: "Inbound call",
  OUTBOUND: "Outbound call",
} satisfies Record<CallingCall["direction"], string>

export const callEntryPointLabels = {
  AI_HANDOFF: "AI handoff",
  TASK: "Task",
  STANDALONE: "Phone number",
} satisfies Record<CallingCall["entryPoint"], string>

export const taskUrgencyLabels = {
  high_priority: "High priority",
  normal: "Normal priority",
  non_urgent: "Non-urgent",
} satisfies Record<StaffTaskUrgency, string>

export const taskOriginLabels = {
  HUMAN_CALL_FOLLOW_UP: "Call follow-up",
  ABITA_AI: "Created by AI",
  STAFF_MESSAGE_FOLLOW_UP: "Message follow-up",
  APPOINTMENT_REVIEW: "Appointment verification",
  INBOUND_MESSAGE_REVIEW: "Incoming text",
  VOICEMAIL_RECOVERY: "Voicemail follow-up",
  MISSED_CALL_RECOVERY: "Missed-call follow-up",
} satisfies Record<Task["origin"], string>

const taskCategoryLabels = {
  appointments: "Appointments",
  documentation: "Medical records & documentation",
  medication: "Clinical, medication & pharmacy",
  pre_op: "Pre-op",
  post_op: "Post-op",
  insurance: "Insurance & prior authorizations",
  optical: "Optical, frames & prescriptions",
  referrals: "Referrals",
  other: "Other",
  billing: "Billing (historical)",
} satisfies Record<StaffTaskCategory, string>

export const taskGroups = labelOptions(taskCategoryLabels).filter(({ value }) => value !== "billing")

export function taskGroupLabel(category: StaffTaskCategory | undefined) {
  return category ? taskCategoryLabels[category] : "Uncategorized"
}

export const taskFilterLabels = {
  ...taskCategoryLabels,
  appointments: "Scheduling follow-up",
  documentation: "Medical records",
  medication: "Clinical & pharmacy",
  insurance: "Insurance",
  optical: "Optical",
} satisfies Record<StaffTaskCategory, string>

export const analyticsRangeLabels = {
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  "30d": "Last 30 days",
} satisfies Record<OperatorAiAnalyticsRange, string>

export const appointmentActionLabels = {
  BOOKED: "Booked",
  RESCHEDULED: "Rescheduled",
  CANCELLED: "Cancelled",
} satisfies Record<AiAppointmentAction, string>

export const insuranceCoverageLabels = {
  medical: "Medical",
  routine_vision: "Routine vision",
} satisfies Record<InsuranceCoverage, string>

export const insuranceOutcomeLabels = {
  accepted: "Accepted",
  needs_staff_task: "Staff confirms",
  not_accepted: "Not accepted",
} satisfies Record<InsuranceOutcome, string>

export const insuranceRequirementLabels = {
  prior_authorization: "Prior auth",
  pcp_referral: "PCP referral",
  staff_verify: "Staff verifies",
} satisfies Record<InsurancePlanRule["requirements"][number], string>

export const callIssueOutcomeLabels = {
  CONFIRMED: "Real issue",
  NOT_AN_ISSUE: "Not an issue",
} satisfies Record<OperatorAiCallIssueOutcome, string>

export const appointmentOutcomeTitles = {
  BOOKING: "Appointment booked",
  CANCELLATION: "Appointment cancelled",
  RESCHEDULE: "Appointment rescheduled",
  PARTIAL: "Appointment change needs review",
  INDETERMINATE: "AI call",
} satisfies Record<AiAppointmentOutcome, string>

export const intentLabels = {
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
} satisfies Record<CallIntent, string>

export const outcomeLabels = {
  resolved: "Resolved",
  transferred: "Transferred",
  abandoned: "Abandoned",
} satisfies Record<CallOutcome, string>

export const issueReasons = {
  WRONG_APPOINTMENT_TYPE: "Wrong Appointment Type",
  INSURANCE_ISSUE: "Insurance Issue",
  OTHER: "Other",
} satisfies Record<AgentCallIssueReason, string>

export const verdictLabels = {
  yes: "Yes",
  no: "No",
} satisfies Record<AgentCallVerdict["answer"], string>

export const messageAttachmentTypes = Object.keys({
  "image/jpeg": true,
  "image/png": true,
  "image/gif": true,
  "image/webp": true,
  "application/pdf": true,
} satisfies Record<MessageAttachment["contentType"], true>)

export function hasAppointmentFacts(facts: AiAppointmentFacts) {
  return Object.values(facts).some(Boolean)
}

export function visitLabel(facts: AiAppointmentFacts) {
  if (facts.appointmentTypeName) return facts.appointmentTypeName
  if (facts.careLane === "medical_md") return "Medical"
  if (facts.careLane === "routine_od") return "Routine vision"
  return "—"
}

export function formatDuration(seconds?: number | null) {
  if (seconds == null || !Number.isFinite(seconds)) return "—"
  const total = Math.max(0, Math.round(seconds))
  const hours = Math.floor(total / 3_600)
  const minutes = Math.floor(total / 60) % 60
  const remainder = total % 60
  if (hours) return `${hours}h ${minutes}m ${remainder}s`
  if (minutes) return `${minutes}m ${remainder}s`
  return `${remainder}s`
}

export function formatDurationBetween(startedAt: string, endedAt?: string) {
  if (!endedAt) return "In progress"
  return formatDuration((Date.parse(endedAt) - Date.parse(startedAt)) / 1_000)
}

export function formatClock(seconds?: number) {
  if (seconds === undefined || !Number.isFinite(seconds)) return "—"
  const total = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(total / 3_600)
  const minutes = Math.floor(total / 60) % 60
  const remainder = String(total % 60).padStart(2, "0")
  return hours
    ? `${hours}:${String(minutes).padStart(2, "0")}:${remainder}`
    : `${minutes}:${remainder}`
}

export function formatPhone(value: string) {
  if (/^\s*\+(?!1)/.test(value)) return value
  const digits = value.replace(/\D/g, "")
  const local = digits.length === 11 && digits.startsWith("1") ? digits.slice(1) : digits
  if (local.length !== 10) return value
  return `(${local.slice(0, 3)}) ${local.slice(3, 6)}-${local.slice(6)}`
}

export function formatRelativeTime(value: string, now = Date.now()) {
  const minutes = Math.max(0, Math.floor((now - Date.parse(value)) / 60_000))
  if (minutes < 1) return "now"
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}
