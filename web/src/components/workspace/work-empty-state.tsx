import { InboxIcon, MousePointerClickIcon } from "lucide-react"

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import type { WorkspaceProjectionState } from "@/lib/workspace-projection"

export function WorkEmptyState({ projection }: { projection: WorkspaceProjectionState }) {
  const inboxClear = projection.patients.counts.all === 0 && !projection.patients.loading && !projection.patients.error
  return (
    <section aria-label="No patient selected" className="flex min-h-0 flex-1">
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            {inboxClear ? <InboxIcon aria-hidden="true" /> : <MousePointerClickIcon aria-hidden="true" />}
          </EmptyMedia>
          <EmptyTitle>{inboxClear ? "Inbox clear" : "Select a patient"}</EmptyTitle>
          <EmptyDescription>
            {inboxClear
              ? "No patients are waiting in this view. New ones appear in the sidebar."
              : "Choose a patient from the sidebar to open their conversation."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </section>
  )
}
