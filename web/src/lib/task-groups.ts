import type { StaffTaskCategory } from "./api/generated/types.gen"

export const taskGroups: { value: StaffTaskCategory; label: string }[] = [
  { value: "appointments", label: "Appointments" },
  { value: "documentation", label: "Medical records & documentation" },
  { value: "medication", label: "Clinical, medication & pharmacy" },
  { value: "pre_op", label: "Pre-op" },
  { value: "post_op", label: "Post-op" },
  { value: "insurance", label: "Insurance & prior authorizations" },
  { value: "optical", label: "Optical, frames & prescriptions" },
  { value: "referrals", label: "Referrals" },
  { value: "other", label: "Other" },
]

export function taskGroupLabel(category: StaffTaskCategory | undefined) {
  return taskGroups.find((group) => group.value === category)?.label ??
    (category === "billing" ? "Billing (historical)" : "Uncategorized")
}
