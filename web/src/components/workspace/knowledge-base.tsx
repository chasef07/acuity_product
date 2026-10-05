"use client"

import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"

import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import type { KnowledgeSection, Location } from "@/lib/api/generated/types.gen"
import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
import {
  AgentPageFailure,
  AgentPageHeader,
  AgentPageLoading,
  LocationSelect,
  NoLocation,
  initialLocationID,
} from "./manage-agent-page"

export function KnowledgeBase({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const [locationID, setLocationID] = useState(() =>
    initialLocationID(locations, locationScopeID),
  )
  const knowledge = useLocationKnowledge({ practiceID, locationID })
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined

  return (
    <section aria-label="Knowledge base" className="flex min-h-0 flex-1 flex-col">
      <AgentPageHeader title="Knowledge base">
        {locationID && (
          <LocationSelect locations={locations} value={locationID} onChange={setLocationID} />
        )}
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
      </AgentPageHeader>
      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6 sm:px-6">
        {!locationID ? (
          <NoLocation />
        ) : knowledge.status === "loading" ? (
          <AgentPageLoading label="knowledge base" />
        ) : knowledge.status === "failed" ? (
          <AgentPageFailure
            failure={knowledge.failure}
            subject="knowledge base"
            unavailable="Knowledge isn't available right now."
            onRetry={knowledge.retry}
          />
        ) : knowledge.data.sections.length === 0 ? (
          <Empty className="py-16">
            <EmptyHeader>
              <EmptyTitle>No knowledge has been imported for this Location yet.</EmptyTitle>
              <EmptyDescription>Abita answers office questions from this knowledge once it is imported.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <ul className="divide-y overflow-hidden rounded-lg border bg-card">
            {knowledge.data.sections.map((section) => (
              <SectionRow key={section.id} section={section} />
            ))}
          </ul>
        )}
      </div>
    </section>
  )
}

function SectionRow({ section }: { section: KnowledgeSection }) {
  return (
    <Collapsible render={<li />}>
      <CollapsibleTrigger
        render={
          <button
            type="button"
            className="group/section flex min-h-10 w-full items-center gap-3 px-3 py-2 text-left text-sm font-medium outline-hidden transition-colors hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          />
        }
      >
        <ChevronRightIcon
          aria-hidden="true"
          className="size-3.5 shrink-0 text-muted-foreground transition-transform group-aria-expanded/section:rotate-90 motion-reduce:transition-none"
        />
        <span className="min-w-0 flex-1">{section.title}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <p className="px-3 pb-3 pl-9 text-sm whitespace-pre-wrap">{section.text}</p>
      </CollapsibleContent>
    </Collapsible>
  )
}
