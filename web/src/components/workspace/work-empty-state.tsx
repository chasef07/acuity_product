import { InboxIcon, MousePointerClickIcon } from "lucide-react"

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import { workspaceFolders } from "@/components/workspace/workspace-rail-folders"
import type { WorkspaceProjectionState } from "@/lib/workspace-projection"

export function WorkEmptyState({ projection }: { projection: WorkspaceProjectionState }) {
  const openCount = workspaceFolders(projection).reduce((total, folder) => total + folder.count, 0)
  const inboxClear = openCount === 0 && !projection.tasks.loading && !projection.tasks.error
  return (
    <section aria-label="No task selected" className="flex min-h-0 flex-1">
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            {inboxClear ? <InboxIcon aria-hidden="true" /> : <MousePointerClickIcon aria-hidden="true" />}
          </EmptyMedia>
          <EmptyTitle>{inboxClear ? "Inbox clear" : "Select a task"}</EmptyTitle>
          <EmptyDescription>
            {inboxClear
              ? "No open tasks, missed calls, or texts in this view. New ones appear in the sidebar."
              : "Choose a task, missed call, or text from the sidebar to open its conversation."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    </section>
  )
}
