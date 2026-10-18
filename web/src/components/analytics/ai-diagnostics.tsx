"use client"

import { useState } from "react"
import { ArrowUpRightIcon } from "lucide-react"
import type {
  OperatorAiAnalyticsSummary,
  OperatorAiDiagnosticExample,
  OperatorAiToolDiagnostics,
} from "@/lib/api/generated/types.gen"
import { formatTimestamp } from "@/lib/format-time"
import styles from "./ai-diagnostics.module.css"
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
                  {formatTimestamp(sample.startedAt)}
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
