"use client"

import { useRef, useState } from "react"
import { MessageCircleQuestionIcon, SearchIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { KnowledgeSection, Location, LocationKnowledge } from "@/lib/api/generated/types.gen"
import { publishLocationKnowledge, useLocationKnowledge } from "@/lib/clients/location-knowledge"
import { nextAttempt, type PublishAttempt } from "@/lib/drafts"
import { fold, matches, publishedSections } from "@/lib/knowledge-publication"
import { callTime, GapCalls, GapFixForm, type GapFixProps } from "./call-gaps"
import { EntryFields } from "./knowledge-base"
import { publishFailureMessage } from "./manage-agent-page"

export function KnowledgeGaps(props: { practiceID: string; locationID: string; locations: Location[] }) {
  return <GapCalls gap="knowledge" icon={MessageCircleQuestionIcon} fix={KnowledgeFix} {...props} />
}

type Mode = "add" | "edit"

function KnowledgeFix(props: GapFixProps) {
  const { practiceID, locationID, call } = props
  const knowledge = useLocationKnowledge({ practiceID, locationID })
  const [mode, setMode] = useState<Mode>("add")
  const [draft, setDraft] = useState<KnowledgeSection>({ id: "", title: "", text: "" })
  const [search, setSearch] = useState("")
  const attempt = useRef<PublishAttempt>(undefined)
  const base = knowledge.status === "ready" ? knowledge.data : undefined
  const stale = knowledge.status === "ready" && Boolean(knowledge.refreshFailure)
  const editing = mode === "edit" && draft.id !== ""
  const picked = editing ? base?.sections.find((section) => section.id === draft.id) : undefined
  const removed = editing && base !== undefined && !picked
  const unchanged = picked !== undefined && picked.title === draft.title.trim() && picked.text === draft.text.trim()
  const ready = draft.title.trim() !== "" && draft.text.trim() !== "" && (mode === "add" || editing) && !unchanged && !stale

  async function publish(current: LocationKnowledge) {
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
    attempt.current = nextAttempt(attempt.current, publication)
    const outcome = await publishLocationKnowledge({ ...publication, revisionId: attempt.current.revisionID })
    if (outcome.ok) return undefined
    if (outcome.failure.kind !== "conflict") return publishFailureMessage(outcome.failure)
    await knowledge.retry()
    return "Someone else changed this office's knowledge. Check the topic and save again."
  }

  const words = fold(search).split(/\s+/).filter(Boolean)
  const found = (base?.sections ?? []).filter((section) => matches(section, words))

  return (
    <GapFixForm gap="knowledge" fix={props} query={knowledge} publish={base && ready ? () => publish(base) : undefined}>
      {(clearError) => (
        <>
          <ToggleGroup
            aria-label="How to fix this"
            variant="segmented"
            size="sm"
            spacing={0}
            value={[mode]}
            onValueChange={(values) => {
              const value = values[0]
              if (value === "add" || value === "edit") {
                setMode(value)
                setDraft({ id: "", title: "", text: "" })
                clearError()
              }
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
    </GapFixForm>
  )
}
