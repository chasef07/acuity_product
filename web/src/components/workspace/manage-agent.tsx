"use client"

import { useEffect, useState } from "react"
import { CheckIcon, FlagIcon, RefreshCwIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Alert, AlertTitle, AlertDescription } from "@/components/ui/alert"
import {
  Empty,
  EmptyHeader,
  EmptyTitle,
  EmptyDescription,
} from "@/components/ui/empty"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
import type { AgentCallsQuery, Location } from "@/lib/api/generated/types.gen"
import { useAgentCalls } from "@/lib/clients/agent-calls"
import { formatUSPhone } from "@/lib/phone"

const ranges = [
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
]
export function ManageAgent({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const [office, setOffice] = useState(locationScopeID || "all")
  const [range, setRange] = useState<AgentCallsQuery["range"]>("7d")
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
    <section
      aria-label="Manage agent"
      className="flex min-h-0 flex-1 flex-col bg-canvas"
    >
      <header className="flex shrink-0 items-center gap-3 px-4 pb-3 pt-6 sm:px-6">
        <h1 className="flex-1 text-2xl font-semibold tracking-tight">
          Manage agent
        </h1>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Refresh calls"
          onClick={() => setVersion((value) => value + 1)}
        >
          <RefreshCwIcon />
        </Button>
      </header>
      <div className="flex flex-wrap items-center gap-2 px-4 pb-5 pt-2 sm:px-6">
        <Input
          aria-label="Search phone number"
          placeholder="Search phone number"
          value={phoneInput}
          onChange={(event) => setPhoneInput(event.target.value)}
          maxLength={32}
          className="w-full sm:w-56"
        />
        <Select
          value={office}
          items={offices}
          onValueChange={(value) => value && setOffice(value)}
        >
          <SelectTrigger aria-label="Office">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {offices.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Select
          value={range}
          items={ranges}
          onValueChange={(value) =>
            value && setRange(value as AgentCallsQuery["range"])
          }
        >
          <SelectTrigger aria-label="Date range">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {ranges.map((item) => (
                <SelectItem key={item.value} value={item.value}>
                  {item.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        <Button
          variant={flaggedOnly ? "secondary" : "ghost"}
          aria-pressed={flaggedOnly}
          onClick={() => setFlaggedOnly((value) => !value)}
        >
          <FlagIcon data-icon="inline-start" />
          Flagged calls
        </Button>
      </div>
      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6 sm:px-6">
        {current.status === "loading" && (
          <div
            role="status"
            aria-label="Loading calls"
            className="flex flex-col gap-4 py-6"
          >
            <span className="sr-only">Loading calls…</span>
            {Array.from({ length: 5 }, (_, index) => (
              <Skeleton key={index} className="h-12 w-full" />
            ))}
          </div>
        )}
        {current.status === "failed" && (
          <Alert variant="destructive" className="my-6">
            <AlertTitle>Calls unavailable</AlertTitle>
            <AlertDescription>
              {current.failure.kind === "signedOut" ||
              current.failure.kind === "unauthenticated"
                ? "Sign in again to view calls."
                : "Calls could not be loaded. Try again."}
            </AlertDescription>
            <Button
              className="mt-3 w-fit"
              variant="outline"
              onClick={() => setVersion((value) => value + 1)}
            >
              Try again
            </Button>
          </Alert>
        )}
        {current.status === "ready" &&
          (calls.length ? (
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
          ) : (
            <Empty className="py-16">
              <EmptyHeader>
                <EmptyTitle>
                  {flaggedOnly ? "No flagged calls" : "No calls found"}
                </EmptyTitle>
                <EmptyDescription>
                  Try another date range, office, or phone number.
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ))}
      </div>
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
    </section>
  )
}
