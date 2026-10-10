"use client"

import { useState } from "react"
import { SearchIcon } from "lucide-react"

import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import type { LocationKnowledge } from "@/lib/api/generated/types.gen"
import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
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

function matching(sections: LocationKnowledge["sections"], words: string[]) {
  return sections.filter((section) => {
    const haystack = fold(`${section.title}\n${section.text}`)
    return words.every((word) => haystack.includes(word))
  })
}

export function KnowledgeBase(props: AgentLocationProps) {
  const knowledge = useLocationKnowledge(props)
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined
  const [search, setSearch] = useState("")
  const words = fold(search).split(/\s+/).filter(Boolean)
  const all = knowledge.status === "ready" ? knowledge.data.sections : []
  const sections = words.length ? matching(all, words) : all

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
          <p aria-live="polite" className="text-sm text-muted-foreground">
            {words.length > 0 && all.length > 0
              ? sections.length > 0
                ? `${sections.length} of ${all.length} entries`
                : `No entries match “${search.trim()}”.`
              : null}
          </p>
          {revision && (
            <p className="ml-auto text-sm text-muted-foreground">
              Updated{" "}
              <time dateTime={revision.createdAt}>
                {new Date(revision.createdAt).toLocaleDateString(undefined, {
                  month: "short",
                  day: "numeric",
                  year: "numeric",
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
        {() =>
          sections.length > 0 && (
            <ul key={words.join(" ")} className="divide-y overflow-hidden rounded-lg border bg-card">
              {sections.map((section) => (
                <DisclosureRow key={section.id} title={section.title} defaultOpen={words.length > 0}>
                  <p className="px-3 pb-3 pl-9 text-sm whitespace-pre-wrap">{section.text}</p>
                </DisclosureRow>
              ))}
            </ul>
          )
        }
      </AgentQueryBody>
    </AgentPage>
  )
}
