"use client"

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react"
import { queryTasks } from "@/lib/api/generated/sdk.gen"
import type { Task } from "@/lib/api/generated/types.gen"
import { portalClient } from "@/lib/api/client"
import { getAccessToken } from "@/lib/auth-client"
import { appendUniqueByID } from "@/lib/workspace-ordering"
import { SidebarMenuItem } from "@/components/ui/sidebar"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { WorkspaceWindowFailure } from "./workspace-window-failure"

// Each open review folder owns its page independently of the My Tasks filter.
// The parent keys this by actor, practice, location, search, and review kind.
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
  kind: "texts" | "calls" | "appointments"
  revision: number
  count: number
  renderTask: (task: Task) => ReactNode
}) {
  const [page, setPage] = useState<{
    items: Task[]
    nextCursor: string
    loading: boolean
    error: string
  }>({ items: [], nextCursor: "", loading: true, error: "" })
  const depth = useRef(0)
  const request = useRef<AbortController | null>(null)
  const load = useCallback(
    async (cursor = "") => {
      request.current?.abort()
      const controller = new AbortController()
      request.current = controller
      const { signal } = controller
      try {
        const token = await getAccessToken()
        if (signal.aborted) return
        setPage((current) => ({ ...current, loading: true, error: "" }))
        if (!token) throw new Error("Sign in again to load this folder.")
        const items: Task[] = []
        let nextCursor = cursor
        do {
          const result = await queryTasks({
            client: portalClient(token),
            signal,
            body: {
              practiceId: practiceID,
              ...(locationID ? { locationId: locationID } : {}),
              ...(search ? { search } : {}),
              kind,
              state: "OPEN",
              ordering: "recent",
              responsibility: "all",
              grouped: false,
              includeCounts: false,
              limit: 50,
              ...(nextCursor ? { cursor: nextCursor } : {}),
            },
          })
          if (signal.aborted) return
          if (!result.data)
            throw new Error("This folder could not be loaded. Try again.")
          items.push(...result.data.items)
          nextCursor = result.data.nextCursor
        } while (!cursor && nextCursor && items.length < depth.current)
        setPage((current) => {
          const combined = cursor
            ? appendUniqueByID(current.items, items)
            : items
          depth.current = combined.length
          return { items: combined, nextCursor, loading: false, error: "" }
        })
      } catch (error) {
        if (!signal.aborted)
          setPage({
            items: [],
            nextCursor: "",
            loading: false,
            error:
              error instanceof Error
                ? error.message
                : "This folder could not be loaded.",
          })
      }
    },
    [practiceID, locationID, search, kind],
  )

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 0)
    return () => {
      window.clearTimeout(timer)
      request.current?.abort()
    }
  }, [load, revision, count])

  return (
    <>
      {page.items.map(renderTask)}
      {page.loading && page.items.length === 0 && (
        <SidebarMenuItem className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
          <Spinner />
          Loading…
        </SidebarMenuItem>
      )}
      {!page.loading && !page.error && page.items.length === 0 && (
        <SidebarMenuItem className="px-3 py-2 text-xs text-muted-foreground">
          {kind === "texts"
            ? "No texts needing review in the past five days."
            : "Nothing to review."}
        </SidebarMenuItem>
      )}
      {page.error && (
        <WorkspaceWindowFailure
          message={page.error}
          onRetry={() => void load()}
        />
      )}
      {page.nextCursor && (
        <SidebarMenuItem>
          <Button
            variant="ghost"
            size="sm"
            className="w-full"
            disabled={page.loading}
            onClick={() => void load(page.nextCursor)}
          >
            {page.loading ? <Spinner /> : "Show more"}
          </Button>
        </SidebarMenuItem>
      )}
    </>
  )
}
