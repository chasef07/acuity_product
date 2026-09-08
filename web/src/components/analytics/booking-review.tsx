"use client"

import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { OperatorAnalyticsDetailView } from "@/components/workspace/operator-analytics-detail"
import { portalClient } from "@/lib/api/client"
import {
  queryBookingNonConversions,
  getOperatorAiInteractionAnalytics,
} from "@/lib/api/generated/sdk.gen"
import type {
  BookingNonConversions,
  Location,
  OperatorAiInteractionAnalytics,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import {
  failureBuckets,
  parseBookingReviewNotes,
  scenarioMarkdown,
  type BookingReviewNotes,
  type BookingReviewNote,
  type FailureBucket,
} from "@/lib/booking-review"
import { formatPercent, formatDay } from "@/lib/booking-analytics"

type Props = {
  practiceID: string
  actorSubject: string
  locationID?: string
  days: 7 | 30 | 90
  timeZone: string
  locations: Location[]
  onClose: () => void
  onDirtyChange: (dirty: boolean) => void
}

export function BookingReview(props: Props) {
  const { practiceID, locationID, days, timeZone } = props
  const [request, setRequest] = useState<{
    data?: BookingNonConversions
    error?: string
  }>({})
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    async function load() {
      try {
        const token = await getAccessToken()
        if (!token)
          throw new Error("Your session needs Platform Operator access.")
        const result = await queryBookingNonConversions({
          client: portalClient(token),
          body: {
            practiceId: practiceID,
            locationId: locationID,
            days,
            timeZone,
          },
          signal: controller.signal,
        })
        if (!result.data)
          throw new Error(
            result.response?.status === 403
              ? "This review requires Platform Operator access."
              : "The review list couldn’t load. Retry to fetch the complete cohort.",
          )
        if (!controller.signal.aborted) setRequest({ data: result.data })
      } catch (error) {
        if (!controller.signal.aborted)
          setRequest({
            error:
              error instanceof Error
                ? error.message
                : "The review list couldn’t load.",
          })
      }
    }
    void load()
    return () => controller.abort()
  }, [practiceID, locationID, days, timeZone, retry])
  return (
    <section aria-label="Non-conversion review" className="space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold tracking-tight">
            Review non-converting calls
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Availability checked · no confirmed booking · human review
          </p>
        </div>
        <Button variant="outline" onClick={props.onClose}>
          Back to report
        </Button>
      </div>
      {request.error ? (
        <div role="alert" className="rounded-lg border p-5">
          <p>{request.error}</p>
          <Button
            className="mt-3"
            variant="outline"
            onClick={() => {
              setRequest({})
              setRetry((v) => v + 1)
            }}
          >
            Retry review
          </Button>
        </div>
      ) : request.data ? (
        <ReviewWorkspace {...props} data={request.data} />
      ) : (
        <p role="status">Loading the complete review cohort…</p>
      )}
    </section>
  )
}

function ReviewWorkspace({
  data,
  actorSubject,
  practiceID,
  locations,
  timeZone,
  onDirtyChange,
}: Props & { data: BookingNonConversions }) {
  const storageKey = `acuity:booking-review:v1:${actorSubject}:${practiceID}`
  const [initialStorage] = useState(() => {
    try {
      return {
        notes: parseBookingReviewNotes(localStorage.getItem(storageKey)),
        error: "",
      }
    } catch {
      return {
        notes: {} as BookingReviewNotes,
        error:
          "Local reviews couldn’t be read. Existing saved data has been preserved; saving is disabled.",
      }
    }
  })
  const [notes, setNotes] = useState(initialStorage.notes)
  const storageReady = !initialStorage.error
  const [storageError, setStorageError] = useState(initialStorage.error)
  const [filter, setFilter] = useState("all")
  const [selectedID, setSelectedID] = useState(data.calls[0]?.id ?? "")
  const [dirty, setDirty] = useState(false)
  const [notice, setNotice] = useState("")
  useEffect(() => {
    if (!dirty) return
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
    }
    window.addEventListener("beforeunload", beforeUnload)
    return () => window.removeEventListener("beforeunload", beforeUnload)
  }, [dirty])
  const visible = data.calls.filter(
    (call) =>
      filter === "all" ||
      (filter === "unreviewed"
        ? !notes[call.id]
        : notes[call.id]?.bucket === filter),
  )
  const selected = visible.find((call) => call.id === selectedID) ?? visible[0]
  const reviewed = data.calls.filter((call) => notes[call.id]).length
  const draftCount = data.calls.filter(
    (call) =>
      notes[call.id]?.scenario.trim() && notes[call.id]?.expected.trim(),
  ).length
  function navigate(action: () => void) {
    if (dirty && !window.confirm("Discard the unsaved review changes?")) return
    setDirty(false)
    onDirtyChange(false)
    setNotice("")
    action()
  }
  function save(id: string, note: BookingReviewNote) {
    try {
      const next = {
        ...parseBookingReviewNotes(localStorage.getItem(storageKey)),
        [id]: note,
      }
      localStorage.setItem(storageKey, JSON.stringify(next))
      setNotes(next)
      setStorageError("")
      setDirty(false)
      onDirtyChange(false)
      setNotice("Review saved in this browser.")
      return true
    } catch {
      setStorageError(
        "This review could not be saved. Your changes remain in the editor; free browser storage and try again.",
      )
      return false
    }
  }
  function exportDrafts() {
    const url = URL.createObjectURL(
      new Blob(
        [
          scenarioMarkdown(
            notes,
            data.calls.map((call) => call.id),
          ),
        ],
        { type: "text/markdown" },
      ),
    )
    const link = document.createElement("a")
    link.href = url
    link.download = `livekit-scenario-drafts-${data.report.from}-${data.report.through}.md`
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return (
    <>
      <div className="flex flex-wrap items-center justify-between gap-4 rounded-lg border bg-muted/30 p-4 text-sm">
        <div>
          <strong className="text-lg tabular-nums">
            {data.calls.length} calls to review
          </strong>
          <p className="mt-1 text-muted-foreground">
            {data.report.total.converted} converted /{" "}
            {data.report.total.searched} availability calls ·{" "}
            {formatPercent(data.report.total.conversion)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {formatDay(data.report.from)} – {formatDay(data.report.through)} ·{" "}
            {timeZone} · complete days
          </p>
        </div>
        <div className="space-y-2 text-right">
          <p>
            {reviewed} / {data.calls.length} reviewed
          </p>
          <Button
            variant="outline"
            disabled={!draftCount}
            onClick={exportDrafts}
          >
            Export scenario drafts ({draftCount})
          </Button>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Buckets and notes stay in this browser. They do not change call outcomes
        or create Tasks. Scenario drafts are for manual use in LiveKit; use
        synthetic details.
      </p>
      {storageError && (
        <p role="alert" className="text-sm text-destructive">
          {storageError}
        </p>
      )}
      <div className="flex flex-wrap gap-2" aria-label="Failure buckets">
        {[
          { id: "all", label: "All calls" },
          { id: "unreviewed", label: "Unreviewed" },
          ...failureBuckets,
        ].map((bucket) => {
          const count = data.calls.filter(
            (call) =>
              bucket.id === "all" ||
              (bucket.id === "unreviewed"
                ? !notes[call.id]
                : notes[call.id]?.bucket === bucket.id),
          ).length
          return (
            <Button
              key={bucket.id}
              size="sm"
              variant={filter === bucket.id ? "secondary" : "outline"}
              aria-pressed={filter === bucket.id}
              onClick={() =>
                navigate(() => {
                  setFilter(bucket.id)
                  setSelectedID("")
                })
              }
            >
              {bucket.label}{" "}
              <span className="tabular-nums text-muted-foreground">
                {count}
              </span>
            </Button>
          )
        })}
      </div>
      {!visible.length ? (
        <p className="rounded-lg border border-dashed p-8 text-center text-muted-foreground">
          {data.calls.length
            ? "No calls in this bucket."
            : "No non-converting availability calls in this period."}
        </p>
      ) : (
        <div className="grid items-start gap-4 xl:grid-cols-[200px_minmax(0,1fr)]">
          <nav
            aria-label="Calls to review"
            className="max-h-96 overflow-y-auto rounded-lg border xl:sticky xl:top-4 xl:max-h-[75vh]"
          >
            {visible.map((call) => (
              <button
                key={call.id}
                type="button"
                aria-current={selected?.id === call.id ? "true" : undefined}
                className={`block w-full border-b px-4 py-3 text-left text-sm last:border-0 hover:bg-muted focus-visible:outline-2 focus-visible:outline-ring ${selected?.id === call.id ? "bg-muted border-l-2 border-l-primary" : ""}`}
                onClick={() => navigate(() => setSelectedID(call.id))}
              >
                <span className="block font-medium">
                  {new Intl.DateTimeFormat("en-US", {
                    month: "short",
                    day: "numeric",
                    hour: "numeric",
                    minute: "2-digit",
                    timeZone,
                  }).format(new Date(call.startedAt))}
                </span>
                <span className="mt-1 block text-xs text-muted-foreground">
                  {locations.find((l) => l.id === call.locationId)?.name ??
                    "Location unavailable"}{" "}
                  · {call.patientGroup}
                </span>
                <span className="mt-2 block text-xs">
                  {notes[call.id]
                    ? failureBuckets.find((b) => b.id === notes[call.id].bucket)
                        ?.label
                    : "Unreviewed"}
                </span>
              </button>
            ))}
          </nav>
          {selected && (
            <div className="grid min-w-0 items-start gap-4 lg:grid-cols-[minmax(0,1fr)_280px]">
              <div className="space-y-3 lg:order-2">
                <ReviewEditor
                  key={selected.id}
                  saved={notes[selected.id]}
                  enabled={storageReady}
                  onDirty={() => {
                    setDirty(true)
                    onDirtyChange(true)
                    setNotice("Unsaved review changes")
                  }}
                  onSave={(note) => save(selected.id, note)}
                />
                <p role="status" className="text-xs text-muted-foreground">
                  {notice}
                </p>
              </div>
              <ReviewEvidence key={selected.id} interactionID={selected.id} />
            </div>
          )}
        </div>
      )}
    </>
  )
}

function ReviewEditor({
  saved,
  enabled,
  onDirty,
  onSave,
}: {
  saved?: BookingReviewNote
  enabled: boolean
  onDirty: () => void
  onSave: (note: BookingReviewNote) => boolean
}) {
  const [bucket, setBucket] = useState<FailureBucket | "">(saved?.bucket ?? "")
  const [observation, setObservation] = useState(saved?.observation ?? "")
  const [scenario, setScenario] = useState(saved?.scenario ?? "")
  const [expected, setExpected] = useState(saved?.expected ?? "")
  return (
    <section
      aria-label="Review notes"
      className="space-y-4 rounded-lg border p-4"
    >
      <div className="flex items-center justify-between gap-3">
        <h3 className="font-medium">What prevented conversion?</h3>
        <Badge variant="outline">Local review</Badge>
      </div>
      <Select
        value={bucket || null}
        onValueChange={(value) => {
          if (value) {
            setBucket(value as FailureBucket)
            onDirty()
          }
        }}
        items={failureBuckets.map((b) => ({ value: b.id, label: b.label }))}
      >
        <SelectTrigger aria-label="Failure bucket" className="w-full">
          <SelectValue placeholder="Choose a bucket after reviewing the evidence" />
        </SelectTrigger>
        <SelectContent>
          {failureBuckets.map((b) => (
            <SelectItem key={b.id} value={b.id}>
              {b.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <label className="block space-y-2 text-sm">
        <span>Evidence / pattern notes</span>
        <Textarea
          value={observation}
          onChange={(e) => {
            setObservation(e.target.value)
            onDirty()
          }}
          placeholder="Describe the obstacle and relevant turn or tool. Avoid patient identifiers."
          maxLength={5000}
        />
      </label>
      <details className="rounded-md border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          Draft a LiveKit scenario
        </summary>
        <div className="mt-4 space-y-3">
          <p className="text-xs text-muted-foreground">
            Use a fictional caller. These two fields are exported; transcripts
            and evidence notes are excluded.
          </p>
          <Button
            size="sm"
            variant="outline"
            disabled={!bucket || Boolean(scenario || expected)}
            onClick={() => {
              const template = failureBuckets.find((b) => b.id === bucket)
              if (template) {
                setScenario(template.scenario)
                setExpected(template.expected)
                onDirty()
              }
            }}
          >
            Use bucket starter
          </Button>
          <label className="block space-y-2 text-sm">
            <span>Synthetic caller scenario</span>
            <Textarea
              value={scenario}
              onChange={(e) => {
                setScenario(e.target.value)
                onDirty()
              }}
              maxLength={5000}
            />
          </label>
          <label className="block space-y-2 text-sm">
            <span>Expected behavior / pass criteria</span>
            <Textarea
              value={expected}
              onChange={(e) => {
                setExpected(e.target.value)
                onDirty()
              }}
              maxLength={5000}
            />
          </label>
        </div>
      </details>
      <Button
        disabled={!bucket || !enabled}
        onClick={() => {
          if (bucket)
            onSave({
              bucket,
              observation,
              scenario,
              expected,
              savedAt: new Date().toISOString(),
            })
        }}
      >
        Save review locally
      </Button>
    </section>
  )
}

function ReviewEvidence({ interactionID }: { interactionID: string }) {
  const [request, setRequest] = useState<{
    detail?: OperatorAiInteractionAnalytics
    error?: string
  }>({})
  const [retry, setRetry] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    async function load() {
      try {
        const token = await getAccessToken()
        if (!token) throw new Error("Sign in again to read this call.")
        const result = await getOperatorAiInteractionAnalytics({
          client: portalClient(token),
          path: { interactionId: interactionID },
          signal: controller.signal,
        })
        if (!result.data)
          throw new Error(
            "Call evidence couldn’t load. Retry before drawing a conclusion.",
          )
        if (!controller.signal.aborted) setRequest({ detail: result.data })
      } catch (error) {
        if (!controller.signal.aborted)
          setRequest({
            error:
              error instanceof Error
                ? error.message
                : "Call evidence unavailable.",
          })
      }
    }
    void load()
    return () => controller.abort()
  }, [interactionID, retry])
  return (
    <section
      aria-label="Selected call evidence"
      className="max-h-[75vh] min-w-0 overflow-y-auto rounded-lg border"
    >
      {request.detail ? (
        <OperatorAnalyticsDetailView detail={request.detail} />
      ) : request.error ? (
        <div role="alert" className="space-y-3 p-5">
          <p>{request.error}</p>
          <Button
            variant="outline"
            onClick={() => {
              setRequest({})
              setRetry((v) => v + 1)
            }}
          >
            Retry evidence
          </Button>
        </div>
      ) : (
        <p role="status" className="p-5">
          Loading transcript and tool evidence…
        </p>
      )}
    </section>
  )
}
