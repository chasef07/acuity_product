"use client"

import { useState } from "react"
import { ArrowUpRightIcon } from "lucide-react"
import {
  Bar,
  ComposedChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import type {
  OperatorAiAnalyticsSummary,
  OperatorAiDiagnosticExample,
  OperatorAiLatencyDistribution,
  OperatorAiToolDiagnostics,
} from "@/lib/api/generated/types.gen"
import styles from "./ai-diagnostics.module.css"

export type DiagnosticFocus = { itemID?: string; callID?: string }
export type SelectDiagnostic = (
  interactionID: string,
  focus?: DiagnosticFocus,
) => void
const stageLabels = {
  e2e: "Response time",
  stt: "STT",
  llm: "LLM",
  tts: "TTS",
}
const stageNotes = {
  e2e: "Caller stop to first audio",
  stt: "Final transcript",
  llm: "First token",
  tts: "First audio byte",
}
export function latencyLabel(value?: number): string {
  if (value === undefined) return "—"
  return value >= 1000 ? `${(value / 1000).toFixed(2)} s` : `${value} ms`
}
function bucketLabel(from: number, to?: number) {
  const value = (ms: number) => (ms >= 1000 ? `${ms / 1000}s` : `${ms}ms`)
  return to === undefined ? `${value(from)}+` : `${value(from)}–${value(to)}`
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
function Trend({ stage }: { stage: OperatorAiLatencyDistribution }) {
  if (!stage.sampleCount)
    return (
      <div className={styles.chartEmpty}>
        No measured {stageLabels[stage.stage].toLowerCase()} in this range.
      </div>
    )
  return (
    <div
      className={styles.trend}
      role="img"
      aria-label={`${stageLabels[stage.stage]} daily median and P95 trend`}
    >
      <ResponsiveContainer width="100%" height="100%">
        <LineChart
          data={stage.trend}
          margin={{ top: 18, right: 10, bottom: 8, left: 0 }}
        >
          <CartesianGrid
            vertical={false}
            stroke="var(--border)"
            strokeDasharray="3 4"
          />
          <XAxis
            dataKey="date"
            tickFormatter={(date: string) => date.slice(5).replace("-", "/")}
            tickLine={false}
            axisLine={false}
            minTickGap={30}
            tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
          />
          <YAxis
            tickFormatter={latencyLabel}
            tickLine={false}
            axisLine={false}
            width={65}
            tick={{ fontSize: 10, fill: "var(--muted-foreground)" }}
            domain={[0, "auto"]}
          />
          <Tooltip
            formatter={(value, name) => [
              latencyLabel(Number(value)),
              name === "p95Ms" ? "P95" : "P50",
            ]}
            labelFormatter={(label) => `${label} · UTC call date`}
            contentStyle={{
              background: "var(--card)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              fontSize: 12,
            }}
          />
          <Line
            type="linear"
            dataKey="p50Ms"
            stroke="var(--muted-foreground)"
            strokeWidth={1.5}
            dot={{ r: 2 }}
            activeDot={{ r: 4 }}
            isAnimationActive={false}
          />
          <Line
            type="linear"
            dataKey="p95Ms"
            stroke="#b2714c"
            strokeWidth={2}
            dot={{ r: 3 }}
            activeDot={{ r: 5 }}
            isAnimationActive={false}
          />
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}
export function DiagnosticsCallTrends({
  summary,
}: {
  summary: OperatorAiAnalyticsSummary
}) {
  return (
    <div className={styles.callTrends}>
      {(["volume", "transfers"] as const).map((metric) => {
        const transfers = metric === "transfers"
        const title = transfers
          ? "Transfer rate over time"
          : "Call volume over time"
        return (
          <section key={metric} aria-label={title} className={styles.callTrend}>
            <div className={styles.sectionHeading}>
              <h2>{title}</h2>
              <span>
                {transfers ? "Transferred / all AI calls" : "AI calls per day"}
              </span>
            </div>
            <strong className={styles.secondaryHeadline}>
              {transfers
                ? summary.totalCalls
                  ? `${(summary.transferRate * 100).toFixed(1)}%`
                  : "—"
                : summary.totalCalls.toLocaleString()}
            </strong>
            <p className={styles.caption}>
              {transfers
                ? `${summary.transferCount.toLocaleString()} of ${summary.totalCalls.toLocaleString()} calls transferred`
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
                    content={({ active, payload }) => {
                      const day = payload?.[0]?.payload
                      if (!active || !day) return null
                      return (
                        <div className={styles.trendTooltip}>
                          <strong>{day.date} · UTC</strong>
                          <p>{day.totalCalls.toLocaleString()} AI calls</p>
                          <p>
                            {day.totalCalls
                              ? `${day.transferCount} transferred · ${(day.transferRate * 100).toFixed(1)}%`
                              : "Transfer rate unavailable · no calls"}
                          </p>
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
                      dataKey="totalCalls"
                      name="AI calls"
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
            <p className={styles.footnote}>
              UTC call date · First and last days may be partial.
              {transfers &&
                " Gaps mean no calls. Transfers use recorded escalated status."}
            </p>
            <details className={styles.trendData}>
              <summary>View daily data</summary>
              <table className={styles.toolTable}>
                <thead>
                  <tr>
                    <th scope="col">Date · UTC</th>
                    <th scope="col">AI calls</th>
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

export function DiagnosticsPerformance({
  summary,
  onSelect,
}: {
  summary: OperatorAiAnalyticsSummary
  onSelect: SelectDiagnostic
}) {
  const [stageKey, setStageKey] = useState("e2e")
  const [bucketIndex, setBucketIndex] = useState<number | null>(null)
  const stages = summary.diagnostics?.stages ?? []
  const stage = stages.find((value) => value.stage === stageKey)
  if (!stage)
    return (
      <p className={styles.empty}>
        Detailed timing is unavailable. Refresh to try again.
      </p>
    )
  const selectedBucket =
    bucketIndex === null ? undefined : stage.buckets[bucketIndex]
  const examples =
    selectedBucket?.examples ??
    stage.buckets
      .flatMap((bucket) => bucket.examples)
      .sort((a, b) => (b.durationMs ?? 0) - (a.durationMs ?? 0))
      .slice(0, 5)
  const maxCount = Math.max(...stage.buckets.map((bucket) => bucket.count), 1)
  return (
    <div className={styles.diagnostics}>
      <section className={styles.hero} aria-label="Response performance">
        <div className={styles.summary}>
          <p className={styles.eyebrow}>{stageLabels[stage.stage]} · P95</p>
          <strong className={styles.headline}>
            {latencyLabel(stage.p95Ms)}
          </strong>
          <p className={styles.caption}>
            {stageNotes[stage.stage]}
            <br />
            95% of measured samples are at or below this time.
          </p>
          <div className={styles.facts}>
            <div>
              <span>Median · P50</span>
              <strong>{latencyLabel(stage.p50Ms)}</strong>
            </div>
            <div>
              <span>Tail · P99</span>
              <strong>{latencyLabel(stage.p99Ms)}</strong>
            </div>
          </div>
          <p className={styles.coverage}>
            {stage.sampleCount.toLocaleString()} samples ·{" "}
            {stage.measuredCalls.toLocaleString()} of{" "}
            {summary.totalCalls.toLocaleString()} calls measured
          </p>
        </div>
        <div>
          <div className={styles.sectionHeading}>
            <h2>
              {stage.stage === "e2e" ? "Response" : stageLabels[stage.stage]}{" "}
              timing over time
            </h2>
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
          </div>
          <Trend stage={stage} />
        </div>
      </section>
      <section className={styles.stageSection} aria-label="Pipeline stages">
        <div className={styles.sectionHeading}>
          <h2>Where the time goes</h2>
          <span>Select a stage to explore its distribution</span>
        </div>
        <div className={styles.stages}>
          {stages.map((item) => (
            <button
              key={item.stage}
              aria-pressed={stageKey === item.stage}
              className={`${styles.stage} ${item.stage === "e2e" ? styles.overall : ""}`}
              onClick={() => {
                setStageKey(item.stage)
                setBucketIndex(null)
              }}
            >
              <span>
                {stageLabels[item.stage]}
                {item.stage === "e2e" && <small>Overall</small>}
              </span>
              <strong>
                {latencyLabel(item.p95Ms)} <small>P95</small>
              </strong>
              <span>{stageNotes[item.stage]}</span>
              <span className={styles.stageFoot}>
                P50 {latencyLabel(item.p50Ms)} ·{" "}
                {item.sampleCount.toLocaleString()} samples
              </span>
            </button>
          ))}
        </div>
        <p className={styles.footnote}>
          Stages can overlap. Their percentiles do not add up to the measured
          response time.
        </p>
      </section>
      <section
        className={styles.distribution}
        aria-label="Latency distribution"
      >
        <div className={styles.sectionHeading}>
          <h2>{stageLabels[stage.stage]} distribution</h2>
          <span>{stage.sampleCount.toLocaleString()} measured samples</span>
        </div>
        <div className={styles.histogram}>
          {stage.buckets.map((bucket, index) => (
            <button
              key={bucket.fromMs}
              className={styles.bin}
              disabled={!bucket.count}
              aria-pressed={bucketIndex === index}
              aria-label={`${bucketLabel(bucket.fromMs, bucket.toMs)}: ${bucket.count} samples`}
              onClick={() =>
                setBucketIndex(bucketIndex === index ? null : index)
              }
            >
              <div className={styles.barTrack}>
                <span
                  className={styles.bar}
                  style={{ height: `${(bucket.count / maxCount) * 100}%` }}
                >
                  <span>
                    {bucket.count > 0 ? bucket.count.toLocaleString() : ""}
                  </span>
                </span>
              </div>
              <span>{bucketLabel(bucket.fromMs, bucket.toMs)}</span>
            </button>
          ))}
        </div>
        <p className={styles.footnote}>
          Select a bucket to inspect examples. Ranges include the lower bound
          and exclude the upper bound.
        </p>
      </section>
      <Evidence
        examples={examples}
        onSelect={onSelect}
        title={
          selectedBucket
            ? `Samples · ${bucketLabel(selectedBucket.fromMs, selectedBucket.toMs)}`
            : `Slowest ${stage.stage === "e2e" ? "response" : stageLabels[stage.stage]} samples`
        }
        note="Up to 5 samples · open call evidence"
      />
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
