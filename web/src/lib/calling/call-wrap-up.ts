import type {
  CallingCall,
  CallingDispositionClosure,
  CallingDispositionRequest,
  CallingDispositionResult,
} from "../api/generated/types.gen.ts"

export type WrapUpReason = CallingDispositionClosure["reason"]

export type WrapUpCall = Pick<CallingCall, "direction" | "entryPoint" | "taskId">

export type WrapUpTick = Pick<CallingDispositionClosure, "reason" | "expectedVersion">

export type WrapUpState = {
  ticks: ReadonlyMap<string, WrapUpTick>
  followUp: boolean
}

export type WrapUpRequest = {
  outcome: CallingDispositionRequest["outcome"]
  closures: CallingDispositionClosure[]
}

export type WrapUpShortfall = {
  changedTaskIds: string[]
  unclosedTaskIds: string[]
  followUpNotCreated: boolean
}

export function wrapUpOffersFollowUp(call: WrapUpCall) {
  return call.direction === "INBOUND" || call.entryPoint !== "TASK"
}

export function wrapUpRequest(call: WrapUpCall, state: WrapUpState): WrapUpRequest {
  return {
    outcome: wrapUpOutcome(call, state),
    closures: [...state.ticks].map(([taskId, tick]) => ({ taskId, ...tick })),
  }
}

function wrapUpOutcome(call: WrapUpCall, state: WrapUpState): WrapUpRequest["outcome"] {
  if (call.direction === "INBOUND") {
    return state.followUp ? "FOLLOW_UP_REQUIRED" : "RESOLVED"
  }
  if (call.entryPoint === "TASK") {
    return call.taskId !== undefined && state.ticks.has(call.taskId) ? "COMPLETE_TASK" : "KEEP_OPEN"
  }
  return state.followUp ? "CREATE_TASK" : "RESOLVED"
}

export function wrapUpShortfall(
  request: WrapUpRequest,
  result: Pick<
    CallingDispositionResult,
    "closedTaskIds" | "alreadyClosedTaskIds" | "changedTaskIds" | "recordedOutcome"
  >,
): WrapUpShortfall | undefined {
  const closed = new Set([...(result.closedTaskIds ?? []), ...(result.alreadyClosedTaskIds ?? [])])
  const changed = new Set(result.changedTaskIds ?? [])
  const open = request.closures.map((closure) => closure.taskId).filter((taskID) => !closed.has(taskID))
  const shortfall = {
    changedTaskIds: open.filter((taskID) => changed.has(taskID)),
    unclosedTaskIds: open.filter((taskID) => !changed.has(taskID)),
    followUpNotCreated:
      (request.outcome === "FOLLOW_UP_REQUIRED" || request.outcome === "CREATE_TASK") &&
      result.recordedOutcome !== undefined &&
      result.recordedOutcome !== request.outcome,
  }
  return open.length > 0 || shortfall.followUpNotCreated ? shortfall : undefined
}

export function wrapUpCounts(counts: {
  openTaskCount: number | undefined
  closedInSummary: number
  listed: number
  ticked: number
}) {
  const open = Math.max(counts.listed, (counts.openTaskCount ?? 0) - counts.closedInSummary)
  const kept = open - counts.ticked
  return {
    open,
    closeLabel: kept > 0 ? `Close ${counts.ticked} · keep ${kept} open` : `Close ${counts.ticked}`,
  }
}

export function wrapUpDuration(seconds: number) {
  const total = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(total / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  const rest = total % 60
  if (hours > 0) return `${hours}h ${String(minutes).padStart(2, "0")}m`
  if (minutes > 0) return `${minutes}m ${String(rest).padStart(2, "0")}s`
  return `${rest}s`
}
