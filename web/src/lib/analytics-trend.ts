// Callers supply complete daily buckets in date order. Compare equal adjacent
// groups within the selection; omit the oldest day when the count is odd.
export function dailyTotalComparison(values: readonly (number | null)[]) {
  const days = Math.floor(values.length / 2)
  if (!days) return "Choose a longer range to compare complete days."
  const periods = values.slice(-days * 2)
  if (periods.some((value) => value === null))
    return "Trend unavailable: some complete days have missing usage."
  const previous = periods.slice(0, days).reduce<number>((sum, value) => sum + value!, 0)
  const current = periods.slice(days).reduce<number>((sum, value) => sum + value!, 0)
  const period = `Last ${days} complete ${days === 1 ? "day" : "days"} vs preceding ${days}`
  const roundingError = Number.EPSILON * periods.length * Math.max(current, previous)
  if (Math.abs(current - previous) <= roundingError)
    return `Unchanged · ${period}`
  if (previous === 0) return `Up from zero · ${period}`
  const change = Math.abs((current - previous) / previous * 100)
  const percent = change < 0.1 ? "<0.1" : change.toFixed(1)
  return `${current > previous ? "Up" : "Down"} ${percent}% · ${period}`
}
