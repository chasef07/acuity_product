"use client"

import { useId, useRef, useState } from "react"
import { PlusIcon, SearchIcon } from "lucide-react"

import { Alert, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { KnowledgeSection, LocationKnowledge } from "@/lib/api/generated/types.gen"
import { publishLocationKnowledge, useLocationKnowledge } from "@/lib/clients/location-knowledge"
import type { PortalFailure } from "@/lib/clients/portal-request"
import {
  readKnowledgeDraft,
  writeKnowledgeDraft,
  type KnowledgeDraft as Draft,
  type KnowledgeDraftEntry as Entry,
} from "@/lib/knowledge-drafts"
import {
  AgentPage,
  AgentQueryBody,
  DisclosureRow,
  LocationSelect,
  type AgentLocationProps,
} from "./manage-agent-page"

function fold(text: string) {
  return text.normalize("NFD").replace(/\p{Diacritic}/gu, "").toLowerCase()
}

function matches(entry: Entry, words: string[]) {
  const haystack = fold(`${entry.title}\n${entry.text}`)
  return words.every((word) => haystack.includes(word))
}

function publishedSections(entries: Entry[]): KnowledgeSection[] {
  const used = new Set(entries.map((entry) => entry.id).filter(Boolean))
  return entries.map(({ id, title, text }) => {
    if (id) return { id, title, text }
    const slug = fold(title).replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 90) || "entry"
    let candidate = slug
    for (let suffix = 2; used.has(candidate); suffix++) candidate = `${slug}-${suffix}`
    used.add(candidate)
    return { id: candidate, title, text }
  })
}

function changed(draft: Draft) {
  return JSON.stringify(publishedSections(draft.entries)) !== JSON.stringify(draft.base.sections)
}

export function KnowledgeBase(props: AgentLocationProps) {
  const knowledge = useLocationKnowledge(props)
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined
  const [search, setSearch] = useState("")
  const words = fold(search).split(/\s+/).filter(Boolean)

  return (
    <AgentPage
      title="Knowledge base"
      controls={
        <>
          <LocationSelect {...props} />
          <InputGroup className="w-full sm:w-72">
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
            <InputGroupInput
              type="search"
              aria-label="Search knowledge"
              autoComplete="off"
              placeholder="Search knowledge"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </InputGroup>
          {revision && (
            <p className="ml-auto text-sm text-muted-foreground">
              Updated{" "}
              <time dateTime={revision.createdAt}>
                {new Date(revision.createdAt).toLocaleString(undefined, {
                  month: "short",
                  day: "numeric",
                  year: "numeric",
                  hour: "numeric",
                  minute: "2-digit",
                })}
              </time>
            </p>
          )}
        </>
      }
    >
      <AgentQueryBody
        query={knowledge}
        subject="knowledge base"
        unavailable="Knowledge isn't available right now."
        onRetry={knowledge.retry}
        missingLocation={!props.locationID}
        isEmpty={(data) => data.sections.length === 0}
        emptyTitle="No knowledge has been imported for this Location yet."
        emptyDescription="Abita answers office questions from this knowledge once it is imported."
      >
        {(data) => (
          <KnowledgeEditor
            key={`${props.practiceID}:${props.locationID}`}
            practiceID={props.practiceID}
            locationID={props.locationID}
            loaded={data}
            search={search}
            words={words}
            onReload={knowledge.retry}
          />
        )}
      </AgentQueryBody>
    </AgentPage>
  )
}

type PublishState = { status: "idle" } | { status: "publishing" } | { status: "failed"; failure: PortalFailure }

function KnowledgeEditor({
  practiceID,
  locationID,
  loaded,
  search,
  words,
  onReload,
}: {
  practiceID: string
  locationID: string
  loaded: LocationKnowledge
  search: string
  words: string[]
  onReload: () => void
}) {
  const draftKey = `${practiceID}:${locationID}`
  const [draft, setDraft] = useState<Draft>(
    () =>
      readKnowledgeDraft(draftKey) ?? {
        base: loaded,
        entries: loaded.sections.map((section) => ({ ...section, key: section.id })),
        reason: "",
      },
  )
  const [editing, setEditing] = useState("")
  const [publish, setPublish] = useState<PublishState>({ status: "idle" })
  const [revealed, setRevealed] = useState({ key: "", count: 0 })
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)
  const reasonID = useId()

  function update(next: Draft) {
    writeKnowledgeDraft(draftKey, changed(next) || next.reason.trim() ? next : undefined)
    setDraft(next)
    setPublish({ status: "idle" })
  }

  function updateEntry(key: string, change: Partial<KnowledgeSection>) {
    update({ ...draft, entries: draft.entries.map((entry) => (entry.key === key ? { ...entry, ...change } : entry)) })
  }

  function add() {
    const key = crypto.randomUUID()
    setEditing(key)
    update({ ...draft, entries: [...draft.entries, { key, id: "", title: "", text: "" }] })
  }

  const unpublished = changed(draft)
  const sections = publishedSections(draft.entries)
  const failure = publish.status === "failed" ? publish.failure : undefined
  const entryFailure = failure?.kind === "rejected" ? failure.entryId : undefined
  const failedEntry = draft.entries.find((_, index) => entryFailure !== undefined && sections[index].id === entryFailure)
  const shown = draft.entries.filter((entry) => entry.key === editing || matches(entry, words))
  const publishing = publish.status === "publishing"
  const failureBesideEntry = failedEntry !== undefined && shown.includes(failedEntry)

  async function submit() {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: draft.base.revision?.id,
      reason: draft.reason.trim(),
      sections,
    }
    const payload = JSON.stringify(publication)
    if (attempt.current?.payload !== payload) attempt.current = { payload, revisionID: crypto.randomUUID() }
    setPublish({ status: "publishing" })
    const outcome = await publishLocationKnowledge({ ...publication, revisionId: attempt.current.revisionID })
    if (!outcome.ok) {
      setPublish({ status: "failed", failure: outcome.failure })
      if (outcome.failure.entryId) {
        const index = sections.findIndex((section) => section.id === outcome.failure.entryId)
        if (index >= 0) {
          const key = draft.entries[index].key
          setEditing(key)
          setRevealed((current) => ({ key, count: current.count + 1 }))
        }
      }
      return
    }
    writeKnowledgeDraft(draftKey, undefined)
    onReload()
  }

  function discard() {
    writeKnowledgeDraft(draftKey, undefined)
    onReload()
  }

  return (
    <fieldset disabled={publishing} className="flex min-w-0 flex-col gap-4">
      <p aria-live="polite" className="text-sm text-muted-foreground empty:hidden">
        {words.length > 0
          ? shown.length > 0
            ? `${shown.length} of ${draft.entries.length} entries`
            : `No entries match “${search.trim()}”.`
          : null}
      </p>
      {(unpublished || draft.reason) && (
        <div className="flex flex-col gap-3 rounded-lg border bg-card p-3">
          <p className="text-sm font-medium">Unpublished changes</p>
          <label htmlFor={reasonID} className="text-sm">
            Why are you changing this?
          </label>
          <Input
            id={reasonID}
            value={draft.reason}
            maxLength={2000}
            onChange={(event) => update({ ...draft, reason: event.target.value })}
          />
          {failure && !failureBesideEntry && (
            <Alert variant="destructive">
              <AlertTitle>{publishFailureMessage(failure)}</AlertTitle>
            </Alert>
          )}
          <div className="flex flex-wrap gap-2">
            <Button
              disabled={!unpublished || !draft.reason.trim()}
              onClick={() => void submit()}
            >
              {publishing && <Spinner data-icon="inline-start" />}
              {publishing ? "Publishing…" : "Publish"}
            </Button>
            <Button variant="ghost" onClick={discard}>
              Discard changes
            </Button>
          </div>
        </div>
      )}
      {shown.length > 0 && (
        <ul key={words.join(" ")} className="divide-y overflow-hidden rounded-lg border bg-card">
          {shown.map((entry) => (
            <DisclosureRow
              key={entry.key === revealed.key ? `${entry.key}:${revealed.count}` : entry.key}
              title={entry.title || "Untitled entry"}
              defaultOpen={words.length > 0 || !entry.id || entry.key === revealed.key}
            >
              <div className="flex flex-col gap-3 px-3 pb-3 pl-9">
                {entry === failedEntry && failure && (
                  <p role="alert" className="text-sm text-destructive">
                    {failure.message}
                  </p>
                )}
                {entry.key === editing ? (
                  <EntryFields entry={entry} onChange={(change) => updateEntry(entry.key, change)} />
                ) : (
                  <p className="text-sm whitespace-pre-wrap">{entry.text}</p>
                )}
                <div className="flex gap-2">
                  {entry.key === editing ? (
                    <Button size="sm" variant="outline" onClick={() => setEditing("")}>
                      Done
                    </Button>
                  ) : (
                    <Button size="sm" variant="outline" onClick={() => setEditing(entry.key)}>
                      Edit
                    </Button>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => update({ ...draft, entries: draft.entries.filter((item) => item.key !== entry.key) })}
                  >
                    Remove
                  </Button>
                </div>
              </div>
            </DisclosureRow>
          ))}
        </ul>
      )}
      <Button variant="outline" className="w-fit" onClick={add}>
        <PlusIcon data-icon="inline-start" />
        Add entry
      </Button>
    </fieldset>
  )
}

function EntryFields({ entry, onChange }: { entry: Entry; onChange: (change: Partial<KnowledgeSection>) => void }) {
  const titleID = useId()
  const textID = useId()
  return (
    <>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={titleID} className="text-sm font-medium">
          Title
        </label>
        <Input id={titleID} value={entry.title} maxLength={200} onChange={(event) => onChange({ title: event.target.value })} />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor={textID} className="text-sm font-medium">
          Text
        </label>
        <Textarea id={textID} value={entry.text} onChange={(event) => onChange({ text: event.target.value })} />
      </div>
    </>
  )
}

function publishFailureMessage(failure: PortalFailure) {
  if (failure.kind === "signedOut" || failure.kind === "unauthenticated") return "Sign in again to publish."
  if (failure.kind === "unauthorized") return "You don't have access to change this Location's knowledge."
  return failure.message ?? "Not published. Nothing changed. Try again."
}
