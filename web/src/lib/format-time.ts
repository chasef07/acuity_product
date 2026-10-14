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
