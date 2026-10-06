"use client"

import { useLocationKnowledge } from "@/lib/clients/location-knowledge"
import {
  AgentPage,
  AgentQueryBody,
  DisclosureRow,
  LocationSelect,
  type AgentLocationProps,
} from "./manage-agent-page"

export function KnowledgeBase(props: AgentLocationProps) {
  const knowledge = useLocationKnowledge(props)
  const revision = knowledge.status === "ready" ? knowledge.data.revision : undefined

  return (
    <AgentPage
      title="Knowledge base"
      controls={
        <>
          <LocationSelect {...props} />
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
        {(data) => (
          <ul className="divide-y overflow-hidden rounded-lg border bg-card">
            {data.sections.map((section) => (
              <DisclosureRow key={section.id} title={section.title}>
                <p className="px-3 pb-3 pl-9 text-sm whitespace-pre-wrap">{section.text}</p>
              </DisclosureRow>
            ))}
          </ul>
        )}
      </AgentQueryBody>
    </AgentPage>
  )
}
