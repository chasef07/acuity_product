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
  failure?: PortalFailure
}

export function useRecoverySource(
  task: Pick<Task, "id" | "version" | "callId" | "interactions" | "relatedInteractionCount">,
  revision: number,
): RecoverySource {
  const taskKey = `${task.id}:${task.version}:${task.callId ?? ""}:${revision}`
  const loaded = task.relatedInteractionCount > 0 &&
    task.interactions.length === task.relatedInteractionCount
  const detail = usePortalQuery(
    loaded ? null : taskKey,
    (transport) => readTask({ ...transport, path: { taskId: task.id } }),
    { keepPrevious: true },
  )
  const interactions = loaded
    ? task.interactions
    : detail.status === "loading" ? [] : (detail.data?.interactions ?? [])
  const callID =
    loaded || (detail.status === "ready" && !detail.refreshing)
      ? (newestRecoveryInteraction(interactions)?.callId ?? task.callId)
      : undefined
  const call = usePortalQuery(
    callID ? `${callID}:${taskKey}` : null,
    (transport) => getCallingCall({ ...transport, path: { callId: callID ?? "" } }),
    { keepPrevious: true },
  )
  const shownCall = call.status === "loading" ? undefined : call.data
  const failure =
    !loaded && detail.status === "failed"
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
