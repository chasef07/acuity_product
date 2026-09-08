export type ReviewPatientStatus = "new" | "existing" | "unknown"
export type PatientStatusOverrides = Record<string, ReviewPatientStatus>

export function isReviewPatientStatus(
  value: unknown,
): value is ReviewPatientStatus {
  return value === "new" || value === "existing" || value === "unknown"
}

export function readPatientStatusOverrides(
  raw: string | null,
): PatientStatusOverrides {
  if (!raw) return {}
  const value: unknown = JSON.parse(raw)
  if (
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    !Object.values(value).every(isReviewPatientStatus)
  )
    throw new Error("Invalid saved patient status corrections")
  return value as PatientStatusOverrides
}

export function patientStatusLabel(status: ReviewPatientStatus): string {
  return status === "new"
    ? "New"
    : status === "existing"
      ? "Existing"
      : "Unknown"
}
