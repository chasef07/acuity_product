const reviewCutoffHour = 16

export function latestReviewDay(now = new Date()): string {
  const parts = Object.fromEntries(
    new Intl.DateTimeFormat("en-US", { timeZone: "America/New_York", year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", hourCycle: "h23" })
      .formatToParts(now)
      .map((part) => [part.type, part.value]),
  )
  const opened = Number(parts.hour) >= reviewCutoffHour
  return new Date(Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day) - (opened ? 0 : 1))).toISOString().slice(0, 10)
}
