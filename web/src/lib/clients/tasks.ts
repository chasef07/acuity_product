import { useCallback, useEffect, useRef, useState } from "react"

import {
  changeTaskCategory as changeTaskCategoryRequest,
  completeTask as completeTaskRequest,
  completeTaskGroup as completeTaskGroupRequest,
  queryTasks,
  readTask as readTaskRequest,
  renameTask as renameTaskRequest,
  reopenTask as reopenTaskRequest,
} from "../api/generated/sdk.gen"
import type { StaffTaskCategory, Task } from "../api/generated/types.gen"
import { appendUniqueByID } from "../workspace-ordering"
import { type PortalFailure, portalRequest } from "./portal-request"

type TaskVersion = Pick<Task, "id" | "version">

export function readTask(taskID: string, signal?: AbortSignal) {
  return portalRequest(
    (transport) => readTaskRequest({ ...transport, path: { taskId: taskID } }),
    signal,
  )
}

export function renameTask(task: TaskVersion, title: string) {
  return portalRequest((transport) =>
    renameTaskRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version, title },
    }),
  )
}

export function completeTask(task: TaskVersion) {
  return portalRequest((transport) =>
    completeTaskRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version },
    }),
  )
}

/** Completes exactly the reviewed members; the first member addresses the group. */
export function reopenTask(task: TaskVersion) {
  return portalRequest((transport) =>
    reopenTaskRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version },
    }),
  )
}

export function completeTaskGroup(members: TaskVersion[]) {
  return portalRequest((transport) =>
    completeTaskGroupRequest({
      ...transport,
      path: { taskId: members[0].id },
      body: {
        members: members.map((member) => ({
          id: member.id,
          expectedVersion: member.version,
        })),
      },
    }),
  )
}

export function changeTaskCategory(task: TaskVersion, category: StaffTaskCategory) {
  return portalRequest((transport) =>
    changeTaskCategoryRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version, category },
    }),
  )
}

export type ReviewFolderKind = "texts" | "calls" | "appointments"

export type ReviewFolderPage = {
  items: Task[]
  nextCursor: string
  loading: boolean
  failure?: PortalFailure
}

/**
 * Open reviews for one folder, independent of the My Tasks filter. `revision`
 * and `count` changes reload the pages already shown; `showMore` appends.
 */
export function useReviewFolder({
  practiceID,
  locationID,
  search,
  kind,
  revision,
  count,
}: {
  practiceID: string
  locationID: string
  search: string
  kind: ReviewFolderKind
  revision: number
  count: number
}): ReviewFolderPage & { retry: () => void; showMore: () => void } {
  const [page, setPage] = useState<ReviewFolderPage>({
    items: [],
    nextCursor: "",
    loading: true,
  })
  const depth = useRef(0)
  const request = useRef<AbortController | null>(null)
  const load = useCallback(
    async (cursor = "") => {
      request.current?.abort()
      const controller = new AbortController()
      request.current = controller
      const { signal } = controller
      setPage((current) => ({ ...current, loading: true, failure: undefined }))
      try {
        const items: Task[] = []
        let nextCursor = cursor
        do {
          const outcome = await portalRequest(
            (transport) =>
              queryTasks({
                ...transport,
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
              }),
            signal,
          )
          if (!outcome.ok) {
            setPage({ items: [], nextCursor: "", loading: false, failure: outcome.failure })
            return
          }
          items.push(...outcome.data.items)
          nextCursor = outcome.data.nextCursor
        } while (!cursor && nextCursor && items.length < depth.current)
        setPage((current) => {
          const combined = cursor ? appendUniqueByID(current.items, items) : items
          depth.current = combined.length
          return { items: combined, nextCursor, loading: false }
        })
      } catch (error) {
        if (!signal.aborted) throw error
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

  return {
    ...page,
    retry: () => void load(),
    showMore: () => void load(page.nextCursor),
  }
}
