import { portalAPIURL } from "../api/client"
import {
  getCallingCall,
  getTaskOutboundEligibility,
  issueCallingRecordingPlayback,
  issueCallingVoicemailPlayback,
  readTask,
} from "../api/generated/sdk.gen"
import type { CallingCall, Task } from "../api/generated/types.gen"
import { readFailure, useRead } from "../data/queries"
import {
  type PortalFailure,
  type PortalOutcome,
  portalRequest,
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
  task: Pick<Task, "id" | "callId" | "interactions" | "relatedInteractionCount">,
): RecoverySource {
  const loaded = task.relatedInteractionCount > 0 &&
    task.interactions.length === task.relatedInteractionCount
  const detail = useRead({
    live: "task-detail",
    name: "task",
    identity: { taskID: task.id },
    enabled: !loaded,
    read: (identity, transport) => readTask({ ...transport, path: { taskId: identity.taskID } }),
  })
  const interactions = loaded
    ? task.interactions
    : detail.status === "ready" ? detail.data.interactions : []
  const callID =
    loaded || detail.status === "ready"
      ? (newestRecoveryInteraction(interactions)?.callId ?? task.callId)
      : undefined
  const call = useRead({
    live: "task-detail",
    name: "calling-call",
    identity: { callID },
    enabled: Boolean(callID),
    read: (identity, transport) =>
      getCallingCall({ ...transport, path: { callId: identity.callID ?? "" } }),
  })
  const failure = (loaded ? undefined : readFailure(detail)) ?? readFailure(call)
  return {
    interactions,
    ...(call.status === "ready" ? { call: call.data } : {}),
    ...(failure ? { failure } : {}),
  }
}

export function useTaskCallEligibility({
  task,
  enabled,
}: {
  task: Pick<Task, "id">
  enabled: boolean
}) {
  return useRead({
    live: "task-detail",
    name: "task-call-eligibility",
    identity: { taskID: task.id },
    enabled,
    read: (identity, transport) =>
      getTaskOutboundEligibility({ ...transport, path: { taskId: identity.taskID } }),
  })
}
