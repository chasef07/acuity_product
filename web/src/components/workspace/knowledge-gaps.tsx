"use client"

import { useId, useRef, useState } from "react"
import { ChevronRightIcon, MessageCircleQuestionIcon, SearchIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { AgentCall, KnowledgeGapResolution, KnowledgeSection, LocationKnowledge } from "@/lib/api/generated/types.gen"
import { closeKnowledgeGap, useAgentCalls } from "@/lib/clients/agent-calls"
import { publishLocationKnowledge, useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { formatTimestamp } from "@/lib/format-time"
import { fold, matches, publishedSections, publishFailureMessage } from "@/lib/knowledge-publication"
import { formatClock } from "@/lib/vocabulary"
import { AgentCallPanel } from "./agent-call-panel"
import { EntryFields } from "./knowledge-base"

export function KnowledgeGaps({ practiceID, locationID }: { practiceID: string; locationID: string }) {
  const gaps = useAgentCalls({ practiceID, locationID, range: "24h", phone: "", flaggedOnly: false, knowledgeGapOnly: true })
  const [open, setOpen] = useState<AgentCall>()
  const [published, setPublished] = useState<string[]>([])
  const [saving, setSaving] = useState(false)
  const calls = gaps.status === "ready" ? gaps.data.calls : []
  const more = gaps.status === "ready" && Boolean(gaps.data.nextCursor)

  function closed(id: string) {
    setOpen((current) => (current?.id === id ? undefined : current))
    setPublished((ids) => ids.filter((item) => item !== id))
    gaps.update((shown) => ({ ...shown, calls: shown.calls.filter((call) => call.id !== id) }))
  }

  if (gaps.status === "loading" || (gaps.status === "ready" && calls.length === 0 && !open)) return null
  if (gaps.status === "failed") {
    return (
      <Alert variant="destructive" className="mx-4 mb-8 w-auto sm:mx-6">
        <AlertTitle>Calls that need knowledge are unavailable</AlertTitle>
        <Button variant="outline" size="sm" className="mt-2 w-fit" onClick={gaps.retry}>
          Try again
        </Button>
      </Alert>
    )
  }
  return (
    <section aria-label="Calls that need knowledge" className="mx-4 mb-8 rounded-xl border p-4 sm:mx-6">
      <h2 className="flex items-center gap-2 text-base font-medium">
        <MessageCircleQuestionIcon aria-hidden="true" className="size-4 text-muted-foreground" />
        {`${calls.length}${more ? "+" : ""} ${calls.length === 1 && !more ? "call needs" : "calls need"} knowledge`}
      </h2>
      <p className="mt-0.5 text-sm text-muted-foreground">Missing from the knowledge base · Last 24 hours</p>
      {calls.length > 0 && (
        <ul className="mt-3 border-t">
          {calls.map((call) => (
            <li key={call.id} className="border-b last:border-b-0">
              <button
                type="button"
                className="flex min-h-11 w-full items-center gap-3 py-2 text-left text-sm hover:bg-muted/50"
                onClick={() => setOpen(call)}
              >
                <span className="min-w-0 flex-1">{formatTimestamp(call.startedAt)}</span>
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
        footerBusy={saving}
        footer={
          open && (
            <KnowledgeFix
              key={open.id}
              practiceID={practiceID}
              locationID={locationID}
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

type Mode = "add" | "edit"

function KnowledgeFix({
  practiceID,
  locationID,
  call,
  published,
  onPublished,
  onSavingChange,
  onClosed,
}: {
  practiceID: string
  locationID: string
  call: AgentCall
  published: boolean
  onPublished: (id: string) => void
  onSavingChange: (saving: boolean) => void
  onClosed: (id: string) => void
}) {
  const knowledge = useLocationKnowledge({ practiceID, locationID })
  const [mode, setMode] = useState<Mode>("add")
  const [draft, setDraft] = useState<KnowledgeSection>({ id: "", title: "", text: "" })
  const [search, setSearch] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)
  const errorID = useId()
  const base = knowledge.status === "ready" ? knowledge.data : undefined
  const editing = mode === "edit" && draft.id !== ""
  const picked = editing ? base?.sections.find((section) => section.id === draft.id) : undefined
  const removed = editing && base !== undefined && !picked
  const unchanged = picked !== undefined && picked.title === draft.title.trim() && picked.text === draft.text.trim()
  const ready = draft.title.trim() !== "" && draft.text.trim() !== "" && (mode === "add" || editing) && !unchanged

  function busy(value: boolean) {
    setSaving(value)
    onSavingChange(value)
  }

  function switchMode(next: Mode) {
    setMode(next)
    setDraft({ id: "", title: "", text: "" })
    setError("")
  }

  async function close(resolution: KnowledgeGapResolution) {
    const outcome = await closeKnowledgeGap(call.id, resolution)
    if (!outcome.ok && outcome.failure.kind !== "conflict" && outcome.failure.kind !== "rejected") {
      setError(resolution === "ANSWERED" ? "Saved to knowledge, but this call couldn't be closed. Try again." : "This call couldn't be closed. Try again.")
      return
    }
    onClosed(call.id)
  }

  async function saveAndClose() {
    if (saving || (!published && (!base || !ready))) return
    busy(true)
    setError("")
    if (!published && base) {
      const outcome = await publish(base)
      if (!outcome.ok) {
        if (outcome.failure.kind === "conflict") {
          setError("Someone else changed this office's knowledge. Check the topic and save again.")
          await knowledge.retry()
        } else {
          setError(publishFailureMessage(outcome.failure))
        }
        busy(false)
        return
      }
      onPublished(call.id)
      void knowledge.retry()
    }
    await close("ANSWERED")
    busy(false)
  }

  function publish(current: LocationKnowledge) {
    const entry = { id: draft.id, title: draft.title.trim(), text: draft.text.trim() }
    const replaces = editing && current.sections.some((section) => section.id === entry.id)
    const sections = replaces ? current.sections.map((section) => (section.id === entry.id ? entry : section)) : [...current.sections, entry]
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: current.revision?.id,
      reason: `${replaces ? `Updated “${entry.title}”` : `Added “${entry.title}”`} from a caller question on ${new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(call.startedAt))}`,
      sections: publishedSections(sections),
    }
    const payload = JSON.stringify(publication)
    if (attempt.current?.payload !== payload) attempt.current = { payload, revisionID: crypto.randomUUID() }
    return publishLocationKnowledge({ ...publication, revisionId: attempt.current.revisionID })
  }

  async function notNeeded() {
    if (saving) return
    busy(true)
    setError("")
    await close("NOT_NEEDED")
    busy(false)
  }

  const words = fold(search).split(/\s+/).filter(Boolean)
  const found = (base?.sections ?? []).filter((section) => matches(section, words))

  return (
    <form
      aria-label="Fix this"
      aria-describedby={error ? errorID : undefined}
      className="flex flex-col gap-3"
      onSubmit={(event) => {
        event.preventDefault()
        void saveAndClose()
      }}
    >
      <h3 className="text-sm font-medium">Fix this</h3>
      {published ? (
        <p className="text-sm text-muted-foreground">Saved to knowledge.</p>
      ) : knowledge.status === "failed" ? (
        <Alert variant="destructive">
          <AlertTitle>Office knowledge couldn&apos;t load</AlertTitle>
          <Button type="button" variant="outline" size="sm" className="mt-2 w-fit" onClick={knowledge.retry}>
            Try again
          </Button>
        </Alert>
      ) : !base ? (
        <p role="status" className="text-sm text-muted-foreground">Loading office knowledge…</p>
      ) : (
        <>
          <ToggleGroup
            aria-label="How to fix this"
            variant="segmented"
            size="sm"
            spacing={0}
            value={[mode]}
            onValueChange={(values) => {
              const value = values[0]
              if (value === "add" || value === "edit") switchMode(value)
            }}
          >
            <ToggleGroupItem value="add" className="h-7 text-xs data-pressed:bg-background data-pressed:shadow-xs">
              Add a topic
            </ToggleGroupItem>
            <ToggleGroupItem value="edit" className="h-7 text-xs data-pressed:bg-background data-pressed:shadow-xs">
              Edit a topic
            </ToggleGroupItem>
          </ToggleGroup>
          {mode === "edit" && !editing && (
            <>
              <InputGroup>
                <InputGroupAddon>
                  <SearchIcon aria-hidden="true" />
                </InputGroupAddon>
                <InputGroupInput aria-label="Search topics" placeholder="Parking" value={search} onChange={(event) => setSearch(event.target.value)} />
              </InputGroup>
              <ul aria-label="Topics" className="max-h-48 overflow-y-auto rounded-md border">
                {found.map((section) => (
                  <li key={section.id} className="border-b last:border-b-0">
                    <button
                      type="button"
                      className="w-full px-3 py-2 text-left text-sm hover:bg-muted/50"
                      onClick={() => setDraft(section)}
                    >
                      {section.title}
                    </button>
                  </li>
                ))}
                {found.length === 0 && <li className="px-3 py-2 text-sm text-muted-foreground">No topics match.</li>}
              </ul>
            </>
          )}
          {(mode === "add" || editing) && (
            <>
              {editing && (
                <Button type="button" variant="link" size="sm" className="h-auto w-fit p-0" onClick={() => setDraft({ id: "", title: "", text: "" })}>
                  Pick a different topic
                </Button>
              )}
              {removed && <p className="text-sm text-muted-foreground">Someone else removed this topic. Saving adds it back.</p>}
              <EntryFields entry={draft} onChange={(change) => setDraft((current) => ({ ...current, ...change }))} />
            </>
          )}
        </>
      )}
      {error && (
        <Alert id={errorID} variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        {!published && (
          <Button type="button" variant="ghost" disabled={saving} onClick={() => void notNeeded()}>
            Not needed
          </Button>
        )}
        <Button type="submit" disabled={saving || (!published && !ready)}>
          {saving && <Spinner data-icon="inline-start" />}
          {published ? "Close call" : "Save and close"}
        </Button>
      </div>
    </form>
  )
}
