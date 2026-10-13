import type { CallingState } from "../api/generated/types.gen.ts"
import type { CallingHintSignal } from "./calling-hints.ts"

const callingChangeHintGraceMilliseconds = 2_000

type CallingChangeContext = { hidden: boolean; covered: boolean }

type CallingChangeRead = CallingChangeContext & {
  startedAt: number
  hinted: boolean
  report?: string
}

type Discovery = "hint" | "late_hint" | "missed"

type PendingChange = { startedAt: number; context: string }

export function createCallingChangeMetric() {
  let previousContent: string | undefined
  let previousRinging = false
  let coverageInterrupted = false
  let unconsumedHint = false
  let lastHintAt = Number.NEGATIVE_INFINITY
  let pending: PendingChange[] = []
  const reports: string[] = []

  function settle(change: PendingChange, hintAt: number) {
    const late = hintAt - change.startedAt <= callingChangeHintGraceMilliseconds
    reports.push(report(late ? "late_hint" : "missed", change.context))
    return late
  }

  function expire(now: number) {
    pending = pending.filter((change) => {
      if (now - change.startedAt <= callingChangeHintGraceMilliseconds) return true
      settle(change, now)
      return false
    })
  }

  return {
    signal(signal: CallingHintSignal, now: number) {
      if (signal !== "hint") {
        coverageInterrupted = true
        return
      }
      lastHintAt = now
      let credited = false
      for (const change of pending) credited = settle(change, now) || credited
      pending = []
      if (!credited) unconsumedHint = true
    },

    beginRead(now: number, context: CallingChangeContext): CallingChangeRead {
      expire(now)
      const hinted = unconsumedHint
      const covered = context.covered && !coverageInterrupted
      unconsumedHint = false
      coverageInterrupted = false
      return { ...context, covered, startedAt: now, hinted, report: reports.shift() }
    },

    readFailed(read: CallingChangeRead) {
      if (read.hinted) unconsumedHint = true
      if (!read.covered) coverageInterrupted = true
    },

    observe(read: CallingChangeRead, state: CallingState) {
      const content = callingContent(state)
      const changed = previousContent !== undefined && previousContent !== content
      const ringing = previousRinging || state.ringing.length > 0
      previousContent = content
      previousRinging = state.ringing.length > 0
      if (!changed) return
      const context = changeContext(read, ringing)
      const change = { startedAt: read.startedAt, context }
      if (read.hinted) reports.push(report("hint", context))
      else if (lastHintAt < read.startedAt) pending.push(change)
      else if (settle(change, lastHintAt)) unconsumedHint = false
    },

    reset() {
      previousContent = undefined
      previousRinging = false
      coverageInterrupted = false
      unconsumedHint = false
      lastHintAt = Number.NEGATIVE_INFINITY
      pending = []
      reports.length = 0
    },
  }
}

function callingContent(state: CallingState) {
  return JSON.stringify([
    state.softphone.activeCallId,
    state.softphone.pendingOutcomeCallId,
    state.ringing,
    state.staffTransfers,
    state.bridged,
    state.voicemail,
    state.disposition,
  ])
}

function changeContext(read: CallingChangeRead, ringing: boolean) {
  return [
    `hints=${read.covered ? "covered" : "uncovered"}`,
    `tab=${read.hidden ? "hidden" : "visible"}`,
    `offer=${ringing ? "ringing" : "none"}`,
  ].join(";")
}

function report(discovery: Discovery, context: string) {
  return `discovery=${discovery};${context}`
}
