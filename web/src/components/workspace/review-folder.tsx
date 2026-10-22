"use client"

import type { ReactNode } from "react"
import type { Task } from "@/lib/api/generated/types.gen"
import { type ReviewFolderKind, useReviewFolder } from "@/lib/clients/tasks"
import { readFailure } from "@/lib/data/queries"
import { SidebarMenuItem } from "@/components/ui/sidebar"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { WorkspaceWindowFailure } from "./workspace-window-failure"

export function ReviewFolder({
  practiceID,
  locationID,
  search,
  kind,
  renderTask,
}: {
  practiceID: string
  locationID: string
  search: string
  kind: ReviewFolderKind
  renderTask: (task: Task) => ReactNode
}) {
  const page = useReviewFolder({ practiceID, locationID, search, kind })
  const { items, nextCursor } = page.status === "ready" ? page.data : { items: [], nextCursor: "" }
  const failure = readFailure(page) ?? page.moreFailure
  const error = failure
    ? failure.kind === "signedOut" || failure.kind === "unauthenticated"
      ? "Sign in again to load this folder."
      : "This folder could not be loaded. Try again."
    : ""

  return (
    <>
      {items.map(renderTask)}
      {page.status === "loading" && (
        <SidebarMenuItem className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
          <Spinner />
          Loading…
        </SidebarMenuItem>
      )}
      {page.status === "ready" && !error && items.length === 0 && (
        <SidebarMenuItem className="px-3 py-2 text-xs text-muted-foreground">
          {kind === "texts"
            ? "No texts needing review in the past five days."
            : "Nothing to review."}
        </SidebarMenuItem>
      )}
      {error && (
        <SidebarMenuItem>
          <WorkspaceWindowFailure message={error} onRetry={() => void page.retry()} />
        </SidebarMenuItem>
      )}
      {nextCursor && (
        <SidebarMenuItem>
          <Button
            variant="ghost"
            size="sm"
            className="w-full"
            disabled={page.loadingMore}
            onClick={() => void page.loadMore()}
          >
            {page.loadingMore ? <Spinner /> : "Show more"}
          </Button>
        </SidebarMenuItem>
      )}
    </>
  )
}
