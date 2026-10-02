"use client"

import { useState } from "react"
import { ArrowUpRightIcon } from "lucide-react"
import {
  Bar,
  ComposedChart,
  CartesianGrid,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import type {
  OperatorAiAnalyticsSummary,
  OperatorAiDiagnosticExample,
  OperatorAiToolDiagnostics,
} from "@/lib/api/generated/types.gen"
import styles from "./ai-diagnostics.module.css"
import {
  VersionLane,
  versionGroups,
  versionReferenceLines,
  type VersionView,
} from "./version-markers"

export type DiagnosticFocus = { itemID?: string; callID?: string }
export type SelectDiagnostic = (
  interactionID: string,
  focus?: DiagnosticFocus,
) => void
export function latencyLabel(value?: number): string {
  if (value === undefined) return "—"
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value} ms`
}
function ToolName({ name }: { name: string }) {
  return <span className={styles.toolName}>{name.replaceAll("_", " ")}</span>
}
function Evidence({
  examples,
  onSelect,
  title,
  note,
}: {
  examples: OperatorAiDiagnosticExample[]
  onSelect: SelectDiagnostic
  title: string
  note: string
}) {
  return (
    <section className={styles.evidence} aria-label={title}>
      <div className={styles.sectionHeading}>
        <h3>{title}</h3>
        <span>{note}</span>
      </div>
      {examples.length === 0 ? (
        <p className={styles.empty}>No recorded samples in this selection.</p>
      ) : (
        <div className={styles.evidenceGrid}>
          {examples.map((sample, index) => (
            <button
              key={`${sample.interactionId}:${sample.itemId}:${sample.callId}:${index}`}
              className={styles.example}
              onClick={() =>
                onSelect(sample.interactionId, {
                  itemID: sample.itemId,
                  callID: sample.callId,
                })
              }
            >
              <div>
                <strong>{latencyLabel(sample.durationMs)}</strong>
                <span>
                  {new Date(sample.startedAt).toLocaleString(undefined, {
                    month: "short",
                    day: "numeric",
                    hour: "numeric",
                    minute: "2-digit",
                  })}
                </span>
              </div>
              <div>
                {sample.status === "ERROR" && (
                  <span className={styles.error}>Error</span>
                )}
                <ArrowUpRightIcon size={14} aria-hidden="true" />
              </div>
            </button>
          ))}
        </div>
      )}
    </section>
  )
}
const minutesLabel = (minutes: number, digits = 0) =>
  `${minutes.toLocaleString(undefined, { maximumFractionDigits: digits })} min`

export function DiagnosticsCallTrends({
  summary,
  versionView,
}: {
  summary: OperatorAiAnalyticsSummary
  versionView?: VersionView
}) {
  const versionMarkers = versionGroups(
    versionView,
    summary.daily.map((day) => day.date),
    false,
  )
  return (
    <div className={styles.callTrends}>
      {(["volume", "minutes", "transfers"] as const).map((metric) => {
        const transfers = metric === "transfers"
        const minutes = metric === "minutes"
        const title = transfers
          ? "Transfer rate over time"
          : minutes
            ? "Call minutes over time"
            : "Call volume over time"
        return (
          <section key={metric} aria-label={title} className={styles.callTrend}>
            <div className={styles.sectionHeading}>
              <h2>{title}</h2>
              <span>
                {transfers
                  ? "Transferred / all AI calls"
                  : minutes
                    ? "Completed AI call minutes per day"
                    : "AI calls per day"}
              </span>
            </div>
            <strong className={styles.secondaryHeadline}>
              {transfers
                ? summary.totalCalls
                  ? `${(summary.transferRate * 100).toFixed(1)}%`
                  : "—"
                : minutes
                  ? minutesLabel(summary.totalCallMinutes)
                  : summary.totalCalls.toLocaleString()}
            </strong>
            <p className={styles.caption}>
              {transfers
                ? `${summary.transferCount.toLocaleString()} of ${summary.totalCalls.toLocaleString()} calls transferred`
                : minutes && summary.totalCalls
                  ? `${(summary.totalCallMinutes / summary.totalCalls).toFixed(1)} min average per call`
                  : "Total in selected range"}
            </p>
            <div
              className={styles.trend}
              role="img"
              aria-label={`${title}, daily UTC buckets`}
            >
              <ResponsiveContainer width="100%" height="100%">
                <ComposedChart
                  data={summary.daily}
                  margin={{ top: 24, right: 12, bottom: 0, left: 0 }}
                >
                  <CartesianGrid
                    vertical={false}
                    stroke="var(--border)"
                    strokeDasharray="3 4"
                  />
                  <XAxis
                    dataKey="date"
                    tickFormatter={(date: string) =>
                      date.slice(5).replace("-", "/")
                    }
                    tickLine={false}
                    axisLine={false}
                    minTickGap={30}
                    tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                  />
                  {versionReferenceLines(versionMarkers)}
                  <YAxis
                    domain={transfers ? [0, 1] : [0, "auto"]}
                    allowDecimals={transfers}
                    tickFormatter={
                      transfers
                        ? (value: number) => `${Math.round(value * 100)}%`
                        : undefined
                    }
                    width={44}
                    tickLine={false}
                    axisLine={false}
                    tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
                  />
                  <Tooltip
                    cursor={transfers ? undefined : { fill: "var(--muted)", opacity: 0.5 }}
                    content={({ active, payload }) => {
                      const day = payload?.[0]?.payload
                      if (!active || !day) return null
                      return (
                        <div className={styles.trendTooltip}>
                          <strong>{day.date} · UTC</strong>
                          <p>{day.totalCalls.toLocaleString()} AI calls</p>
                          {minutes ? (
                            <p>{minutesLabel(day.callMinutes, 1)}</p>
                          ) : (
                            <p>
                              {day.totalCalls
                                ? `${day.transferCount} transferred · ${(day.transferRate * 100).toFixed(1)}%`
                                : "Transfer rate unavailable · no calls"}
                            </p>
                          )}
                        </div>
                      )
                    }}
                  />
                  {transfers ? (
                    <Line
                      dataKey="transferRate"
                      name="Transfer rate"
                      type="linear"
                      stroke="var(--chart-3)"
                      strokeWidth={2}
                      dot={{ r: 3 }}
                      activeDot={{ r: 5 }}
                      connectNulls={false}
                      isAnimationActive={false}
                    />
                  ) : (
                    <Bar
                      dataKey={minutes ? "callMinutes" : "totalCalls"}
                      name={minutes ? "Call minutes" : "AI calls"}
                      fill="var(--muted-foreground)"
                      fillOpacity={0.45}
                      maxBarSize={32}
                      radius={[3, 3, 0, 0]}
                      isAnimationActive={false}
                    />
                  )}
                </ComposedChart>
              </ResponsiveContainer>
            </div>
            {versionView && (
              <VersionLane
                groups={versionMarkers}
                dateCount={summary.daily.length}
                locations={versionView.locations}
              />
            )}
            <p className={styles.footnote}>
              UTC call date · First and last days may be partial.
              {transfers &&
                " Gaps mean no calls. Transfers use recorded escalated status."}
              {minutes && " Calls still in progress are not counted."}
            </p>
            <details className={styles.trendData}>
              <summary>View daily data</summary>
              <table className={styles.toolTable}>
                <thead>
                  <tr>
                    <th scope="col">Date · UTC</th>
                    <th scope="col">AI calls</th>
                    {minutes && <th scope="col">Minutes</th>}
                    {transfers && (
                      <>
                        <th scope="col">Transferred</th>
                        <th scope="col">Rate</th>
                      </>
                    )}
                  </tr>
                </thead>
                <tbody>
                  {summary.daily.map((day) => (
                    <tr key={day.date}>
                      <th scope="row">{day.date}</th>
                      <td>{day.totalCalls}</td>
                      {minutes && <td>{day.callMinutes.toFixed(1)}</td>}
                      {transfers && (
                        <>
                          <td>{day.transferCount}</td>
                          <td>
                            {day.transferRate === undefined
                              ? "—"
                              : `${(day.transferRate * 100).toFixed(1)}%`}
                          </td>
                        </>
                      )}
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          </section>
        )
      })}
    </div>
  )
}

export function DiagnosticsTools({
  summary,
  onSelect,
}: {
  summary: OperatorAiAnalyticsSummary
  onSelect: SelectDiagnostic
}) {
  const [selection, setSelection] = useState<{
    name: string
    errors: boolean
  } | null>(null)
  const [sort, setSort] = useState<"latency" | "errors" | "volume">("latency")
  const tools = [...(summary.diagnostics?.tools ?? [])].sort((a, b) => {
    const value = (tool: OperatorAiToolDiagnostics) =>
      sort === "errors"
        ? tool.errorCount / tool.executionCount
        : sort === "volume"
          ? tool.executionCount
          : (tool.p95Ms ?? -1)
    return value(b) - value(a) || a.name.localeCompare(b.name)
  })
  const selected =
    tools.find((tool) => tool.name === selection?.name) ?? tools[0]
  const measured = tools.reduce((sum, tool) => sum + tool.sampleCount, 0)
  const maxLatency = Math.max(...tools.map((tool) => tool.p95Ms ?? 0), 1)
  return (
    <div className={styles.diagnostics}>
      <section
        className={styles.toolSummary}
        aria-label="Tool execution summary"
      >
        <div>
          <p className={styles.eyebrow}>Tool executions</p>
          <strong className={styles.headline}>
            {summary.toolCallCount.toLocaleString()}
          </strong>
          <p className={styles.caption}>
            Across {summary.totalCalls.toLocaleString()} calls
          </p>
        </div>
        <div>
          <p className={styles.eyebrow}>Execution failure rate</p>
          <strong className={styles.secondaryHeadline}>
            {summary.toolCallCount
              ? `${(summary.toolFailureRate * 100).toFixed(1)}%`
              : "—"}
          </strong>
          <p className={styles.caption}>
            {summary.toolErrorCount.toLocaleString()} errors · business results
            stay separate
          </p>
        </div>
        <div>
          <p className={styles.eyebrow}>Timing coverage</p>
          <strong className={styles.secondaryHeadline}>
            {summary.diagnostics && summary.toolCallCount
              ? `${((measured / summary.toolCallCount) * 100).toFixed(0)}%`
              : "—"}
          </strong>
          <p className={styles.caption}>
            {summary.diagnostics
              ? `${measured.toLocaleString()} of ${summary.toolCallCount.toLocaleString()} executions timed`
              : "Timing coverage unavailable"}
          </p>
        </div>
      </section>
      <section aria-label="Tool breakdown">
        <div className={styles.sectionHeading}>
          <div>
            <h2>Tool performance</h2>
            <p className={styles.caption}>
              Compare typical execution time with the slow tail.
            </p>
          </div>
          <label className={styles.sort}>
            Sort by{" "}
            <select
              aria-label="Sort tools"
              value={sort}
              onChange={(event) => setSort(event.target.value as typeof sort)}
            >
              <option value="latency">P95 latency</option>
              <option value="errors">Failure rate</option>
              <option value="volume">Executions</option>
            </select>
          </label>
        </div>
        <div className={styles.tableScroll}>
          <table className={styles.toolTable}>
            <thead>
              <tr>
                <th>Tool</th>
                <th className={styles.durationColumn}>
                  <div className={styles.legend}>
                    <span>
                      <i />
                      P50
                    </span>
                    <span>
                      <i className={styles.accentDot} />
                      P95
                    </span>
                  </div>
                </th>
                <th>P50</th>
                <th>P95</th>
                <th>Executions</th>
                <th>Failures</th>
                <th>Timed</th>
              </tr>
            </thead>
            <tbody>
              {tools.map((tool) => (
                <tr
                  key={tool.name}
                  data-selected={selected?.name === tool.name}
                >
                  <td>
                    <button
                      className={styles.toolButton}
                      onClick={() =>
                        setSelection({ name: tool.name, errors: false })
                      }
                    >
                      <ToolName name={tool.name} />
                      <span>{tool.name}</span>
                    </button>
                  </td>
                  <td className={styles.durationColumn}>
                    <button
                      className={styles.durationBars}
                      aria-label={`Inspect ${tool.name} latency`}
                      onClick={() =>
                        setSelection({ name: tool.name, errors: false })
                      }
                    >
                      {tool.p95Ms === undefined ? (
                        <span className={styles.missing}>Not measured</span>
                      ) : (
                        <>
                          <i
                            style={{
                              width: `${(tool.p95Ms / maxLatency) * 100}%`,
                            }}
                          />
                          <i
                            style={{
                              width: `${((tool.p50Ms ?? 0) / maxLatency) * 100}%`,
                            }}
                          />
                        </>
                      )}
                    </button>
                  </td>
                  <td>{latencyLabel(tool.p50Ms)}</td>
                  <td>
                    <strong>{latencyLabel(tool.p95Ms)}</strong>
                  </td>
                  <td>
                    {tool.executionCount.toLocaleString()}
                    {tool.incompleteCount > 0 && (
                      <small>{tool.incompleteCount} incomplete</small>
                    )}
                  </td>
                  <td>
                    <button
                      className={
                        tool.errorCount
                          ? styles.failureButton
                          : styles.rateButton
                      }
                      onClick={() =>
                        setSelection({ name: tool.name, errors: true })
                      }
                    >
                      {((tool.errorCount / tool.executionCount) * 100).toFixed(
                        1,
                      )}
                      %
                      <small>
                        {tool.errorCount} / {tool.executionCount}
                      </small>
                    </button>
                  </td>
                  <td>
                    {tool.sampleCount} / {tool.executionCount}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {tools.length === 0 && (
          <p className={styles.empty}>
            {summary.diagnostics
              ? "No tool executions in this range."
              : "Detailed tool timing is unavailable. Refresh to try again."}
          </p>
        )}
        <p className={styles.footnote}>
          Execution duration uses recorded call and result timestamps. Missing
          timing stays unknown; incomplete executions are counted separately.
        </p>
      </section>
      {selected && (
        <Evidence
          examples={selection?.errors ? selected.errors : selected.examples}
          onSelect={onSelect}
          title={`${selection?.errors ? "Failed executions" : "Slowest executions"} · ${selected.name.replaceAll("_", " ")}`}
          note="Up to 5 examples · open tool evidence"
        />
      )}
    </div>
  )
}
