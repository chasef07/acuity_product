"use client"

import { useState, type ComponentType } from "react"
import { ChevronRightIcon, type LucideIcon } from "lucide-react"

import { Alert, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import type { AgentCall, Location } from "@/lib/api/generated/types.gen"
import { useAgentCalls } from "@/lib/clients/agent-calls"
import { formatTimestamp } from "@/lib/format-time"
import { formatClock } from "@/lib/vocabulary"
import { AgentCallPanel } from "./agent-call-panel"

export type GapFixProps = {
  practiceID: string
  locationID: string
  officeName?: string
  call: AgentCall
  published: boolean
  onPublished: (id: string) => void
  onSavingChange: (saving: boolean) => void
  onClosed: (id: string) => void
}

const gaps = {
  knowledge: { missing: "Missing from the knowledge base", note: "Not in the knowledge base", line: (call: AgentCall) => call.knowledgeGapLine },
  insurance: { missing: "Missing from the insurance list", note: "Not on the insurance list", line: (call: AgentCall) => call.insuranceGapLine },
}

export function callTime(call: AgentCall) {
  return new Intl.DateTimeFormat(undefined, { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", timeZoneName: "short" }).format(new Date(call.startedAt))
}

export function GapCalls({
  gap,
  icon: Icon,
  fix: Fix,
  practiceID,
  locationID,
  locations,
}: {
  gap: keyof typeof gaps
  icon: LucideIcon
  fix: ComponentType<GapFixProps>
  practiceID: string
  locationID: string
  locations: Location[]
}) {
  const { missing, note, line } = gaps[gap]
  const query = useAgentCalls({
    practiceID,
    locationID,
    range: "24h",
    phone: "",
    flaggedOnly: false,
    knowledgeGapOnly: gap === "knowledge",
    insuranceGapOnly: gap === "insurance",
  })
  const [open, setOpen] = useState<AgentCall>()
  const [published, setPublished] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const calls = query.status === "ready" ? query.data.calls : []
  const more = query.status === "ready" && Boolean(query.data.nextCursor)
  const label = `Calls that need ${gap}`
  const openLine = open && line(open)

  function officeName(call: AgentCall) {
    return locations.find((location) => location.id === call.locationId)?.name ?? "Unknown office"
  }

  function closed(id: string) {
    setOpen((current) => (current?.id === id ? undefined : current))
    setPublished((ids) => ids.filter((item) => item !== id))
    query.update((shown) => ({ ...shown, calls: shown.calls.filter((call) => call.id !== id) }))
  }

  if (query.status === "loading" || (query.status === "ready" && calls.length === 0 && !open)) return null
  if (query.status === "failed") {
    return (
      <Alert variant="destructive" className="mx-4 mb-8 w-auto sm:mx-6">
        <AlertTitle>{label} are unavailable</AlertTitle>
        <Button variant="outline" size="sm" className="mt-2 w-fit" onClick={query.retry}>
          Try again
        </Button>
      </Alert>
    )
  }
  return (
    <section aria-label={label} className="mx-4 mb-8 rounded-xl border p-4 sm:mx-6">
      <h2 className="flex items-center gap-2 text-base font-medium">
        <Icon aria-hidden="true" className="size-4 text-muted-foreground" />
        {`${calls.length}${more ? "+" : ""} ${calls.length === 1 && !more ? "call needs" : "calls need"} ${gap}`}
      </h2>
      <p className="mt-0.5 text-sm text-muted-foreground">{missing} · Last 24 hours</p>
      {calls.length > 0 && (
        <ul className="mt-3 border-t">
          {calls.map((call) => (
            <li key={call.id} className="border-b last:border-b-0">
              <button
                type="button"
                className="flex min-h-11 w-full items-center gap-3 py-2 text-left text-sm hover:bg-muted/50"
                onClick={() => setOpen(call)}
              >
                <span className="min-w-0 flex-1">
                  {formatTimestamp(call.startedAt)}
                  {!locationID && <span className="text-muted-foreground"> · {officeName(call)}</span>}
                  {line(call) && <span className="block truncate text-muted-foreground">“{line(call)}”</span>}
                </span>
                <span className="text-muted-foreground">{formatClock(call.durationSeconds)}</span>
                <ChevronRightIcon aria-hidden="true" className="size-4 text-muted-foreground" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <AgentCallPanel
        id={open?.id ?? ""}
        position={0}
        loadedCount={calls.length}
        hasMore={false}
        loadingNext={false}
        onClose={() => setOpen(undefined)}
        onFlagged={() => {}}
        highlight={openLine ? { line: openLine, note } : undefined}
        footerBusy={saving}
        footer={
          open && (
            <Fix
              key={open.id}
              practiceID={practiceID}
              locationID={open.locationId}
              officeName={locationID ? undefined : officeName(open)}
              call={open}
              published={published.includes(open.id)}
              onPublished={(id) => setPublished((ids) => [...ids, id])}
              onSavingChange={setSaving}
              onClosed={closed}
            />
          )
        }
      />
    </section>
  )
}
