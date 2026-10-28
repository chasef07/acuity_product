"use client"

import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { ChartContainer, ChartTooltip } from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

export type DailyBarSeries = { key: string; label: string; color: string }
export type DailyBarDatum = { date: string } & Record<string, string | number | null>

export function DailyBarChart({ label, data, series, format = (value) => value.toLocaleString(), percent = false }: {
  label: string
  data: DailyBarDatum[]
  series: DailyBarSeries[]
  format?: (value: number) => string
  percent?: boolean
}) {
  const config = Object.fromEntries(series.map((item) => [item.key, { label: item.label, color: item.color }]))
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <ChartContainer config={config} className="h-72 w-full aspect-auto" role="img" aria-label={label}>
        <BarChart accessibilityLayer data={data} margin={{ top: 12, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" tickFormatter={(date: string) => date.slice(5).replace("-", "/")} tickLine={false} axisLine={false} minTickGap={24} />
          <YAxis tickLine={false} axisLine={false} width={46} allowDecimals={percent} domain={percent ? [0, 1] : [0, "auto"]} tickFormatter={format} />
          <ChartTooltip
            content={({ active, payload, label: date }) => active && payload?.length ? (
              <div className="rounded-lg border bg-popover p-3 text-xs text-popover-foreground shadow-md">
                <p className="mb-2 font-medium">{String(date)} · Eastern</p>
                <dl className="flex flex-col gap-1.5">
                  {payload.map((item) => <div key={String(item.dataKey)} className="flex justify-between gap-6"><dt>{config[String(item.dataKey)]?.label}</dt><dd className="font-medium tabular-nums">{typeof item.value === "number" ? format(item.value) : "—"}</dd></div>)}
                </dl>
              </div>
            ) : null}
          />
          {series.map((item) => <Bar key={item.key} dataKey={item.key} name={item.label} stackId="daily" fill={`var(--color-${item.key})`} maxBarSize={44} isAnimationActive={false} />)}
        </BarChart>
      </ChartContainer>
      {series.length > 1 && <ul aria-label="Chart legend" className="flex flex-wrap gap-x-4 gap-y-2 text-[11px] text-muted-foreground">
        {series.map((item) => <li key={item.key} className="flex items-center gap-1.5"><span aria-hidden="true" className="size-2 rounded-sm" style={{ backgroundColor: item.color }} />{item.label}</li>)}
      </ul>}
      <p className="text-[11px] text-muted-foreground">Daily values · Eastern time · First and last days may be partial.</p>
      <details>
        <summary className="cursor-pointer text-xs text-muted-foreground">Daily data</summary>
        <Table>
          <TableHeader><TableRow><TableHead>Date · Eastern</TableHead>{series.map((item) => <TableHead key={item.key}>{item.label}</TableHead>)}</TableRow></TableHeader>
          <TableBody>{data.map((day) => <TableRow key={day.date}><TableCell>{day.date}</TableCell>{series.map((item) => <TableCell key={item.key}>{typeof day[item.key] === "number" ? format(day[item.key] as number) : "—"}</TableCell>)}</TableRow>)}</TableBody>
        </Table>
      </details>
    </div>
  )
}
