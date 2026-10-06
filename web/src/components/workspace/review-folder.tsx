"use client"

import type { ReactNode } from "react"
import type { Task } from "@/lib/api/generated/types.gen"
import { type ReviewFolderKind, useReviewFolder } from "@/lib/clients/tasks"
import { SidebarMenuItem } from "@/components/ui/sidebar"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { WorkspaceWindowFailure } from "./workspace-window-failure"

export function ReviewFolder({
  practiceID,
  locationID,
  search,
  kind,
  revision,
  count,
  renderTask,
}: {
  practiceID: string
  locationID: string
  search: string
  kind: ReviewFolderKind
  revision: number
  count: number
  renderTask: (task: Task) => ReactNode
}) {
  const page = useReviewFolder({ practiceID, locationID, search, kind, revision, count })
  const error = page.failure
    ? page.failure.kind === "signedOut" || page.failure.kind === "unauthenticated"
      ? "Sign in again to load this folder."
      : "This folder could not be loaded. Try again."
    : ""

  return (
    <>
      {page.items.map(renderTask)}
      {page.loading && page.items.length === 0 && (
        <SidebarMenuItem className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
          <Spinner />
          Loading…
        </SidebarMenuItem>
      )}
      {!page.loading && !error && page.items.length === 0 && (
        <SidebarMenuItem className="px-3 py-2 text-xs text-muted-foreground">
          {kind === "texts"
            ? "No texts needing review in the past five days."
            : "Nothing to review."}
        </SidebarMenuItem>
      )}
      {error && (
        <SidebarMenuItem>
          <WorkspaceWindowFailure message={error} onRetry={page.retry} />
        </SidebarMenuItem>
      )}
      {page.nextCursor && (
        <SidebarMenuItem>
          <Button
            variant="ghost"
            size="sm"
            className="w-full"
            disabled={page.loading}
            onClick={page.showMore}
          >
            {page.loading ? <Spinner /> : "Show more"}
          </Button>
        </SidebarMenuItem>
      )}
    </>
  )
}
