import {
  changeTaskCategory as changeTaskCategoryRequest,
  completeTask as completeTaskRequest,
  readTask as readTaskRequest,
  renameTask as renameTaskRequest,
  reopenTask as reopenTaskRequest,
} from "../api/generated/sdk.gen"
import type { StaffTaskCategory, StaffTaskCloseReason, Task } from "../api/generated/types.gen"
import { useRead } from "../data/queries"
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

export function changeTaskCategory(task: TaskVersion, category: StaffTaskCategory) {
  return portalRequest((transport) =>
    changeTaskCategoryRequest({
      ...transport,
      path: { taskId: task.id },
      body: { expectedVersion: task.version, category },
    }),
  )
}
