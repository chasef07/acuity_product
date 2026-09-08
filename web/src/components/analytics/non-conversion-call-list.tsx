"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { ArrowLeftIcon, ArrowUpRightIcon } from "lucide-react"
import { AcuityMark } from "@/components/acuity-mark"
import { PatientStatusControl } from "@/components/analytics/patient-status-control"
import {
  readPatientStatusOverrides,
  type PatientStatusOverrides,
  type ReviewPatientStatus,
} from "@/lib/call-patient-status"
import { CallFailureTags } from "@/components/analytics/call-failure-tags"
import { Badge } from "@/components/ui/badge"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  changeCallTag,
  readCallReviewTags,
  type CallReviewTags,
  type TagChange,
} from "@/lib/call-review-tags"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { OperatorAnalyticsDetailSheet } from "@/components/workspace/operator-analytics-detail"
import { portalClient } from "@/lib/api/client"
import {
  discoverAccess,
  queryBookingNonConversions,
} from "@/lib/api/generated/sdk.gen"
import type {
  BookingNonConversions,
  PracticeAccess,
} from "@/lib/api/generated/types.gen"
import { getAccessToken } from "@/lib/auth-client"
import { formatDay, formatPercent } from "@/lib/booking-analytics"

type Loaded = {
  storageKey: string
  cohort: BookingNonConversions
  practice: PracticeAccess
  timeZone: string
}

export function NonConversionCallList() {
  const [request, setRequest] = useState<{
    data?: Loaded
    error?: string
    signIn?: boolean
  }>({})
  const [retry, setRetry] = useState(0)
  const [tags, setTags] = useState<CallReviewTags>({ tags: [], calls: {} })
  const [patientStatuses, setPatientStatuses] =
    useState<PatientStatusOverrides>({})
  const [patientStatusMessage, setPatientStatusMessage] = useState("")
  const [patientStatusError, setPatientStatusError] = useState("")
  const [tagError, setTagError] = useState("")
  const [tagFilter, setTagFilter] = useState("all")
  const [selectedID, setSelectedID] = useState("")
  useEffect(() => {
    const controller = new AbortController()
    async function load() {
      try {
        const token = await getAccessToken()
        if (!token) {
          if (!controller.signal.aborted)
            setRequest({ error: "Sign in to view these calls.", signIn: true })
          return
        }
        const client = portalClient(token)
        const access = await discoverAccess({
          client,
          signal: controller.signal,
        })
        if (!access.data?.platformOperator)
          throw new Error("This call review requires Platform Operator access.")
        const requestedPractice = new URLSearchParams(
          window.location.search,
        ).get("practice")
        const practice = requestedPractice
          ? access.data.practices.find((p) => p.id === requestedPractice)
          : access.data.practices[0]
        if (!practice)
          throw new Error(
            "No authorized Practice is available for this review.",
          )
        const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone
        const result = await queryBookingNonConversions({
          client,
          body: { practiceId: practice.id, days: 7, timeZone },
          signal: controller.signal,
        })
        if (!result.data)
          throw new Error(
            "The call list couldn’t load. Retry to load the complete list.",
          )
        if (!controller.signal.aborted) {
          const storageKey = `acuity:call-tags:v1:${access.data.actor.subject}:${practice.id}`
          try {
            setTags(readCallReviewTags(localStorage.getItem(storageKey)))
            setTagError("")
          } catch {
            setTagError(
              "Saved tags couldn’t be read. Existing data is preserved; tags cannot be changed until storage is available.",
            )
          }
          try {
            setPatientStatuses(
              readPatientStatusOverrides(
                localStorage.getItem(
                  storageKey.replace(
                    "acuity:call-tags:v1:",
                    "acuity:call-patient-status:v1:",
                  ),
                ),
              ),
            )
            setPatientStatusError("")
          } catch {
            setPatientStatusError(
              "Saved patient status corrections couldn’t be read. Existing data is preserved.",
            )
          }
          setRequest({
            data: { cohort: result.data, practice, timeZone, storageKey },
          })
        }
      } catch (error) {
        if (!controller.signal.aborted)
          setRequest({
            error:
              error instanceof Error
                ? error.message
                : "The call list couldn’t load.",
          })
      }
    }
    void load()
    return () => controller.abort()
  }, [retry])
  const data = request.data
  const storageKey = data?.storageKey
  useEffect(() => {
    if (!storageKey) return
    function sync(event: StorageEvent) {
      if (event.key !== storageKey && event.key !== null) return
      try {
        setTags(readCallReviewTags(localStorage.getItem(storageKey!)))
        setTagError("")
      } catch {
        setTagError(
          "Saved tags couldn’t be read. Your existing data is preserved.",
        )
      }
    }
    window.addEventListener("storage", sync)
    return () => window.removeEventListener("storage", sync)
  }, [storageKey])
  const patientStatusKey = storageKey?.replace(
    "acuity:call-tags:v1:",
    "acuity:call-patient-status:v1:",
  )
  useEffect(() => {
    if (!patientStatusKey) return
    function sync(event: StorageEvent) {
      if (event.key !== patientStatusKey && event.key !== null) return
      try {
        setPatientStatuses(
          readPatientStatusOverrides(localStorage.getItem(patientStatusKey!)),
        )
        setPatientStatusError("")
        setPatientStatusMessage("")
      } catch {
        setPatientStatusError(
          "Saved patient status corrections couldn’t be read. Existing data is preserved.",
        )
      }
    }
    window.addEventListener("storage", sync)
    return () => window.removeEventListener("storage", sync)
  }, [patientStatusKey])
  function updatePatientStatus(
    callID: string,
    original: ReviewPatientStatus,
    value: ReviewPatientStatus,
  ) {
    if (!patientStatusKey) return
    try {
      const next = {
        ...readPatientStatusOverrides(localStorage.getItem(patientStatusKey)),
      }
      if (value === original) delete next[callID]
      else next[callID] = value
      localStorage.setItem(patientStatusKey, JSON.stringify(next))
      setPatientStatuses(next)
      setPatientStatusError("")
      setPatientStatusMessage(
        "Patient status saved locally. Tags and booking totals are unchanged.",
      )
    } catch {
      setPatientStatusMessage("")
      setPatientStatusError(
        "Patient status couldn’t be saved. Check browser storage and try again.",
      )
    }
  }
  const selectedCall = data?.cohort.calls.find((call) => call.id === selectedID)
  function updateTag(change: TagChange) {
    if (!storageKey) return false
    try {
      const next = changeCallTag(
        readCallReviewTags(localStorage.getItem(storageKey)),
        change,
      )
      localStorage.setItem(storageKey, JSON.stringify(next))
      setTags(next)
      setTagError("")
      return true
    } catch {
      setTagError(
        "This tag couldn’t be saved. Check browser storage and try again.",
      )
      return false
    }
  }
  const visibleCalls =
    data?.cohort.calls
      .map((call, index) => ({ call, index }))
      .filter(
        ({ call }) =>
          tagFilter === "all" ||
          (tagFilter === "untagged"
            ? !tags.calls[call.id]?.length
            : tags.calls[call.id]?.includes(tagFilter.slice(4))),
      ) ?? []
  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b">
        <div className="mx-auto flex max-w-6xl items-center justify-between gap-4 px-5 py-5 sm:px-8">
          <div className="flex items-center gap-3">
            <AcuityMark className="size-7" />
            <span className="text-sm font-medium">Call review</span>
          </div>
          <Link
            href="/workspace"
            className="inline-flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeftIcon className="size-4" />
            Workspace
          </Link>
        </div>
      </header>
      <main className="mx-auto max-w-6xl space-y-6 px-5 py-8 sm:px-8">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight">
            Non-converting calls
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Calls that checked availability without a confirmed booking. Click a
            call to read its transcript.
          </p>
        </div>
        {request.error ? (
          <div role="alert" className="space-y-3 rounded-lg border p-5">
            <p>{request.error}</p>
            {request.signIn ? (
              <Link
                className="underline"
                href="/sign-in?next=%2Fworkspace%2Fnon-conversions"
              >
                Sign in
              </Link>
            ) : (
              <Button
                variant="outline"
                onClick={() => {
                  setRequest({})
                  setRetry((v) => v + 1)
                }}
              >
                Retry
              </Button>
            )}
          </div>
        ) : !data ? (
          <p role="status">Loading calls and booking totals…</p>
        ) : (
          <>
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-muted-foreground">
              <span>{data.practice.name}</span>
              <span>
                {formatDay(data.cohort.report.from)} –{" "}
                {formatDay(data.cohort.report.through)} · {data.timeZone}
              </span>
            </div>
            <dl className="grid grid-cols-1 divide-y overflow-hidden rounded-xl border sm:grid-cols-3 sm:divide-x sm:divide-y-0">
              <Metric
                label="Confirmed bookings"
                value={String(data.cohort.report.total.bookings)}
                caption="In the selected seven-day period"
              />
              <Metric
                label="Conversion rate"
                value={formatPercent(data.cohort.report.total.conversion)}
                caption={`${data.cohort.report.total.converted} of ${data.cohort.report.total.searched} availability-check calls`}
              />
              <Metric
                label="Non-converting calls"
                value={String(data.cohort.calls.length)}
                caption="Click any call below to review"
              />
            </dl>
            <div className="flex flex-wrap items-center gap-3">
              <Select
                value={tagFilter}
                onValueChange={(value) => {
                  if (value) setTagFilter(value)
                }}
                items={[
                  { value: "all", label: "All failure tags" },
                  { value: "untagged", label: "Untagged calls" },
                  ...tags.tags.map((tag) => ({
                    value: `tag:${tag}`,
                    label: tag,
                  })),
                ]}
              >
                <SelectTrigger
                  aria-label="Filter by failure tag"
                  className="w-64"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All failure tags</SelectItem>
                  <SelectItem value="untagged">Untagged calls</SelectItem>
                  {tags.tags.map((tag) => (
                    <SelectItem key={tag} value={`tag:${tag}`}>
                      {tag}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <p className="text-sm text-muted-foreground">
                Open a call to create or apply failure tags.
              </p>
            </div>
            {tagError && (
              <p role="alert" className="text-sm text-destructive">
                {tagError}
              </p>
            )}
            <p role="status" className="text-xs text-muted-foreground">
              {patientStatusMessage ||
                "Patient status corrections save locally. Original classifications and booking totals are preserved."}
            </p>
            {patientStatusError && (
              <p role="alert" className="text-sm text-destructive">
                {patientStatusError}
              </p>
            )}
            <section
              aria-label="Non-converting call list"
              className="overflow-hidden rounded-xl border"
            >
              <div className="flex items-center justify-between border-b px-4 py-4">
                <h2 className="font-medium">Calls to review</h2>
                <span className="text-sm tabular-nums text-muted-foreground">
                  {visibleCalls.length} of {data.cohort.calls.length} calls
                </span>
              </div>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">Call</TableHead>
                    <TableHead>Date & time</TableHead>
                    <TableHead>Office</TableHead>
                    <TableHead>Patient status</TableHead>
                    <TableHead>Failure tags</TableHead>
                    <TableHead>
                      <span className="sr-only">Open transcript</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleCalls.map(({ call, index }) => (
                    <TableRow
                      key={call.id}
                      className="cursor-pointer hover:bg-muted/60"
                      onClick={() => setSelectedID(call.id)}
                    >
                      <TableCell className="pl-4 font-medium tabular-nums">
                        {String(index + 1).padStart(3, "0")}
                      </TableCell>
                      <TableCell className="whitespace-nowrap">
                        {new Intl.DateTimeFormat("en-US", {
                          month: "short",
                          day: "numeric",
                          hour: "numeric",
                          minute: "2-digit",
                          timeZone: data.timeZone,
                        }).format(new Date(call.startedAt))}
                      </TableCell>
                      <TableCell>
                        {data.practice.locations.find(
                          (l) => l.id === call.locationId,
                        )?.name ?? "Location unavailable"}
                      </TableCell>
                      <TableCell>
                        <PatientStatusControl
                          original={call.patientGroup}
                          correction={patientStatuses[call.id]}
                          label={`Patient status for call ${index + 1}`}
                          onChange={(value) =>
                            updatePatientStatus(
                              call.id,
                              call.patientGroup,
                              value,
                            )
                          }
                        />
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-wrap gap-1">
                          {tags.calls[call.id]?.length ? (
                            tags.calls[call.id].map((tag) => (
                              <Badge key={tag} variant="secondary">
                                {tag}
                              </Badge>
                            ))
                          ) : (
                            <span className="text-xs text-muted-foreground">
                              Untagged
                            </span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          aria-label={`Review call ${index + 1}`}
                          onClick={(event) => {
                            event.stopPropagation()
                            setSelectedID(call.id)
                          }}
                        >
                          View transcript
                          <ArrowUpRightIcon className="size-4" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {visibleCalls.length === 0 && (
                <p className="p-8 text-center text-sm text-muted-foreground">
                  No calls match this filter.
                </p>
              )}
            </section>
          </>
        )}
      </main>
      <OperatorAnalyticsDetailSheet
        interactionID={selectedID}
        reviewControls={
          selectedID && data ? (
            <>
              {selectedCall && (
                <section aria-label="Patient status correction" className="mb-5 space-y-2">
                  <PatientStatusControl
                    original={selectedCall.patientGroup}
                    correction={patientStatuses[selectedCall.id]}
                    label="Patient status"
                    onChange={(value) =>
                      updatePatientStatus(
                        selectedCall.id,
                        selectedCall.patientGroup,
                        value,
                      )
                    }
                  />
                  <p role="status" className="text-xs text-muted-foreground">
                    {patientStatusMessage || "Saves a local review correction."}
                  </p>
                  {patientStatusError && (
                    <p role="alert" className="text-sm text-destructive">
                      {patientStatusError}
                    </p>
                  )}
                </section>
              )}
              <CallFailureTags
                key={selectedID}
                callID={selectedID}
                state={tags}
                onChange={updateTag}
              />
              {tagError && (
                <p role="alert" className="mt-2 text-sm text-destructive">
                  {tagError}
                </p>
              )}
            </>
          ) : undefined
        }
        onClose={() => setSelectedID("")}
      />
    </div>
  )
}

function Metric({
  label,
  value,
  caption,
}: {
  label: string
  value: string
  caption: string
}) {
  return (
    <div className="space-y-2 p-5 sm:p-6">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-4xl font-semibold tracking-tight tabular-nums">
        {value}
      </dd>
      <p className="text-xs text-muted-foreground">{caption}</p>
    </div>
  )
}
