"use client"

import type { ComponentProps, ReactNode } from "react"
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts"
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { formatDay } from "@/lib/booking-analytics"

export type DailyBarSeries = { key: string; label: string; color: string }
export type DailyBarDatum = { date: string } & Record<string, string | number | null>

export function DailyChartFrame({
  label,
  config,
  data,
  dataKey = "day",
  yAxis,
  tooltip,
  role,
  children,
}: {
  label: string
  config: ChartConfig
  data: object[]
  dataKey?: string
  yAxis: ComponentProps<typeof YAxis>
  tooltip: ComponentProps<typeof ChartTooltip>["content"]
  role?: "img"
  children: ReactNode
}) {
  return (
    <ChartContainer config={config} className="h-[300px] w-full aspect-auto" role={role} aria-label={label}>
      <BarChart accessibilityLayer data={data} margin={{ top: 20, right: 24, bottom: 8, left: 8 }}>
        <CartesianGrid vertical={false} stroke="var(--border)" strokeOpacity={0.65} />
        <XAxis
          dataKey={dataKey}
          height={40}
          tickFormatter={formatDay}
          axisLine={false}
          tickLine={false}
          tickMargin={14}
          minTickGap={48}
        />
        <YAxis axisLine={false} tickLine={false} width="auto" tickMargin={10} {...yAxis} />
        <ChartTooltip content={tooltip} cursor={{ stroke: "var(--muted-foreground)", strokeWidth: 1 }} />
        {children}
      </BarChart>
    </ChartContainer>
  )
}

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
      <DailyChartFrame
        role="img"
        label={label}
        config={config}
        data={data}
        dataKey="date"
        yAxis={{ allowDecimals: percent, domain: percent ? [0, 1] : [0, "auto"], tickFormatter: format }}
        tooltip={({ active, payload, label: date }) => active && payload?.length ? (
          <div className="rounded-lg border bg-popover p-3 text-xs text-popover-foreground shadow-md">
            <p className="mb-2 font-medium">{String(date)} · Eastern</p>
            <dl className="flex flex-col gap-1.5">
              {payload.map((item) => <div key={String(item.dataKey)} className="flex justify-between gap-6"><dt>{config[String(item.dataKey)]?.label}</dt><dd className="font-medium tabular-nums">{typeof item.value === "number" ? format(item.value) : "—"}</dd></div>)}
            </dl>
          </div>
        ) : null}
      >
        {series.map((item) => <Bar key={item.key} dataKey={item.key} name={item.label} stackId="daily" fill={`var(--color-${item.key})`} maxBarSize={44} isAnimationActive={false} />)}
      </DailyChartFrame>
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
