"use client"

import { useId, useState } from "react"
import {
  ArrowRightLeftIcon,
  BookOpenIcon,
  CalendarCheckIcon,
  ShieldCheckIcon,
  UserSearchIcon,
} from "lucide-react"

import { AnimatedAcuityMark } from "@/components/animated-acuity-mark"
import { Button } from "@/components/ui/button"
import { useAgentCalls } from "@/lib/clients/agent-calls"
import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { formatUSPhone } from "@/lib/phone"
import type { ManageAgentPage } from "@/lib/workspace-projection"
import { AgentCallPanel } from "./agent-call-panel"
import { AgentQueryBody, ChooseOffice, type AgentLocationProps } from "./manage-agent-page"

const capabilities = [
  { icon: BookOpenIcon, label: "Answer office questions", detail: "From the knowledge base" },
  { icon: UserSearchIcon, label: "Find the caller's patient record" },
  { icon: CalendarCheckIcon, label: "Book, reschedule, and cancel appointments" },
  { icon: ShieldCheckIcon, label: "Check insurance" },
  { icon: ArrowRightLeftIcon, label: "Transfer calls to staff" },
]

export function AgentHome({
  practiceID,
  locationID,
  onPage,
}: AgentLocationProps & { onPage: (page: ManageAgentPage) => void }) {
  const titleID = useId()
  return (
    <section aria-labelledby={titleID} className="min-h-0 flex-1 overflow-auto">
      <header className="flex flex-wrap items-center gap-4 px-4 pt-8 sm:px-6">
        <AnimatedAcuityMark className="size-14 shrink-0" />
        <div className="min-w-0 flex-1">
          <h1 id={titleID} className="text-2xl font-semibold tracking-tight">
            Acuity
          </h1>
          <KnowledgeVersion practiceID={practiceID} locationID={locationID} />
        </div>
      </header>
      <div className="grid gap-10 px-4 py-8 sm:px-6 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        {locationID ? (
          <NeedsKnowledge practiceID={practiceID} locationID={locationID} onAddKnowledge={() => onPage("knowledge")} />
        ) : (
          <ChooseOffice />
        )}
        <Capabilities />
      </div>
    </section>
  )
}

function KnowledgeVersion({ practiceID, locationID }: { practiceID: string; locationID: string }) {
  const knowledge = useLocationKnowledge({ practiceID, locationID })
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined
  if (!locationID) return null
  return (
    <p className="mt-1 text-sm text-muted-foreground">
      {knowledge.status === "loading"
        ? "Loading knowledge version…"
        : knowledge.status === "failed"
          ? <>
              Knowledge version unavailable.{" "}
              <Button variant="link" size="sm" className="h-auto p-0" onClick={knowledge.retry}>
                Try again
              </Button>
            </>
          : revision
            ? <>
                Version {revision.version} · published{" "}
                {new Date(revision.createdAt).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}
              </>
            : "No knowledge published yet"}
    </p>
  )
}

function NeedsKnowledge({
  practiceID,
  locationID,
  onAddKnowledge,
}: {
  practiceID: string
  locationID: string
  onAddKnowledge: () => void
}) {
  const titleID = useId()
  const [revision, setRevision] = useState(0)
  const [selectedID, setSelectedID] = useState("")
  const current = useAgentCalls({
    practiceID,
    locationID,
    range: "7d",
    phone: "",
    flaggedOnly: false,
    needsKnowledgeOnly: true,
    revision,
  })
  const calls = current.status === "ready" ? current.data.calls : []
  const nextCursor = current.status === "ready" ? current.data.nextCursor : ""
  const selectedIndex = calls.findIndex((call) => call.id === selectedID)

  async function loadNext(from: string) {
    const next = await current.loadMore()
    if (next?.calls.length) setSelectedID((previous) => (previous === from ? next.calls[0].id : previous))
  }

  return (
    <section aria-labelledby={titleID} className="min-w-0">
      <div className="flex items-baseline gap-2">
        <h2 id={titleID} className="text-lg font-semibold tracking-tight">
          Needs knowledge
        </h2>
        {current.status === "ready" && (
          <span className="font-mono text-sm text-muted-foreground">
            {calls.length}
            {nextCursor ? "+" : ""}
          </span>
        )}
      </div>
      <p className="mt-1 mb-4 text-sm text-muted-foreground">
        Calls this week where a caller asked an office question the knowledge base couldn&apos;t answer.
      </p>
      <AgentQueryBody
        query={current}
        subject="calls"
        unavailable="Calls aren't available right now."
        onRetry={() => setRevision((value) => value + 1)}
        isEmpty={(data) => data.calls.length === 0}
        emptyTitle="Nothing needs knowledge"
        emptyDescription="No unanswered office questions in the last 7 days."
      >
        {(data) => (
          <ul className="border-t">
            {data.calls.map((call) => (
              <li key={call.id} className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b py-3">
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-medium">
                    {call.knowledgeGaps.length
                      ? call.knowledgeGaps.map((gap) => `“${gap}”`).join(" · ")
                      : "An office question went unanswered"}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {new Date(call.startedAt).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}
                    {" · "}
                    {formatUSPhone(call.phone)}
                  </p>
                </div>
                <div className="flex gap-2">
                  <Button variant="ghost" size="sm" onClick={() => setSelectedID(call.id)}>
                    Open call
                  </Button>
                  <Button variant="outline" size="sm" onClick={onAddKnowledge}>
                    Add to knowledge
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </AgentQueryBody>
      {nextCursor && (
        <Button variant="outline" className="mt-4" disabled={current.loadingMore} onClick={() => void current.loadMore()}>
          {current.loadingMore ? "Loading…" : "Load more calls"}
        </Button>
      )}
      <AgentCallPanel
        id={selectedID}
        position={selectedIndex + 1}
        loadedCount={calls.length}
        hasMore={Boolean(nextCursor)}
        loadingNext={current.loadingMore}
        onPrevious={selectedIndex > 0 ? () => setSelectedID(calls[selectedIndex - 1].id) : undefined}
        onNext={
          selectedIndex >= 0 && (selectedIndex < calls.length - 1 || nextCursor)
            ? () => {
                if (selectedIndex < calls.length - 1) setSelectedID(calls[selectedIndex + 1].id)
                else void loadNext(selectedID)
              }
            : undefined
        }
        onClose={() => setSelectedID("")}
        onFlagged={(id) =>
          current.update((shown) => ({
            ...shown,
            calls: shown.calls.map((call) => (call.id === id ? { ...call, issueFlagged: true } : call)),
          }))
        }
      />
    </section>
  )
}

function Capabilities() {
  const titleID = useId()
  return (
    <section aria-labelledby={titleID} className="min-w-0">
      <h2 id={titleID} className="mb-4 text-lg font-semibold tracking-tight">
        What Acuity can do
      </h2>
      <ul className="border-t">
        {capabilities.map((capability) => (
          <li key={capability.label} className="flex items-center gap-3 border-b py-3">
            <capability.icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{capability.label}</span>
              {capability.detail && (
                <span className="block text-xs text-muted-foreground">{capability.detail}</span>
              )}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}
