"use client"

import { useEffect, useState } from "react"
import { CheckIcon, FlagIcon, RefreshCwIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"
import { Spinner } from "@/components/ui/spinner"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import {
  AgentCallPanel,
  AppointmentActions,
  callDuration,
} from "./agent-call-panel"
import { InsuranceList } from "./insurance-list"
import { KnowledgeBase } from "./knowledge-base"
import { AgentPage, AgentQueryBody, OptionSelect } from "./manage-agent-page"
import type {
  AgentCallsQuery,
  InsuranceCoverage,
  Location,
} from "@/lib/api/generated/types.gen"
import { useAgentCalls } from "@/lib/clients/agent-calls"
import { formatUSPhone } from "@/lib/phone"
import type { ManageAgentPage } from "@/lib/workspace-projection"

const ranges: { value: AgentCallsQuery["range"]; label: string }[] = [
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
]

export function ManageAgent({
  practiceID,
  locationScopeID,
  locations,
  page,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
  page: ManageAgentPage
}) {
  const [locationID, setLocationID] = useState(() =>
    locations.some((location) => location.id === locationScopeID)
      ? locationScopeID
      : (locations[0]?.id ?? ""),
  )
  const [coverage, setCoverage] = useState<InsuranceCoverage>("medical")
  const location = { practiceID, locations, locationID, onLocation: setLocationID }
  return (
    <div className="flex min-h-0 flex-1 flex-col bg-canvas">
      {page === "knowledge" ? (
        <KnowledgeBase {...location} />
      ) : page === "insurance" ? (
        <InsuranceList {...location} coverage={coverage} onCoverage={setCoverage} />
      ) : (
        <AgentTranscripts
          practiceID={practiceID}
          locationScopeID={locationScopeID}
          locations={locations}
        />
      )}
    </div>
  )
}

function AgentTranscripts({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const [office, setOffice] = useState(locationScopeID || "all")
  const [range, setRange] = useState<AgentCallsQuery["range"]>("24h")
  const [phoneInput, setPhoneInput] = useState("")
  const [phone, setPhone] = useState("")
  const [flaggedOnly, setFlaggedOnly] = useState(false)
  const [version, setVersion] = useState(0)
  const [selectedID, setSelectedID] = useState("")
  const current = useAgentCalls({
    practiceID,
    locationID: office === "all" ? "" : office,
    range,
    phone,
    flaggedOnly,
    revision: version,
  })
  const calls = current.status === "ready" ? current.data.calls : []
  const nextCursor = current.status === "ready" ? current.data.nextCursor : ""
  const selectedIndex = calls.findIndex((call) => call.id === selectedID)
  const loadingMore = current.loadingMore
  const moreError = current.moreFailure
    ? "More calls could not be loaded. Try again."
    : undefined
  const offices = [
    { value: "all", label: "All offices" },
    ...locations.map((location) => ({
      value: location.id,
      label: location.name,
    })),
  ]

  useEffect(() => {
    const timeout = setTimeout(() => setPhone(phoneInput), 300)
    return () => clearTimeout(timeout)
  }, [phoneInput])

  async function loadMore(navigateFrom?: string) {
    const next = await current.loadMore()
    if (navigateFrom && next?.calls.length) {
      setSelectedID((previous) =>
        previous === navigateFrom ? next.calls[0].id : previous,
      )
    }
  }

  return (
    <AgentPage
      title="Transcripts"
      action={
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh calls"
          onClick={() => setVersion((value) => value + 1)}
        >
          <RefreshCwIcon />
        </Button>
      }
      controls={
        <>
          <Input
            aria-label="Search phone number"
            placeholder="Search phone number"
            value={phoneInput}
            onChange={(event) => setPhoneInput(event.target.value)}
            maxLength={32}
            className="w-full sm:w-56"
          />
          <OptionSelect label="Office" value={office} items={offices} onChange={setOffice} />
          <OptionSelect label="Date range" value={range} items={ranges} onChange={setRange} />
          <Button
            variant={flaggedOnly ? "secondary" : "ghost"}
            aria-pressed={flaggedOnly}
            onClick={() => setFlaggedOnly((value) => !value)}
          >
            <FlagIcon data-icon="inline-start" />
            Flagged calls
          </Button>
          {current.status === "ready" && (
            <p role="status" className="ml-auto text-sm tabular-nums text-muted-foreground">
              {calls.length}
              {nextCursor ? "+" : ""} {calls.length === 1 && !nextCursor ? "call" : "calls"}
            </p>
          )}
        </>
      }
    >
      <AgentQueryBody
        query={current}
        subject="calls"
        unavailable="Calls aren't available right now."
        onRetry={() => setVersion((value) => value + 1)}
        isEmpty={(data) => data.calls.length === 0}
        emptyTitle={flaggedOnly ? "No flagged calls" : "No calls found"}
        emptyDescription="Try another date range, office, or phone number."
      >
        {() => (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-40">Date / time</TableHead>
                  <TableHead className="w-56">Phone number</TableHead>
                  <TableHead>Appointment action</TableHead>
                  <TableHead className="w-24 text-right">Duration</TableHead>
                  <TableHead className="w-28 text-right">Transferred</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {calls.map((call) => (
                  <TableRow
                    key={call.id}
                    className="cursor-pointer"
                    onClick={() => setSelectedID(call.id)}
                  >
                    <TableCell className="py-2">
                      <Button
                        variant="ghost"
                        className="h-auto flex-col items-start gap-0 px-2 py-1 -ml-2 tabular-nums"
                        onClick={(event) => {
                          event.stopPropagation()
                          setSelectedID(call.id)
                        }}
                        aria-label={`Open call from ${formatUSPhone(call.phone)} at ${new Date(call.startedAt).toLocaleString()}`}
                      >
                        <span className="block">
                          {new Date(call.startedAt).toLocaleDateString(
                            undefined,
                            { month: "short", day: "numeric" },
                          )}
                        </span>
                        <span className="text-xs text-muted-foreground">
                          {new Date(call.startedAt).toLocaleTimeString(
                            undefined,
                            { hour: "numeric", minute: "2-digit" },
                          )}
                        </span>
                      </Button>
                    </TableCell>
                    <TableCell>
                      <span className="inline-flex items-center gap-2 tabular-nums">
                        {formatUSPhone(call.phone)}
                        {call.issueFlagged && (
                          <FlagIcon
                            className="size-3.5 text-muted-foreground"
                            aria-label="Issue flagged"
                          />
                        )}
                      </span>
                    </TableCell>
                    <TableCell>
                      <AppointmentActions call={call} />
                    </TableCell>
                    <TableCell className="text-right tabular-nums text-muted-foreground">
                      {callDuration(call.durationSeconds)}
                    </TableCell>
                    <TableCell className="text-right">
                      {call.transferred ? (
                        <CheckIcon
                          className="ml-auto size-4"
                          aria-label="Transferred: Yes"
                        />
                      ) : (
                        <span
                          aria-label="Transferred: No"
                          className="text-muted-foreground"
                        >
                          —
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
            {moreError && (
              <Alert variant="destructive" className="mt-4">
                <AlertTitle>More calls unavailable</AlertTitle>
                <AlertDescription>{moreError}</AlertDescription>
              </Alert>
            )}
            {nextCursor && (
              <div className="py-5 text-center">
                <Button
                  variant="outline"
                  disabled={loadingMore}
                  onClick={() => void loadMore()}
                >
                  {loadingMore && <Spinner data-icon="inline-start" />}
                  {loadingMore ? "Loading…" : "Load more calls"}
                </Button>
              </div>
            )}
          </>
        )}
      </AgentQueryBody>
      <AgentCallPanel
        id={selectedID}
        position={selectedIndex + 1}
        loadedCount={calls.length}
        hasMore={Boolean(nextCursor)}
        loadingNext={loadingMore}
        navigationError={moreError}
        onPrevious={
          selectedIndex > 0
            ? () => setSelectedID(calls[selectedIndex - 1].id)
            : undefined
        }
        onNext={
          selectedIndex >= 0 &&
          (selectedIndex < calls.length - 1 || nextCursor)
            ? () => {
                if (selectedIndex < calls.length - 1)
                  setSelectedID(calls[selectedIndex + 1].id)
                else void loadMore(selectedID)
              }
            : undefined
        }
        onClose={() => setSelectedID("")}
        onFlagged={(id) =>
          current.update((shown) => ({
            ...shown,
            calls: shown.calls.map((call) =>
              call.id === id ? { ...call, issueFlagged: true } : call,
            ),
          }))
        }
      />
    </AgentPage>
  )
}
