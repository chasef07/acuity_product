import { portalAPIURL } from "../api/client"
import {
  getCallingCall,
  getTaskOutboundEligibility,
  issueCallingRecordingPlayback,
  issueCallingVoicemailPlayback,
  readTask,
} from "../api/generated/sdk.gen"
import type { CallingCall, Task } from "../api/generated/types.gen"
import {
  type PortalFailure,
  type PortalOutcome,
  portalRequest,
  usePortalQuery,
} from "./portal-request"

export type RecordingKind = "voicemail" | "call"

/** Issues a short-lived playback capability and resolves with its audio URL. */
export async function issueRecordingPlayback(
  kind: RecordingKind,
  callID: string,
): Promise<PortalOutcome<string>> {
  const issuePlayback =
    kind === "voicemail" ? issueCallingVoicemailPlayback : issueCallingRecordingPlayback
  const outcome = await portalRequest((transport) =>
    issuePlayback({ ...transport, path: { callId: callID } }),
  )
  if (!outcome.ok) return outcome
  const playbackPath = kind === "voicemail" ? "voicemail-playback" : "recording-playback"
  return {
    ok: true,
    data: new URL(
      `/v1/calling/${playbackPath}/${encodeURIComponent(outcome.data.token)}`,
      portalAPIURL(),
    ).toString(),
  }
}

/** The recovery Interaction to present: the newest voicemail, else the newest call. */
export function newestRecoveryInteraction(interactions: Task["interactions"]) {
  const newestFirst = [...interactions].sort((left, right) =>
    right.occurredAt.localeCompare(left.occurredAt),
  )
  return (
    newestFirst.find((interaction) => interaction.type === "VOICEMAIL") ??
    newestFirst[0]
  )
}

export type RecoverySource = {
  interactions: Task["interactions"]
  call?: CallingCall
  /** The latest failed read of the Task or of its source call. */
  failure?: PortalFailure
}

/**
 * A recovery Task's linked Interactions and the call behind the newest one.
 * `revision` changes reload it; the last source stays shown meanwhile.
 */
export function useRecoverySource(
  task: Pick<Task, "id" | "version" | "callId">,
  revision: number,
): RecoverySource {
  const taskKey = `${task.id}:${task.version}:${task.callId ?? ""}:${revision}`
  const detail = usePortalQuery(
    taskKey,
    (transport) => readTask({ ...transport, path: { taskId: task.id } }),
    { keepPrevious: true },
  )
  const interactions = detail.status === "loading" ? [] : (detail.data?.interactions ?? [])
  // The source call reloads only after the current Task read lands.
  const callID =
    detail.status === "ready" && !detail.refreshing
      ? (newestRecoveryInteraction(interactions)?.callId ?? task.callId)
      : undefined
  const call = usePortalQuery(
    callID ? `${callID}:${taskKey}` : null,
    (transport) => getCallingCall({ ...transport, path: { callId: callID ?? "" } }),
    { keepPrevious: true },
  )
  const shownCall = call.status === "loading" ? undefined : call.data
  const failure =
    detail.status === "failed"
      ? detail.failure
      : call.status === "failed"
        ? call.failure
        : undefined
  return {
    interactions,
    ...(shownCall ? { call: shownCall } : {}),
    ...(failure ? { failure } : {}),
  }
}

/**
 * Whether staff may call this Task now. Loading until the first answer;
 * `historyHint` changes recheck it while the last answer stays shown.
 */
export function useTaskCallEligibility({
  task,
  historyHint,
  enabled,
}: {
  task: Pick<Task, "id" | "version" | "state">
  historyHint: number
  enabled: boolean
}) {
  return usePortalQuery(
    enabled ? `${task.id}:${task.state}:${task.version}:${historyHint}` : null,
    (transport) =>
      getTaskOutboundEligibility({ ...transport, path: { taskId: task.id } }),
    { keepPrevious: true },
  )
}
