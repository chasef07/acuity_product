"use client"

import { useId, useRef, useState } from "react"
import {
  ArrowRightLeftIcon,
  BookOpenIcon,
  CalendarCheckIcon,
  ShieldCheckIcon,
  UserSearchIcon,
  XIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { useAgentCalls } from "@/lib/clients/agent-calls"
import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { formatTimestamp } from "@/lib/format-time"
import { formatUSPhone } from "@/lib/phone"
import type { ManageAgentPage } from "@/lib/workspace-projection"
import { AgentCallPanel } from "./agent-call-panel"
import { AgentHomeMap, agentAreas, type AgentArea } from "./agent-home-map"
import { AgentQueryBody, ChooseOffice, type AgentLocationProps } from "./manage-agent-page"

const tools = [
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
  const detailsID = useId()
  const details = useRef<HTMLDivElement>(null)
  const [selected, setSelected] = useState<AgentArea>()
  const area = agentAreas.find((item) => item.id === selected)

  function selectArea(next: AgentArea) {
    setSelected(next)
    requestAnimationFrame(() => details.current?.scrollIntoView({ block: "nearest" }))
  }

  function closeDetails() {
    setSelected(undefined)
    document.getElementById(`${detailsID}-${selected}`)?.focus()
  }

  return (
    <section aria-labelledby={titleID} className="@container min-h-0 flex-1 overflow-auto">
      <header className="px-4 pt-8 sm:px-6">
        <h1 id={titleID} className="text-2xl font-semibold tracking-tight">Your agent</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          The knowledge, insurance guidance, and tools behind every call.
        </p>
      </header>
      <div className="px-4 py-10 sm:px-6 @[36rem]:py-16">
        <AgentHomeMap selected={selected} detailsID={detailsID} disabled={!locationID} onSelect={selectArea} />
      </div>
      {!locationID && <ChooseOffice />}
      <div id={detailsID} ref={details}>
        {area && locationID && (
          <section aria-label="Agent details" className="border-t px-4 py-6 sm:px-6">
            <header className="mb-5 flex items-start justify-between gap-4">
              <div>
                <h2 className="text-lg font-semibold tracking-tight">{area.title}</h2>
                {selected === "knowledge" && <KnowledgeVersion practiceID={practiceID} locationID={locationID} />}
              </div>
              <Button variant="ghost" size="sm" onClick={closeDetails}>
                <XIcon aria-hidden="true" />
                Close details
              </Button>
            </header>
            {selected === "knowledge" ? (
              <>
                <Button variant="outline" className="mb-8" onClick={() => onPage("knowledge")}>Open knowledge base</Button>
                <NeedsKnowledge practiceID={practiceID} locationID={locationID} onAddKnowledge={() => onPage("knowledge")} />
              </>
            ) : selected === "insurance" ? (
              <>
                <p className="mb-4 text-sm text-muted-foreground">Review accepted plans, referrals, and requirements for medical and routine vision coverage.</p>
                <Button variant="outline" onClick={() => onPage("insurance")}>Open insurance list</Button>
              </>
            ) : <AgentTools />}
          </section>
        )}
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
                {formatTimestamp(revision.createdAt)}
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
  const moreError = current.moreFailure
    ? "More calls could not be loaded. Try again."
    : undefined

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
                    {formatTimestamp(call.startedAt)}
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
      {moreError && (
        <Alert variant="destructive" className="mt-4">
          <AlertTitle>More calls unavailable</AlertTitle>
          <AlertDescription>{moreError}</AlertDescription>
        </Alert>
      )}
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
        navigationError={moreError}
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

function AgentTools() {
  return (
    <ul aria-label="Agent tools" className="border-t">
      {tools.map((tool) => (
        <li key={tool.label} className="flex items-center gap-3 border-b py-3">
          <tool.icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-medium">{tool.label}</span>
            {tool.detail && (
              <span className="block text-xs text-muted-foreground">{tool.detail}</span>
            )}
          </span>
        </li>
      ))}
    </ul>
  )
}
