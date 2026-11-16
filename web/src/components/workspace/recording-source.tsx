"use client"

import { useEffect, useRef, useState } from "react"
import { AudioLinesIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { CallingCall, Task } from "@/lib/api/generated/types.gen"
import {
  issueRecordingPlayback,
  newestRecoveryInteraction,
  type RecordingKind,
  useRecoverySource,
} from "@/lib/clients/calls"
import { completeTask, readTask } from "@/lib/clients/tasks"
import { formatTimestamp } from "@/lib/format-time"
import { callStateLabels, formatClock } from "@/lib/vocabulary"

export function RecoveryTaskSource({
  task,
  onUpdated,
}: {
  task: Task
  onUpdated: (task: Task, advance?: boolean) => void
}) {
  const { call, interactions, failure } = useRecoverySource(task)
  const error =
    failure && failure.kind !== "signedOut"
      ? "Recovery source is temporarily unavailable."
      : ""

  const selected = newestRecoveryInteraction(interactions)
  const otherInteractions = [...interactions]
    .sort((left, right) => right.occurredAt.localeCompare(left.occurredAt))
    .filter((interaction) => interaction.callId !== selected?.callId)
  const otherCallOrder = otherInteractions.every(
    (interaction) => interaction.occurredAt < (selected?.occurredAt ?? ""),
  )
    ? "earlier"
    : "other"

  return (
    <section aria-label="Call recovery source" className="mt-4">
      {error && <p className="mt-2 text-sm text-destructive">{error}</p>}
      {call?.voicemail && <VoicemailSource key={call.id} call={call} compact onUpdated={onUpdated} taskID={task.id} />}
      {otherInteractions.length > 0 && (
        <details className="mt-3 text-xs text-muted-foreground">
          <summary className="cursor-pointer">
            {otherInteractions.length} {otherCallOrder}{" "}
            {otherInteractions.length === 1 ? "call" : "calls"}
          </summary>
          <ul className="mt-2 space-y-1 pl-4">
            {otherInteractions.map((interaction) => (
              <li key={interaction.callId}>
                {callStateLabels[interaction.type === "VOICEMAIL" ? "VOICEMAIL" : "MISSED"]}
                {" · "}
                {formatTimestamp(interaction.occurredAt)}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  )
}

export function VoicemailSource({ call, compact = false, taskID, onUpdated }: {
  call: CallingCall
  compact?: boolean
  taskID?: string
  onUpdated?: (task: Task, advance?: boolean) => void
}) {
  const reviewedTask = useRef<Task | undefined>(undefined)
  const [completionError, setCompletionError] = useState("")
  const voicemail = call.voicemail
  if (!voicemail) return null
  async function captureReview() {
    reviewedTask.current = undefined
    setCompletionError("")
    const id = taskID ?? call.recoveryTask?.id
    if (!id) return
    const result = await readTask(id)
    if (result.ok && result.data.origin === "VOICEMAIL_RECOVERY" && result.data.state === "OPEN") {
      const latestCallID = newestRecoveryInteraction(result.data.interactions)?.callId ?? result.data.callId
      if (latestCallID === call.id) reviewedTask.current = result.data
      else setCompletionError("A newer call needs review. This playback will keep that work open.")
    }
    else if (!result.ok) setCompletionError("Could not load voicemail work. Use Mark done after listening.")
  }
  async function finishReview() {
    const task = reviewedTask.current
    if (!task) return
    reviewedTask.current = undefined
    const result = await completeTask(task)
    if (result.ok) onUpdated?.(result.data)
    else setCompletionError("Could not mark voicemail done. Review any new activity and use Mark done.")
  }
  return <>
    <RecordingSource call={call} audioState={voicemail.audioState} durationSeconds={voicemail.durationSeconds} kind="voicemail" compact={compact} unavailable={voicemail.outcome === "MISSED_CALL"} onBeforePlay={captureReview} onEnded={() => void finishReview()} />
    {completionError && <p role="alert" className="px-5 py-2 text-xs text-destructive">{completionError}</p>}
  </>
}

export function CallRecordingSource({ call }: { call: CallingCall }) {
  if (!call.recording) return null
  return (
    <RecordingSource
      call={call}
      audioState={call.recording.audioState}
      durationSeconds={call.recording.durationSeconds}
      kind="call"
    />
  )
}

const recordingPresentation = {
  voicemail: {
    title: "Voicemail",
    label: "voicemail",
    audioLabel: "Voicemail recording",
  },
  call: {
    title: "Call recording",
    label: "call recording",
    audioLabel: "Call recording",
  },
} as const

function RecordingSource({
  call,
  audioState,
  durationSeconds,
  kind,
  compact = false,
  unavailable = false,
  onBeforePlay,
  onEnded,
}: {
  call: CallingCall
  audioState: "PROCESSING" | "READY" | "UNAVAILABLE" | "EXPIRED" | "DELETED" | undefined
  durationSeconds: number
  kind: RecordingKind
  compact?: boolean
  unavailable?: boolean
  onBeforePlay?: () => Promise<void>
  onEnded?: () => void
}) {
  const [audioURL, setAudioURL] = useState("")
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const audioRef = useRef<HTMLAudioElement>(null)
  const presentation = recordingPresentation[kind]

  useEffect(() => {
    if (audioURL) void audioRef.current?.play().catch(() => undefined)
  }, [audioURL])

  const stateLabel =
    unavailable
      ? "No voicemail recording."
      : audioState === "READY"
        ? "Ready"
        : audioState === "EXPIRED" || audioState === "DELETED"
          ? "Recording expired"
        : audioState === "UNAVAILABLE"
          ? "Recording unavailable"
          : "Processing"

  async function loadAudio() {
    setLoading(true)
    setError("")
    await onBeforePlay?.()
    const playback = await issueRecordingPlayback(kind, call.id)
    setLoading(false)
    if (playback.ok) setAudioURL(playback.data)
    else if (playback.failure.kind !== "signedOut") {
      setError("Playback authorization is unavailable.")
    }
  }

  return (
    <section className={compact ? "mt-4" : "border-t px-5 py-4"}>
      <div className="flex items-start gap-3">
        <AudioLinesIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <h2 className="text-sm font-semibold">{presentation.title}</h2>
          <p className="mt-0.5 text-xs text-muted-foreground">
            {stateLabel}
            {durationSeconds > 0 && ` · ${formatClock(durationSeconds)}`}
          </p>
        </div>
        {audioState === "READY" && !audioURL && (
          <Button
            size="sm"
            variant="outline"
            disabled={loading}
            onClick={() => void loadAudio()}
          >
            {loading ? <Spinner /> : "Play"}
          </Button>
        )}
      </div>
      {audioURL && (
        <audio
          ref={audioRef}
          aria-label={presentation.audioLabel}
          controls
          controlsList="nodownload"
          preload="metadata"
          src={audioURL}
          onEnded={onEnded}
          onError={() => setError(`The ${presentation.label} could not be opened.`)}
          className="mt-3 h-9 max-w-full"
        />
      )}
      {error && <p className="mt-2 text-xs text-destructive">{error}</p>}
    </section>
  )
}
