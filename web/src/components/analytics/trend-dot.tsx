// A line cannot show a value when both adjacent days are missing.
export function trendDot(values: (number | null)[], color: string) {
  return function TrendDot({
    cx,
    cy,
    index,
  }: {
    cx?: number
    cy?: number
    index?: number
  }) {
    if (
      index === undefined ||
      values[index] == null ||
      values[index - 1] != null ||
      values[index + 1] != null
    )
      return <g />
    return <circle cx={cx} cy={cy} r={3} fill={color} stroke="none" />
  }
}
