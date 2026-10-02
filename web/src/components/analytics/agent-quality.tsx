"use client"

import type { ReactNode } from "react"
import {
  Bar,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import type {
  OperatorAiAnalyticsSummary,
  OperatorAiContextFootprint,
  OperatorAiQualityDay,
} from "@/lib/api/generated/types.gen"
import {
  evaluationLabels,
  evaluationScales,
} from "@/components/workspace/call-evaluation"
import styles from "./ai-diagnostics.module.css"
import {
  VersionLane,
  versionGroups,
  versionReferenceLines,
  type VersionView,
} from "./version-markers"

const sentimentLabels = evaluationScales.expressed_sentiment
const footprintParts = [
  { key: "speakerPromptTokens", label: "Speaker prompt", color: "var(--quality-series-1)" },
  { key: "thinkerPromptTokens", label: "Thinker prompt", color: "var(--quality-series-2)" },
  { key: "toolSchemaTokens", label: "Tool definitions", color: "var(--quality-series-3)" },
] as const

const axisTick = { fontSize: 10, fill: "var(--muted-foreground)" }
const dateTick = (date: string) => date.slice(5).replace("-", "/")
const tokens = (value?: number) =>
  value === undefined ? "—" : Math.round(value).toLocaleString()
const compactTokens = (value: number) =>
  value >= 1000 ? `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k` : `${value}`
const percent = (value?: number) =>
  value === undefined ? "—" : `${(value * 100).toFixed(1)}%`
const checkLabel = (check: string) =>
  evaluationLabels[check] ?? check.replaceAll("_", " ")
const flagRate = (day: { flaggedCalls: number; evaluatedCalls: number }) =>
  day.evaluatedCalls ? day.flaggedCalls / day.evaluatedCalls : undefined
const footprintTotal = (value?: OperatorAiContextFootprint) =>
  value
    ? value.speakerPromptTokens + value.thinkerPromptTokens + value.toolSchemaTokens
    : undefined
function sentimentLabel(value?: number) {
  return value === undefined ? "—" : sentimentLabels[Math.round(value)]
}

export function DiagnosticsQuality({
  summary,
  versionView,
}: {
  summary: OperatorAiAnalyticsSummary
  versionView?: VersionView
}) {
  const quality = summary.quality
  const latest = quality.latestFootprint
  const staff = quality.staffFlags
  const stats = [
    {
      label: "Red flag rate",
      value: percent(flagRate(quality)),
      note: `${quality.flaggedCalls.toLocaleString()} of ${quality.evaluatedCalls.toLocaleString()} evaluated calls · ${quality.unevaluatedCalls.toLocaleString()} not evaluated`,
    },
    {
      label: "Staff flags",
      value: (staff.pending + staff.confirmed + staff.notAnIssue).toLocaleString(),
      note: `${staff.confirmed} confirmed · ${staff.pending} pending · ${staff.notAnIssue} not an issue`,
    },
    {
      label: "Caller sentiment",
      value:
        quality.meanSentiment === undefined
          ? "—"
          : `${quality.meanSentiment.toFixed(2)} / 4`,
      note: `${sentimentLabel(quality.meanSentiment)} · ${quality.sentimentCalls.toLocaleString()} scored calls`,
    },
    {
      label: "Context footprint",
      value: latest ? `${tokens(footprintTotal(latest))} tokens` : "Not reported",
      note: latest
        ? `${latest.toolCount} tools · latest reporting call`
        : "The agent does not report its prompt and tool size yet",
    },
  ]
  return (
    <div className={`${styles.quality} space-y-3`}>
      <div className={styles.qualityStats}>
        {stats.map((stat) => (
          <div key={stat.label}>
            <span>{stat.label}</span>
            <strong>{stat.value}</strong>
            <small>{stat.note}</small>
          </div>
        ))}
      </div>
      <div className={styles.callTrends}>
        <RedFlagTrend days={quality.daily} versionView={versionView} />
        <FlagsByCheck summary={summary} />
        <SentimentTrend days={quality.daily} versionView={versionView} />
        <FootprintTrend days={quality.daily} versionView={versionView} />
        <TokenTrend summary={summary} versionView={versionView} />
      </div>
    </div>
  )
}

function TrendSection({
  title,
  note,
  legend,
  children,
  lane,
  footnote,
  table,
}: {
  title: string
  note: string
  legend?: ReactNode
  children: ReactNode
  lane?: ReactNode
  footnote: string
  table?: ReactNode
}) {
  return (
    <section aria-label={title} className={styles.callTrend}>
      <div className={styles.sectionHeading}>
        <h2>{title}</h2>
        <span>{note}</span>
      </div>
      {legend}
      {children}
      {lane}
      <p className={styles.footnote}>{footnote}</p>
      {table && (
        <details className={styles.trendData}>
          <summary>View daily data</summary>
          <div className={styles.tableScroll}>
            <table className={styles.toolTable}>{table}</table>
          </div>
        </details>
      )}
    </section>
  )
}

function Legend({ items }: { items: Array<{ label: string; color: string }> }) {
  return (
    <div className={styles.qualityLegend}>
      {items.map((item) => (
        <span key={item.label}>
          <i style={{ background: item.color }} />
          {item.label}
        </span>
      ))}
    </div>
  )
}

function Chart({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className={styles.trend} role="img" aria-label={label}>
      <ResponsiveContainer width="100%" height="100%">
        {children}
      </ResponsiveContainer>
    </div>
  )
}

function Grid() {
  return (
    <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 4" />
  )
}

function DateAxis() {
  return (
    <XAxis
      dataKey="date"
      tickFormatter={dateTick}
      tickLine={false}
      axisLine={false}
      minTickGap={30}
      tick={axisTick}
    />
  )
}

function versionMarkers({
  view,
  days,
  measured,
}: {
  view?: VersionView
  days: Array<{ date: string }>
  measured: boolean
}) {
  const groups = versionGroups(view, days.map((day) => day.date), measured)
  return {
    lines: versionReferenceLines(groups),
    lane: view ? (
      <VersionLane groups={groups} dateCount={days.length} locations={view.locations} />
    ) : null,
  }
}

function RedFlagTrend({
  days,
  versionView,
}: {
  days: OperatorAiQualityDay[]
  versionView?: VersionView
}) {
  const data = days.map((day) => ({ ...day, flagRate: flagRate(day) }))
  const markers = versionMarkers({ view: versionView, days, measured: true })
  return (
    <TrendSection
      title="Red flags over time"
      note="Flagged / evaluated calls"
      lane={markers.lane}
      footnote="UTC call date · A call is flagged when any judge check scores 0.4 or lower. Gaps mean no evaluated calls. Dashed lines mark judge or evaluator changes, which can move this rate without any change to the agent."
      table={
        <>
          <thead>
            <tr>
              <th scope="col">Date · UTC</th>
              <th scope="col">Flagged</th>
              <th scope="col">Evaluated</th>
              <th scope="col">Not evaluated</th>
              <th scope="col">Rate</th>
              <th scope="col">Staff flags</th>
            </tr>
          </thead>
          <tbody>
            {data.map((day) => (
              <tr key={day.date}>
                <th scope="row">{day.date}</th>
                <td>{day.flaggedCalls}</td>
                <td>{day.evaluatedCalls}</td>
                <td>{day.unevaluatedCalls}</td>
                <td>{percent(day.flagRate)}</td>
                <td>
                  {day.staffFlags.confirmed} confirmed · {day.staffFlags.pending} pending ·{" "}
                  {day.staffFlags.notAnIssue} not an issue
                </td>
              </tr>
            ))}
          </tbody>
        </>
      }
    >
      <Chart label="Daily red flag rate among evaluated calls">
        <ComposedChart data={data} margin={{ top: 24, right: 12, bottom: 0, left: 0 }}>
          <Grid />
          <DateAxis />
          <YAxis
            domain={[0, "auto"]}
            tickFormatter={(value: number) => `${Math.round(value * 100)}%`}
            width={44}
            tickLine={false}
            axisLine={false}
            tick={axisTick}
          />
          {markers.lines}
          <Tooltip
            content={({ active, payload }) => {
              const day = payload?.[0]?.payload as (typeof data)[number] | undefined
              if (!active || !day) return null
              return (
                <div className={styles.trendTooltip}>
                  <strong>{day.date} · UTC</strong>
                  <p>
                    {day.evaluatedCalls
                      ? `${day.flaggedCalls} of ${day.evaluatedCalls} evaluated calls flagged · ${percent(day.flagRate)}`
                      : "No evaluated calls"}
                  </p>
                  {day.checkFlags.map((flag) => (
                    <p key={flag.check}>
                      {checkLabel(flag.check)}: {flag.calls} of {flag.scoredCalls} scored
                    </p>
                  ))}
                  {day.unevaluatedCalls > 0 && <p>{day.unevaluatedCalls} not evaluated</p>}
                  <p>
                    Staff: {day.staffFlags.confirmed} confirmed · {day.staffFlags.pending} pending ·{" "}
                    {day.staffFlags.notAnIssue} not an issue
                  </p>
                </div>
              )
            }}
          />
          <Line
            dataKey="flagRate"
            name="Red flag rate"
            type="linear"
            stroke="var(--destructive)"
            strokeWidth={2}
            dot={{ r: 3 }}
            activeDot={{ r: 5 }}
            connectNulls={false}
            isAnimationActive={false}
          />
        </ComposedChart>
      </Chart>
    </TrendSection>
  )
}

function FlagsByCheck({ summary }: { summary: OperatorAiAnalyticsSummary }) {
  const quality = summary.quality
  const max = Math.max(...quality.checkFlags.map((flag) => flag.calls), 1)
  const staff = quality.staffFlags
  const reviewed = staff.confirmed + staff.notAnIssue
  return (
    <TrendSection
      title="Red flags by check"
      note="Flagged calls in selected range"
      footnote="Each check's rate is flagged calls among calls that check scored; errored, not-applicable, and absent checks are excluded. A call can be flagged by more than one check. Staff flags are reported by office staff and reviewed by Acuity; they are counted by call start date."
    >
      {quality.checkFlags.length === 0 ? (
        <p className={styles.chartEmpty}>
          {quality.evaluatedCalls ? "No checks scored in this range." : "No evaluated calls in this range."}
        </p>
      ) : (
        <ul className={styles.checkBars}>
          {quality.checkFlags.map((flag) => (
            <li key={flag.check}>
              <span>{checkLabel(flag.check)}</span>
              <span className={styles.checkTrack}>
                <i style={{ width: `${(flag.calls / max) * 100}%` }} />
              </span>
              <strong>
                {flag.calls.toLocaleString()}
                <small>{flag.scoredCalls ? percent(flag.calls / flag.scoredCalls) : "—"}</small>
              </strong>
            </li>
          ))}
        </ul>
      )}
      <dl className={styles.staffReview}>
        <div>
          <dt>Staff confirmed</dt>
          <dd>{staff.confirmed.toLocaleString()}</dd>
        </div>
        <div>
          <dt>Not an issue</dt>
          <dd>{staff.notAnIssue.toLocaleString()}</dd>
        </div>
        <div>
          <dt>Awaiting review</dt>
          <dd>{staff.pending.toLocaleString()}</dd>
        </div>
        <div>
          <dt>Confirmed share</dt>
          <dd>{reviewed ? percent(staff.confirmed / reviewed) : "—"}</dd>
        </div>
      </dl>
    </TrendSection>
  )
}

const sentimentColors = [0, 1, 2, 3, 4].map((index) => `var(--quality-sentiment-${index})`)

function SentimentTrend({
  days,
  versionView,
}: {
  days: OperatorAiQualityDay[]
  versionView?: VersionView
}) {
  const markers = versionMarkers({ view: versionView, days, measured: true })
  const data = days.map((day) => {
    const shares: Record<string, number | undefined> = {}
    day.sentimentCounts.forEach((count, index) => {
      shares[`s${index}`] = day.sentimentCalls ? count / day.sentimentCalls : undefined
    })
    return { ...day, ...shares }
  })
  return (
    <TrendSection
      title="Caller sentiment over time"
      note="Share of scored calls"
      legend={<Legend items={sentimentLabels.map((label, index) => ({ label, color: sentimentColors[index] }))} />}
      lane={markers.lane}
      footnote="UTC call date · Expressed sentiment judged from the transcript, 0 (very negative) to 4 (very positive), rounded to the nearest level. Empty days had no scored calls."
      table={
        <>
          <thead>
            <tr>
              <th scope="col">Date · UTC</th>
              {sentimentLabels.map((label) => (
                <th key={label} scope="col">{label}</th>
              ))}
              <th scope="col">Mean</th>
            </tr>
          </thead>
          <tbody>
            {days.map((day) => (
              <tr key={day.date}>
                <th scope="row">{day.date}</th>
                {day.sentimentCounts.map((count, index) => (
                  <td key={index}>{count}</td>
                ))}
                <td>{day.meanSentiment === undefined ? "—" : day.meanSentiment.toFixed(2)}</td>
              </tr>
            ))}
          </tbody>
        </>
      }
    >
      <Chart label="Daily share of calls by expressed caller sentiment">
        <ComposedChart data={data} margin={{ top: 24, right: 12, bottom: 0, left: 0 }}>
          <Grid />
          <DateAxis />
          {markers.lines}
          <YAxis
            domain={[0, 1]}
            tickFormatter={(value: number) => `${Math.round(value * 100)}%`}
            width={44}
            tickLine={false}
            axisLine={false}
            tick={axisTick}
          />
          <Tooltip
            cursor={{ fill: "var(--muted)", opacity: 0.5 }}
            content={({ active, payload }) => {
              const day = payload?.[0]?.payload as (typeof data)[number] | undefined
              if (!active || !day) return null
              return (
                <div className={styles.trendTooltip}>
                  <strong>{day.date} · UTC</strong>
                  {day.sentimentCalls ? (
                    <>
                      <p>
                        Mean {day.meanSentiment?.toFixed(2)} · {sentimentLabel(day.meanSentiment)}
                      </p>
                      {[...day.sentimentCounts].reverse().map((count, reversed) => {
                        const index = 4 - reversed
                        return (
                          <p key={index}>
                            {sentimentLabels[index]}: {count} · {percent(count / day.sentimentCalls)}
                          </p>
                        )
                      })}
                    </>
                  ) : (
                    <p>No scored calls</p>
                  )}
                </div>
              )
            }}
          />
          {sentimentLabels.map((label, index) => (
            <Bar
              key={label}
              dataKey={`s${index}`}
              name={label}
              stackId="sentiment"
              fill={sentimentColors[index]}
              stroke="var(--card)"
              strokeWidth={1}
              maxBarSize={32}
              radius={index === 4 ? [3, 3, 0, 0] : 0}
              isAnimationActive={false}
            />
          ))}
        </ComposedChart>
      </Chart>
    </TrendSection>
  )
}

function FootprintTrend({
  days,
  versionView,
}: {
  days: OperatorAiQualityDay[]
  versionView?: VersionView
}) {
  const markers = versionMarkers({ view: versionView, days, measured: false })
  const reported = days.some((day) => day.footprint)
  const data = days.map((day) => ({ ...day, ...day.footprint }))
  return (
    <TrendSection
      title="Context footprint over time"
      note="Median prompt and tool tokens per call"
      legend={<Legend items={footprintParts.map(({ label, color }) => ({ label, color }))} />}
      lane={reported ? markers.lane : undefined}
      footnote="UTC call date · Static context the agent reports at session start, excluding conversation history. A step marks a prompt or tool change."
      table={
        <>
          <thead>
            <tr>
              <th scope="col">Date · UTC</th>
              {footprintParts.map((part) => (
                <th key={part.key} scope="col">{part.label}</th>
              ))}
              <th scope="col">Tools</th>
              <th scope="col">Reporting calls</th>
            </tr>
          </thead>
          <tbody>
            {days.map((day) => (
              <tr key={day.date}>
                <th scope="row">{day.date}</th>
                {footprintParts.map((part) => (
                  <td key={part.key}>{tokens(day.footprint?.[part.key])}</td>
                ))}
                <td>{day.footprint?.toolCount ?? "—"}</td>
                <td>{day.footprintCalls}</td>
              </tr>
            ))}
          </tbody>
        </>
      }
    >
      {!reported ? (
        <p className={styles.chartEmpty}>
          No calls in this range reported a context footprint.
        </p>
      ) : (
        <Chart label="Daily median context footprint by prompt and tool definitions">
          <ComposedChart data={data} margin={{ top: 24, right: 12, bottom: 0, left: 0 }}>
            <Grid />
            <DateAxis />
            {markers.lines}
            <YAxis
              domain={[0, "auto"]}
              tickFormatter={compactTokens}
              width={44}
              tickLine={false}
              axisLine={false}
              tick={axisTick}
            />
            <Tooltip
              cursor={{ fill: "var(--muted)", opacity: 0.5 }}
              content={({ active, payload }) => {
                const day = payload?.[0]?.payload as (typeof data)[number] | undefined
                if (!active || !day) return null
                return (
                  <div className={styles.trendTooltip}>
                    <strong>{day.date} · UTC</strong>
                    {day.footprint ? (
                      <>
                        <p>{tokens(footprintTotal(day.footprint))} tokens · {day.footprint.toolCount} tools</p>
                        {footprintParts.map((part) => (
                          <p key={part.key}>
                            {part.label}: {tokens(day.footprint?.[part.key])}
                          </p>
                        ))}
                        <p>{day.footprintCalls} reporting calls</p>
                      </>
                    ) : (
                      <p>Not reported</p>
                    )}
                  </div>
                )
              }}
            />
            {footprintParts.map((part, index) => (
              <Bar
                key={part.key}
                dataKey={part.key}
                name={part.label}
                stackId="footprint"
                fill={part.color}
                stroke="var(--card)"
                strokeWidth={1}
                maxBarSize={32}
                radius={index === footprintParts.length - 1 ? [3, 3, 0, 0] : 0}
                isAnimationActive={false}
              />
            ))}
          </ComposedChart>
        </Chart>
      )}
    </TrendSection>
  )
}

function TokenTrend({
  summary,
  versionView,
}: {
  summary: OperatorAiAnalyticsSummary
  versionView?: VersionView
}) {
  const quality = summary.quality
  const markers = versionMarkers({ view: versionView, days: quality.daily, measured: false })
  return (
    <TrendSection
      title="Tokens per call over time"
      note="Median LLM input tokens per call"
      lane={quality.tokenCalls ? markers.lane : undefined}
      footnote={`UTC call date · Total recorded input tokens per call, including cached tokens and conversation history. Range medians: ${tokens(quality.p50InputTokens)} input · ${tokens(quality.p50CachedTokens)} cached · ${tokens(quality.p50OutputTokens)} output across ${quality.tokenCalls.toLocaleString()} calls. Gaps mean no recorded usage.`}
      table={
        <>
          <thead>
            <tr>
              <th scope="col">Date · UTC</th>
              <th scope="col">Input P50</th>
              <th scope="col">Input P90</th>
              <th scope="col">Cached P50</th>
              <th scope="col">Output P50</th>
              <th scope="col">Calls</th>
            </tr>
          </thead>
          <tbody>
            {quality.daily.map((day) => (
              <tr key={day.date}>
                <th scope="row">{day.date}</th>
                <td>{tokens(day.p50InputTokens)}</td>
                <td>{tokens(day.p90InputTokens)}</td>
                <td>{tokens(day.p50CachedTokens)}</td>
                <td>{tokens(day.p50OutputTokens)}</td>
                <td>{day.tokenCalls}</td>
              </tr>
            ))}
          </tbody>
        </>
      }
    >
      {quality.tokenCalls === 0 ? (
        <p className={styles.chartEmpty}>No calls in this range recorded token usage.</p>
      ) : (
        <Chart label="Daily median LLM input tokens per call">
          <ComposedChart data={quality.daily} margin={{ top: 24, right: 12, bottom: 0, left: 0 }}>
            <Grid />
            <DateAxis />
            {markers.lines}
            <YAxis
              domain={[0, "auto"]}
              tickFormatter={compactTokens}
              width={44}
              tickLine={false}
              axisLine={false}
              tick={axisTick}
            />
            <Tooltip
              content={({ active, payload }) => {
                const day = payload?.[0]?.payload as OperatorAiQualityDay | undefined
                if (!active || !day) return null
                return (
                  <div className={styles.trendTooltip}>
                    <strong>{day.date} · UTC</strong>
                    {day.tokenCalls ? (
                      <>
                        <p>Input P50 {tokens(day.p50InputTokens)} · P90 {tokens(day.p90InputTokens)}</p>
                        <p>Cached P50 {tokens(day.p50CachedTokens)}</p>
                        <p>Output P50 {tokens(day.p50OutputTokens)}</p>
                        <p>{day.tokenCalls} calls with usage</p>
                      </>
                    ) : (
                      <p>No recorded usage</p>
                    )}
                  </div>
                )
              }}
            />
            <Line
              dataKey="p50InputTokens"
              name="Median input tokens"
              type="linear"
              stroke="var(--quality-series-1)"
              strokeWidth={2}
              dot={{ r: 3 }}
              activeDot={{ r: 5 }}
              connectNulls={false}
              isAnimationActive={false}
            />
          </ComposedChart>
        </Chart>
      )}
    </TrendSection>
  )
}
