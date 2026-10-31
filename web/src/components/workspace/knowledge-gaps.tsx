"use client"

import { useId, useRef, useState } from "react"
import { MessageCircleQuestionIcon, SearchIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { KnowledgeGapResolution, KnowledgeSection, Location, LocationKnowledge } from "@/lib/api/generated/types.gen"
import { closeKnowledgeGap } from "@/lib/clients/agent-calls"
import { publishLocationKnowledge, useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { fold, matches, publishedSections, publishFailureMessage } from "@/lib/knowledge-publication"
import { callTime, GapCalls, type GapFixProps } from "./call-gaps"
import { EntryFields } from "./knowledge-base"

export function KnowledgeGaps(props: { practiceID: string; locationID: string; locations: Location[] }) {
  return <GapCalls gap="knowledge" icon={MessageCircleQuestionIcon} fix={KnowledgeFix} {...props} />
}

type Mode = "add" | "edit"

function KnowledgeFix({
  practiceID,
  locationID,
  officeName,
  call,
  published,
  onPublished,
  onSavingChange,
  onClosed,
}: GapFixProps) {
  const knowledge = useLocationKnowledge({ practiceID, locationID })
  const [mode, setMode] = useState<Mode>("add")
  const [draft, setDraft] = useState<KnowledgeSection>({ id: "", title: "", text: "" })
  const [search, setSearch] = useState("")
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)
  const errorID = useId()
  const base = knowledge.status === "ready" ? knowledge.data : undefined
  const stale = knowledge.status === "ready" && Boolean(knowledge.refreshFailure)
  const editing = mode === "edit" && draft.id !== ""
  const picked = editing ? base?.sections.find((section) => section.id === draft.id) : undefined
  const removed = editing && base !== undefined && !picked
  const unchanged = picked !== undefined && picked.title === draft.title.trim() && picked.text === draft.text.trim()
  const ready = draft.title.trim() !== "" && draft.text.trim() !== "" && (mode === "add" || editing) && !unchanged && !stale

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
      reason: `${replaces ? `Updated “${entry.title}”` : `Added “${entry.title}”`} from a caller question on ${callTime(call)}`,
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
      {officeName && <p className="-mt-2 text-sm text-muted-foreground">Saves to {officeName}&apos;s knowledge base</p>}
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
      ) : stale ? (
        <Alert variant="destructive">
          <AlertTitle>Office knowledge couldn&apos;t reload</AlertTitle>
          <Button type="button" variant="outline" size="sm" className="mt-2 w-fit" onClick={knowledge.retry}>
            Try again
          </Button>
        </Alert>
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
