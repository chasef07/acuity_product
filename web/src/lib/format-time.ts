import type { AiAppointmentFacts } from "./api/generated/types.gen.ts"

export function formatTimestamp(value: string, now = new Date()) {
  if (Number.isNaN(new Date(value).getTime())) return "Unknown time"
  return `${formatDate(value, now)}, ${formatClockTime(value)}`
}

export function formatDate(value: string, now = new Date()) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown date"
  if (sameLocalDay(date, now)) return "Today"

  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  if (sameLocalDay(date, yesterday)) return "Yesterday"

  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    ...(date.getFullYear() === now.getFullYear() ? {} : { year: "numeric" }),
  }).format(date)
}

export function formatClockTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "Unknown time"
  return new Intl.DateTimeFormat(undefined, {
    hour: "numeric",
    minute: "2-digit",
  }).format(date)
}

export function sameLocalDay(left: Date, right: Date) {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  )
}

export function formatAppointmentDateTime(facts: AiAppointmentFacts) {
  if (facts.startDatetime) {
    const value = new Date(facts.startDatetime)
    if (!Number.isNaN(value.getTime())) {
      return new Intl.DateTimeFormat(undefined, {
        dateStyle: "long",
        timeStyle: "short",
      }).format(value)
    }
  }
  if (!facts.appointmentDate) return facts.appointmentTime
  const match = facts.appointmentDate.match(/^(\d{4})-(\d{2})-(\d{2})$/)
  const date = match
    ? new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
    : new Date(facts.appointmentDate)
  const dateLabel = Number.isNaN(date.getTime())
    ? facts.appointmentDate
    : new Intl.DateTimeFormat(undefined, { dateStyle: "long" }).format(date)
  return facts.appointmentTime
    ? `${dateLabel} · ${facts.appointmentTime}`
    : dateLabel
}
