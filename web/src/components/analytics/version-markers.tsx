"use client"

import { ReferenceLine } from "recharts"
import type {
  Location,
  OperatorAiAnalyticsVersions,
  OperatorAiVersionChange,
  OperatorAiVersionDimension,
} from "@/lib/api/generated/types.gen"
import styles from "./ai-diagnostics.module.css"

export type VersionSelection = Record<OperatorAiVersionDimension, boolean>

const dimensions: Array<{
  key: OperatorAiVersionDimension
  label: string
  measurement: boolean
}> = [
  { key: "agent", label: "Agent", measurement: false },
  { key: "prompts", label: "Prompts", measurement: false },
  { key: "tools", label: "Tools", measurement: false },
  { key: "knowledge", label: "Knowledge", measurement: false },
  { key: "judges", label: "Judges", measurement: true },
  { key: "evaluator", label: "Evaluator", measurement: true },
]

export const defaultVersionSelection: VersionSelection = {
  agent: true,
  prompts: true,
  tools: true,
  knowledge: false,
  judges: true,
  evaluator: true,
}

const plotLeft = 44
const plotRight = 12

export type VersionView = {
  versions: OperatorAiAnalyticsVersions
  selection: VersionSelection
  locations: Location[]
}

type VersionGroup = {
  date: string
  index: number
  changes: OperatorAiVersionChange[]
  measurementOnly: boolean
}

const isMeasurement = (dimension: OperatorAiVersionDimension) =>
  dimensions.find((item) => item.key === dimension)?.measurement ?? false

function versionLabel(
  dimension: OperatorAiVersionDimension,
  version: string,
  locations: Location[],
  locationId?: string,
) {
  if (dimension === "knowledge") {
    const office = locations.find((location) => location.id === locationId)?.name
    return `knowledge ${version.slice(0, 8)}${office ? ` · ${office}` : ""}`
  }
  return `${dimension} ${version.replace("typesafe-", "")}`
}

export function versionGroups(
  view: VersionView | undefined,
  dates: string[],
  measuredChart: boolean,
): VersionGroup[] {
  if (!view) return []
  const groups = new Map<string, VersionGroup>()
  for (const change of view.versions.changes) {
    const measurement = isMeasurement(change.dimension)
    if (!view.selection[change.dimension] || (measurement && !measuredChart)) continue
    const index = dates.indexOf(change.date)
    if (index < 0) continue
    const group = groups.get(change.date) ?? {
      date: change.date,
      index,
      changes: [],
      measurementOnly: true,
    }
    group.changes.push(change)
    group.measurementOnly &&= measurement
    groups.set(change.date, group)
  }
  return [...groups.values()].sort((a, b) => a.index - b.index)
}

export function versionReferenceLines(groups: VersionGroup[]) {
  return groups.map((group) => (
    <ReferenceLine
      key={`version-${group.date}`}
      x={group.date}
      stroke={group.measurementOnly ? "var(--muted-foreground)" : "var(--version-behavior)"}
      strokeDasharray={group.measurementOnly ? "4 4" : undefined}
      strokeWidth={1.5}
    />
  ))
}

export function VersionLane({
  groups,
  dateCount,
  locations,
}: {
  groups: VersionGroup[]
  dateCount: number
  locations: Location[]
}) {
  if (!groups.length) return null
  return (
    <ol className={styles.versionLane} aria-label="Version changes">
      {groups.map((group, order) => {
        const center = (group.index + 0.5) / dateCount
        const close = order > 0 && (group.index - groups[order - 1].index) / dateCount < 0.2
        const labels = group.changes.map((change) =>
          versionLabel(change.dimension, change.version, locations, change.locationId),
        )
        const detail = group.changes
          .map(
            (change, i) =>
              `${labels[i]} (was ${change.previousVersion.slice(0, change.dimension === "knowledge" ? 8 : undefined)}) · first call ${new Date(change.firstSeenAt).toLocaleString()}`,
          )
          .join("\n")
        return (
          <li
            key={group.date}
            className={styles.versionChip}
            data-measurement={group.measurementOnly}
            data-row={close && order % 2 ? 1 : 0}
            data-edge={center < 0.15 ? "start" : center > 0.85 ? "end" : undefined}
            style={{
              left: `calc(${plotLeft}px + (100% - ${plotLeft + plotRight}px) * ${center})`,
            }}
            title={`${group.date} · UTC\n${detail}`}
            tabIndex={0}
          >
            <i aria-hidden="true" />
            {labels.length === 1 ? labels[0] : `${labels.length} changes`}
            <span className="sr-only">
              {` on ${group.date}: ${detail}`}
            </span>
          </li>
        )
      })}
    </ol>
  )
}

export function VersionToolbar({
  view,
  onChange,
}: {
  view: VersionView
  onChange: (selection: VersionSelection) => void
}) {
  const { versions, selection, locations } = view
  const inEffect = versions.inEffect.filter((item) => item.dimension !== "knowledge")
  return (
    <section className={styles.versionToolbar} aria-label="Version markers">
      <div role="group" aria-label="Show version changes">
        <span>Versions</span>
        {dimensions.map((dimension) => {
          const count = versions.changes.filter((change) => change.dimension === dimension.key).length
          return (
            <button
              key={dimension.key}
              type="button"
              aria-label={`${dimension.label} versions`}
              aria-pressed={selection[dimension.key]}
              data-measurement={dimension.measurement}
              onClick={() =>
                onChange({ ...selection, [dimension.key]: !selection[dimension.key] })
              }
            >
              {dimension.label}
              {count > 0 && <small>{count}</small>}
            </button>
          )
        })}
      </div>
      <p>
        {inEffect.length
          ? `In effect at start: ${inEffect.map((item) => versionLabel(item.dimension, item.version, locations)).join(" · ")}`
          : "No versions recorded before this range"}
        . Solid lines change agent behavior; dashed lines change only how calls are judged.
      </p>
    </section>
  )
}
