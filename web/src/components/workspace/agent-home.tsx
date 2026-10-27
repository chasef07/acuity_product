"use client"

import { useId, useRef, useState, useSyncExternalStore } from "react"
import {
  ArrowRightLeftIcon,
  BookOpenIcon,
  CalendarCheckIcon,
  ShieldCheckIcon,
  UserSearchIcon,
  XIcon,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { formatTimestamp } from "@/lib/format-time"
import { homeDay, homeGreeting } from "@/lib/home-greeting"
import type { ManageAgentPage } from "@/lib/workspace-projection"
import { AgentHomeMap, agentAreas, type AgentArea } from "./agent-home-map"
import type { AgentLocationProps } from "./manage-agent-page"

const tools = [
  { icon: BookOpenIcon, label: "Read office knowledge" },
  { icon: UserSearchIcon, label: "Find an existing patient" },
  { icon: UserSearchIcon, label: "Register a new patient" },
  { icon: ShieldCheckIcon, label: "Check insurance acceptance" },
  { icon: ShieldCheckIcon, label: "Update insurance coverage" },
  { icon: CalendarCheckIcon, label: "Find available appointments", optional: true },
  { icon: CalendarCheckIcon, label: "Book an appointment", optional: true },
  { icon: CalendarCheckIcon, label: "Reschedule an appointment", optional: true },
  { icon: CalendarCheckIcon, label: "Cancel an appointment", optional: true },
  { icon: BookOpenIcon, label: "Save a staff follow-up task", optional: true },
  { icon: ArrowRightLeftIcon, label: "Transfer a call", optional: true },
  { icon: XIcon, label: "End a call", optional: true },
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
  const day = useSyncExternalStore(subscribeHomeDay, currentHomeDay, serverHomeDay)
  const greeting = day ? homeGreeting(day) : undefined

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
        <h1 id={titleID} className="tracking-tight">
          <span className="block font-serif text-4xl font-medium sm:text-5xl">
            {greeting?.greeting ?? "Welcome"},
          </span>
          <time
            className="mt-2 block font-sans text-sm font-medium tracking-wide text-muted-foreground sm:text-base"
            dateTime={day || undefined}
          >
            {greeting?.dateLabel}
          </time>
        </h1>
      </header>
      <div className="px-4 py-10 sm:px-6 @[36rem]:py-16">
        <AgentHomeMap selected={selected} detailsID={detailsID} onSelect={selectArea} />
      </div>
      <div id={detailsID} ref={details}>
        {area && (
          <section aria-label="Agent details" className="border-t px-4 py-6 sm:px-6">
            <header className="mb-5 flex items-start justify-between gap-4">
              <div>
                <h2 className="text-lg font-semibold tracking-tight">{area.title}</h2>
                {selected === "knowledge" && locationID && <KnowledgeVersion practiceID={practiceID} locationID={locationID} />}
              </div>
              <Button variant="ghost" size="sm" onClick={closeDetails}>
                <XIcon aria-hidden="true" />
                Close details
              </Button>
            </header>
            {selected === "knowledge" ? (
              <Button variant="outline" onClick={() => onPage("knowledge")}>Open knowledge base</Button>
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

function currentHomeDay() {
  return homeDay(new Date())
}

function serverHomeDay() {
  return ""
}

function subscribeHomeDay(onChange: () => void) {
  const timer = window.setInterval(onChange, 60_000)
  window.addEventListener("focus", onChange)
  return () => {
    window.clearInterval(timer)
    window.removeEventListener("focus", onChange)
  }
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

function AgentTools() {
  return (
    <>
      <p className="mb-3 text-sm text-muted-foreground">Some tools depend on this office&apos;s setup.</p>
      <ul aria-label="Agent tools" className="border-t">
        {tools.map((tool) => (
          <li key={tool.label} className="flex min-h-11 items-center gap-3 border-b py-2">
            <tool.icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 whitespace-nowrap text-sm font-medium">{tool.label}</span>
            {tool.optional && <span className="text-xs text-muted-foreground">Optional</span>}
          </li>
        ))}
      </ul>
    </>
  )
}
