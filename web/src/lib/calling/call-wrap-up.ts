import type {
  CallingCall,
  CallingDispositionClosure,
  CallingDispositionRequest,
} from "../api/generated/types.gen.ts"

export type WrapUpReason = CallingDispositionClosure["reason"]

export type WrapUpCall = Pick<CallingCall, "direction" | "entryPoint" | "taskId">

export type WrapUpState = {
  ticks: ReadonlyMap<string, WrapUpReason>
  followUp: boolean
}

export type WrapUpRequest = {
  outcome: CallingDispositionRequest["outcome"]
  closures: CallingDispositionClosure[]
}

export function wrapUpOffersFollowUp(call: WrapUpCall) {
  return call.direction === "INBOUND" || call.entryPoint !== "TASK"
}

export function wrapUpRequest(call: WrapUpCall, state: WrapUpState): WrapUpRequest {
  return {
    outcome: wrapUpOutcome(call, state),
    closures: [...state.ticks].map(([taskId, reason]) => ({ taskId, reason })),
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

export function wrapUpDuration(seconds: number) {
  const total = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(total / 3_600)
  const minutes = Math.floor((total % 3_600) / 60)
  const rest = total % 60
  if (hours > 0) return `${hours}h ${String(minutes).padStart(2, "0")}m`
  if (minutes > 0) return `${minutes}m ${String(rest).padStart(2, "0")}s`
  return `${rest}s`
}
