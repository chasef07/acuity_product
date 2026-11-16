import {
  changeTaskCategory as changeTaskCategoryRequest,
  completeTask as completeTaskRequest,
  completeTaskGroup as completeTaskGroupRequest,
  queryTasks,
  readTask as readTaskRequest,
  renameTask as renameTaskRequest,
  reopenTask as reopenTaskRequest,
} from "../api/generated/sdk.gen"
import type { StaffTaskCategory, StaffTaskCloseReason, Task, TaskQueryRequest } from "../api/generated/types.gen"
import { appendUniqueByID } from "../workspace-ordering"
import { usePages, useRead } from "../data/queries"
import { portalRequest } from "./portal-request"

type TaskVersion = Pick<Task, "id" | "version">

export function readTask(taskID: string, signal?: AbortSignal) {
  return portalRequest(
    (transport) => readTaskRequest({ ...transport, path: { taskId: taskID } }),
    signal,
  )
}

export function useOpenTask(taskID: string | undefined) {
  return useRead({
    live: "task-detail",
    name: "task",
    identity: { taskID: taskID ?? "" },
    enabled: taskID !== undefined,
    read: (identity, transport) =>
      readTaskRequest({ ...transport, path: { taskId: identity.taskID } }),
  })
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

export function completeTask(task: TaskVersion, reason: StaffTaskCloseReason) {
  return portalRequest((transport) =>
    completeTaskRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version, reason },
    }),
  )
}

export function reopenTask(task: TaskVersion) {
  return portalRequest((transport) =>
    reopenTaskRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version },
    }),
  )
}

export function completeTaskGroup(members: TaskVersion[], reason: StaffTaskCloseReason) {
  return portalRequest((transport) =>
    completeTaskGroupRequest({
      ...transport,
      path: { taskId: members[0].id },
      body: {
        reason,
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

export type ReviewFolderKind = Exclude<TaskQueryRequest["kind"], "follow_up" | undefined>

export function useReviewFolder({
  practiceID,
  locationID,
  search,
  kind,
}: {
  practiceID: string
  locationID: string
  search: string
  kind: ReviewFolderKind
}) {
  return usePages({
    live: "review-folders",
    name: "review-folder",
    identity: { practiceID, locationID, search, kind },
    read: (identity, cursor, transport) =>
      queryTasks({
        ...transport,
        body: {
          practiceId: identity.practiceID,
          ...(identity.locationID ? { locationId: identity.locationID } : {}),
          ...(identity.search ? { search: identity.search } : {}),
          kind: identity.kind,
          state: "OPEN",
          ordering: "recent",
          responsibility: "all",
          grouped: false,
          includeCounts: false,
          limit: 50,
          ...(cursor ? { cursor } : {}),
        },
      }),
    combine: combineTaskPages,
  })
}

function combineTaskPages(pages: { items: Task[]; nextCursor: string }[]) {
  return {
    items: pages.reduce<Task[]>((items, page) => appendUniqueByID(items, page.items), []),
    nextCursor: pages.at(-1)!.nextCursor,
  }
}
