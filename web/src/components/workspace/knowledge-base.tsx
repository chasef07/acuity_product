"use client"

import { useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react"
import { ArrowLeftIcon, HistoryIcon, PlusIcon, SearchIcon } from "lucide-react"

import { Alert, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import type { KnowledgeRevisionHistoryEntry, KnowledgeSection, LocationKnowledge } from "@/lib/api/generated/types.gen"
import {
  publishLocationKnowledge,
  useLocationKnowledge,
  useLocationKnowledgeHistory,
  useLocationKnowledgeRevision,
} from "@/lib/clients/location-knowledge"
import type { PortalFailure } from "@/lib/clients/portal-request"
import { type PortalQuery, readFailure } from "@/lib/data/queries"
import { formatTimestamp } from "@/lib/format-time"
import { fold, matches, publishedSections, publishFailureMessage } from "@/lib/knowledge-publication"
import { draftScope } from "@/lib/drafts"
import {
  knowledgeDrafts,
  type KnowledgeDraft as Draft,
  type KnowledgeDraftEntry as Entry,
} from "@/lib/knowledge-drafts"
import {
  AgentPage,
  AgentQueryBody,
  DisclosureRow,
  type AgentLocationProps,
} from "./manage-agent-page"

function abitaPromptKnowledge(entries: Entry[]) {
  return entries.map(({ title, text }) => `### ${title}\n\n${text.trim()}`.trim()).join("\n\n")
}

const maxKnowledgeCharacters = 20_000

function knowledgeCharacters(entries: Entry[]) {
  return entries.reduce((total, { title, text }) => total + [...title].length + [...text].length, 0)
}

function formatCount(count: number) {
  return count.toLocaleString("en-US")
}

function changed(draft: Draft) {
  const sections = publishedSections(draft.entries)
  return (
    sections.length !== draft.base.sections.length ||
    sections.some((section, index) => {
      const base = draft.base.sections[index]
      return section.id !== base.id || section.title !== base.title || section.text !== base.text
    })
  )
}

function changeCount(draft: Draft) {
  const sections = publishedSections(draft.entries)
  const base = new Map(draft.base.sections.map((section) => [section.id, section]))
  const kept = new Set(sections.map((section) => section.id))
  const addedOrEdited = sections.filter((section) => {
    const previous = base.get(section.id)
    return !previous || previous.title !== section.title || previous.text !== section.text
  })
  return addedOrEdited.length + draft.base.sections.filter((section) => !kept.has(section.id)).length
}

function saveDraft(key: string, next: Draft) {
  const idle: Draft = { ...next, publish: { status: "idle" } }
  knowledgeDrafts.write(key, changed(idle) || idle.reason.trim() ? idle : undefined)
}

function rejectedEntry(draft: Draft) {
  if (draft.publish.status !== "failed" || draft.publish.failure.kind !== "rejected") return undefined
  const { entryId } = draft.publish.failure
  const sections = publishedSections(draft.entries)
  return draft.entries.find((_, index) => sections[index].id === entryId)
}

export function KnowledgeBase({ actorSubject, ...props }: AgentLocationProps & { actorSubject: string }) {
  const knowledge = useLocationKnowledge(props)
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined
  const [search, setSearch] = useState("")
  const [historyOpen, setHistoryOpen] = useState(false)
  const words = fold(search).split(/\s+/).filter(Boolean)
  const draftKey = draftScope(actorSubject, props.practiceID, props.locationID)
  const draft = useSyncExternalStore(knowledgeDrafts.subscribe, () => knowledgeDrafts.read(draftKey), () => undefined)

  return (
    <AgentPage
      title="Knowledge base"
      detail={
        revision && (
          <>
            Updated <time dateTime={revision.createdAt}>{formatTimestamp(revision.createdAt)}</time> · Version{" "}
            {revision.version}
          </>
        )
      }
      actions={
        revision && (
          <Button variant="outline" size="sm" onClick={() => setHistoryOpen(true)}>
            <HistoryIcon data-icon="inline-start" />
            History
          </Button>
        )
      }
      controls={
        props.locationID ? (
          <>
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
            {draft && (
              <PublishBar
                draftKey={draftKey}
                draft={draft}
                practiceID={props.practiceID}
                locationID={props.locationID}
                onDiscard={knowledge.retry}
              />
            )}
          </>
        ) : null
      }
    >
      <AgentQueryBody
        query={knowledge}
        subject="knowledge base"
        unavailable="Knowledge isn't available right now."
        onRetry={knowledge.retry}
        missingLocation={!props.locationID}
        isEmpty={(data) => data.sections.length === 0}
        emptyTitle="No knowledge has been imported for this office yet."
        emptyDescription="Abita answers office questions from this knowledge once it is imported."
      >
        {(data) => (
          <KnowledgeEditor
            key={`${draftKey}:${data.revision?.id ?? ""}`}
            draftKey={draftKey}
            stored={draft}
            loaded={data}
            search={search}
            words={words}
            onReload={knowledge.retry}
          />
        )}
      </AgentQueryBody>
      <Sheet open={historyOpen} onOpenChange={setHistoryOpen}>
        <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>History</SheetTitle>
            <SheetDescription>
              Every published version of this office&apos;s knowledge. Restoring publishes an earlier version as a new one.
            </SheetDescription>
          </SheetHeader>
          <KnowledgeHistory practiceID={props.practiceID} locationID={props.locationID} onRestored={knowledge.retry} />
        </SheetContent>
      </Sheet>
    </AgentPage>
  )
}

function KnowledgeEditor({
  draftKey,
  stored,
  loaded,
  search,
  words,
  onReload,
}: {
  draftKey: string
  stored: Draft | undefined
  loaded: LocationKnowledge
  search: string
  words: string[]
  onReload: () => void
}) {
  const fresh = useMemo<Draft>(
    () => ({
      base: loaded,
      entries: loaded.sections.map((section) => ({ ...section, key: section.id })),
      reason: "",
      publish: { status: "idle" },
    }),
    [loaded],
  )
  const draft = stored ?? fresh
  const [editing, setEditing] = useState("")
  const [revealed, setRevealed] = useState({ key: "", count: 0 })
  const wasPublishing = useRef(draft.publish.status === "publishing")

  useEffect(() => {
    if (wasPublishing.current && !stored) onReload()
    wasPublishing.current = stored?.publish.status === "publishing"
  }, [stored, onReload])

  function updateEntry(key: string, change: Partial<KnowledgeSection>) {
    saveDraft(draftKey, { ...draft, entries: draft.entries.map((entry) => (entry.key === key ? { ...entry, ...change } : entry)) })
  }

  function add() {
    const key = crypto.randomUUID()
    setEditing(key)
    saveDraft(draftKey, { ...draft, entries: [...draft.entries, { key, id: "", title: "", text: "" }] })
  }

  const failure = draft.publish.status === "failed" ? draft.publish.failure : undefined
  const failedEntry = rejectedEntry(draft)
  const [seenFailure, setSeenFailure] = useState(failure)
  if (failure !== seenFailure) {
    setSeenFailure(failure)
    if (failedEntry) {
      setEditing(failedEntry.key)
      setRevealed((current) => ({ key: failedEntry.key, count: current.count + 1 }))
    }
  }
  const shown = draft.entries.filter((entry) => entry.key === editing || matches(entry, words))
  const publishing = draft.publish.status === "publishing"
  const characters = knowledgeCharacters(draft.entries)

  return (
    <fieldset disabled={publishing} className="flex min-w-0 flex-col gap-4">
      <p aria-live="polite" className="text-sm text-muted-foreground empty:hidden">
        {words.length > 0
          ? shown.length > 0
            ? `${shown.length} of ${draft.entries.length} entries`
            : `No entries match “${search.trim()}”.`
          : null}
      </p>
      {shown.length > 0 && (
        <ul key={words.join(" ")} className="divide-y overflow-hidden rounded-lg border bg-card">
          {shown.map((entry) => (
            <DisclosureRow
              key={entry.key === revealed.key ? `${entry.key}:${revealed.count}` : entry.key}
              title={entry.title || "Untitled entry"}
              defaultOpen={words.length > 0 || !entry.id || entry.key === revealed.key || entry === failedEntry}
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
                    onClick={() => saveDraft(draftKey, { ...draft, entries: draft.entries.filter((item) => item.key !== entry.key) })}
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
      <div className="overflow-hidden rounded-lg border bg-card">
        <DisclosureRow
          group
          defaultOpen={false}
          title="What Abita sees"
          detail={
            <span className={characters > maxKnowledgeCharacters ? "text-destructive" : undefined}>
              {formatCount(characters)} of {formatCount(maxKnowledgeCharacters)} characters
            </span>
          }
        >
          <p className="px-3 pb-3 pl-9 text-sm whitespace-pre-wrap">{abitaPromptKnowledge(draft.entries)}</p>
        </DisclosureRow>
      </div>
    </fieldset>
  )
}

function PublishBar({
  draftKey,
  draft,
  practiceID,
  locationID,
  onDiscard,
}: {
  draftKey: string
  draft: Draft
  practiceID: string
  locationID: string
  onDiscard: () => void
}) {
  const reasonID = useId()
  const count = changeCount(draft)
  const publishing = draft.publish.status === "publishing"
  const failure = draft.publish.status === "failed" ? draft.publish.failure : undefined
  const failedEntry = rejectedEntry(draft)
  const characters = knowledgeCharacters(draft.entries)
  const oversized = characters > maxKnowledgeCharacters

  async function submit() {
    const publication = {
      practiceId: practiceID,
      locationId: locationID,
      expectedRevisionId: draft.base.revision?.id,
      reason: draft.reason.trim(),
      sections: publishedSections(draft.entries),
    }
    const payload = JSON.stringify(publication)
    const attempt = draft.attempt?.payload === payload ? draft.attempt : { payload, revisionID: crypto.randomUUID() }
    const sent: Draft = { ...draft, publish: { status: "publishing" }, attempt }
    knowledgeDrafts.write(draftKey, sent)
    const outcome = await publishLocationKnowledge({ ...publication, revisionId: attempt.revisionID })
    const current = knowledgeDrafts.read(draftKey) === sent
    if (!outcome.ok) {
      if (current) knowledgeDrafts.write(draftKey, { ...sent, publish: { status: "failed", failure: outcome.failure } })
      return
    }
    if (current) knowledgeDrafts.write(draftKey, undefined)
  }

  function discard() {
    knowledgeDrafts.write(draftKey, undefined)
    onDiscard()
  }

  return (
    <fieldset
      disabled={publishing}
      className="flex min-w-0 flex-1 basis-xl flex-wrap items-center gap-2 rounded-lg border border-primary bg-primary/5 p-2"
    >
      {count > 0 && (
        <p className="shrink-0 text-sm font-medium">
          {count} unpublished {count === 1 ? "change" : "changes"}
        </p>
      )}
      <label htmlFor={reasonID} className="sr-only">
        Why are you changing this?
      </label>
      <Input
        id={reasonID}
        className="min-w-40 flex-1 bg-background"
        placeholder="Why are you changing this?"
        value={draft.reason}
        maxLength={2000}
        onChange={(event) => saveDraft(draftKey, { ...draft, reason: event.target.value })}
      />
      <Button variant="ghost" onClick={discard}>
        Discard changes
      </Button>
      <Button disabled={!changed(draft) || !draft.reason.trim() || oversized} onClick={() => void submit()}>
        {publishing && <Spinner data-icon="inline-start" />}
        {publishing ? "Publishing…" : "Publish"}
      </Button>
      {oversized && (
        <Alert variant="destructive" className="basis-full">
          <AlertTitle>
            This office&apos;s knowledge has {formatCount(characters)} characters of titles and text; keep it to{" "}
            {formatCount(maxKnowledgeCharacters)}.
          </AlertTitle>
        </Alert>
      )}
      {failure && (
        <Alert variant="destructive" className="basis-full">
          <AlertTitle>
            {failedEntry ? `Not published. Fix “${failedEntry.title || "Untitled entry"}” below.` : publishFailureMessage(failure)}
          </AlertTitle>
        </Alert>
      )}
    </fieldset>
  )
}

export function EntryFields({ entry, onChange }: { entry: Pick<KnowledgeSection, "title" | "text">; onChange: (change: Partial<KnowledgeSection>) => void }) {
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

export function KnowledgeHistory({
  practiceID,
  locationID,
  onRestored,
}: {
  practiceID: string
  locationID: string
  onRestored: () => void
}) {
  const history = useLocationKnowledgeHistory({ practiceID, locationID })
  const [selectedID, setSelectedID] = useState("")

  function restored() {
    setSelectedID("")
    history.retry()
    onRestored()
  }

  return (
    <div className="flex flex-col gap-4 p-5 text-sm">
      <AgentQueryBody
        query={history}
        subject="knowledge history"
        unavailable="Knowledge history isn't available right now."
        onRetry={history.retry}
        isEmpty={(data) => data.revisions.length === 0}
        emptyTitle="No versions yet."
        emptyDescription="Each publish adds a version here."
      >
        {(data) => {
          const selected = data.revisions.find((row) => row.id === selectedID)
          const active = data.revisions.find((row) => row.active)
          if (selected) {
            return (
              <RevisionDetail
                key={selected.id}
                practiceID={practiceID}
                locationID={locationID}
                row={selected}
                previousVersion={data.revisions.find((row) => row.id === selected.previousRevisionId)?.version}
                activeRevisionID={active?.id}
                onBack={() => setSelectedID("")}
                onRestored={restored}
              />
            )
          }
          return (
            <ul className="divide-y overflow-hidden rounded-lg border bg-card">
              {data.revisions.map((row) => (
                <li key={row.id}>
                  <button
                    type="button"
                    className="flex w-full flex-col gap-1 px-3 py-2 text-left hover:bg-muted/50"
                    onClick={() => setSelectedID(row.id)}
                  >
                    <RevisionSummary row={row} />
                  </button>
                </li>
              ))}
            </ul>
          )
        }}
      </AgentQueryBody>
    </div>
  )
}

function RevisionSummary({ row }: { row: KnowledgeRevisionHistoryEntry }) {
  return (
    <>
      <span className="flex items-center gap-2 font-medium">
        Version {row.version}
        {row.active && <Badge variant="secondary">Active</Badge>}
      </span>
      <span className="text-muted-foreground">
        {row.author} · <time dateTime={row.createdAt}>{formatTimestamp(row.createdAt)}</time>
      </span>
      <span>{row.reason}</span>
      <span className="text-xs text-muted-foreground">
        {row.lastAgentSearchAt
          ? `Last used by the agent: ${formatTimestamp(row.lastAgentSearchAt)} · ${row.agentSearchCount} ${row.agentSearchCount === 1 ? "call" : "calls"}`
          : "Not used by the agent yet"}
      </span>
    </>
  )
}

function RevisionDetail({
  practiceID,
  locationID,
  row,
  previousVersion,
  activeRevisionID,
  onBack,
  onRestored,
}: {
  practiceID: string
  locationID: string
  row: KnowledgeRevisionHistoryEntry
  previousVersion: number | undefined
  activeRevisionID: string | undefined
  onBack: () => void
  onRestored: () => void
}) {
  const scope = { practiceID, locationID }
  const revision = useLocationKnowledgeRevision({ ...scope, revisionID: row.id })
  const previous = useLocationKnowledgeRevision({ ...scope, revisionID: row.previousRevisionId })
  const failure = readFailure(revision) ?? readFailure(previous)
  const compared: PortalQuery<{ before: KnowledgeSection[]; after: KnowledgeSection[] }> =
    failure
      ? { status: "failed", failure }
      : revision.status === "ready" && (!row.previousRevisionId || previous.status === "ready")
        ? { status: "ready", refreshing: false, data: { after: revision.data.sections, before: previous.status === "ready" ? previous.data.sections : [] } }
        : { status: "loading" }

  return (
    <div className="flex flex-col gap-4">
      <Button variant="ghost" size="sm" className="w-fit" onClick={onBack}>
        <ArrowLeftIcon data-icon="inline-start" />
        Back to history
      </Button>
      <div className="flex flex-col gap-1">
        <RevisionSummary row={row} />
      </div>
      <AgentQueryBody
        query={compared}
        subject="knowledge version"
        unavailable="This version isn't available right now."
        onRetry={() => {
          void revision.retry()
          void previous.retry()
        }}
        isEmpty={() => false}
        emptyTitle=""
        emptyDescription=""
      >
        {({ before, after }) => (
          <>
            <p className="text-muted-foreground">
              {row.previousRevisionId ? `Compared with version ${previousVersion ?? "before it"}` : "First version"}
            </p>
            <SectionDiff before={before} after={after} />
            {!row.active && activeRevisionID && (
              <RestoreRevision
                practiceID={practiceID}
                locationID={locationID}
                version={row.version}
                sections={after}
                activeRevisionID={activeRevisionID}
                onRestored={onRestored}
              />
            )}
          </>
        )}
      </AgentQueryBody>
    </div>
  )
}

function SectionDiff({ before, after }: { before: KnowledgeSection[]; after: KnowledgeSection[] }) {
  const old = new Map(before.map((section) => [section.id, section]))
  const next = new Set(after.map((section) => section.id))
  const added = after.filter((section) => !old.has(section.id))
  const removed = before.filter((section) => !next.has(section.id))
  const changed = after.flatMap((section) => {
    const previous = old.get(section.id)
    return previous && (previous.title !== section.title || previous.text !== section.text) ? [{ previous, section }] : []
  })
  if (added.length + removed.length + changed.length === 0) return <p>No entries changed.</p>
  return (
    <div className="flex flex-col gap-4">
      {added.length > 0 && (
        <DiffGroup title="Added">
          {added.map((section) => (
            <DiffEntry key={section.id} title={section.title} text={section.text} />
          ))}
        </DiffGroup>
      )}
      {removed.length > 0 && (
        <DiffGroup title="Removed">
          {removed.map((section) => (
            <DiffEntry key={section.id} title={section.title} text={section.text} />
          ))}
        </DiffGroup>
      )}
      {changed.length > 0 && (
        <DiffGroup title="Changed">
          {changed.map(({ previous, section }) => (
            <li key={section.id} className="flex flex-col gap-2 px-3 py-2">
              <p className="font-medium">{section.title}</p>
              <p className="text-xs text-muted-foreground">Before</p>
              {previous.title !== section.title && <p className="line-through">{previous.title}</p>}
              <p className="whitespace-pre-wrap line-through decoration-muted-foreground">{previous.text}</p>
              <p className="text-xs text-muted-foreground">After</p>
              <p className="whitespace-pre-wrap">{section.text}</p>
            </li>
          ))}
        </DiffGroup>
      )}
    </div>
  )
}

function DiffGroup({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="font-medium">{title}</h3>
      <ul className="divide-y rounded-lg border bg-card">{children}</ul>
    </section>
  )
}

function DiffEntry({ title, text }: { title: string; text: string }) {
  return (
    <li className="flex flex-col gap-1 px-3 py-2">
      <p className="font-medium">{title}</p>
      <p className="whitespace-pre-wrap">{text}</p>
    </li>
  )
}

function RestoreRevision({
  practiceID,
  locationID,
  version,
  sections,
  activeRevisionID,
  onRestored,
}: {
  practiceID: string
  locationID: string
  version: number
  sections: KnowledgeSection[]
  activeRevisionID: string
  onRestored: () => void
}) {
  const [prompting, setPrompting] = useState(false)
  const [reason, setReason] = useState("")
  const [restoring, setRestoring] = useState(false)
  const [failure, setFailure] = useState<PortalFailure>()
  const attempt = useRef<{ payload: string; revisionID: string }>(undefined)
  const reasonID = useId()

  if (!prompting) {
    return (
      <Button variant="outline" className="w-fit" onClick={() => setPrompting(true)}>
        Restore this version
      </Button>
    )
  }

  async function restore() {
    const publication = { practiceId: practiceID, locationId: locationID, expectedRevisionId: activeRevisionID, reason: reason.trim(), sections }
    const payload = JSON.stringify(publication)
    if (attempt.current?.payload !== payload) attempt.current = { payload, revisionID: crypto.randomUUID() }
    setRestoring(true)
    setFailure(undefined)
    const outcome = await publishLocationKnowledge({ ...publication, revisionId: attempt.current.revisionID })
    setRestoring(false)
    if (outcome.ok) onRestored()
    else setFailure(outcome.failure)
  }

  return (
    <fieldset disabled={restoring} className="flex flex-col gap-3 rounded-lg border bg-card p-3">
      <p>Restoring publishes version {version}&apos;s entries as a new version. History is kept.</p>
      <label htmlFor={reasonID}>Why restore this version?</label>
      <Input id={reasonID} value={reason} maxLength={2000} onChange={(event) => setReason(event.target.value)} />
      {failure && (
        <Alert variant="destructive">
          <AlertTitle>{publishFailureMessage(failure)}</AlertTitle>
        </Alert>
      )}
      <Button className="w-fit" disabled={!reason.trim()} onClick={() => void restore()}>
        {restoring && <Spinner data-icon="inline-start" />}
        {restoring ? "Restoring…" : "Restore"}
      </Button>
    </fieldset>
  )
}
