"use client"

import { useCallback } from "react"

import type {
  CallingCall,
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { readTask } from "@/lib/clients/tasks"

import { TaskGroupContext } from "./task-group-context"
import { CallWorkspace } from "./call-workspace"
import { TaskWorkspace } from "./task-workspace"

type TaskCallContextProps = {
  task: Task | undefined
  group?: Task
  taskRows?: Task[]
  onSelectTask?: (task: Task) => void
  activeCall: CallingCall | undefined
  view: "none" | "task" | "call"
  numberHistory?: ConversationTimelineItem[]
  taskCallError: string
  onTaskUpdated: (task: Task, advance?: boolean) => void
  onReturnToCall: () => void
}

export function TaskCallContext({
  task,
  group,
  taskRows,
  onSelectTask,
  activeCall,
  view,
  numberHistory,
  taskCallError,
  onTaskUpdated,
  onReturnToCall,
}: TaskCallContextProps) {
  const openRecoveryTask = useCallback(
    async (taskID: string) => {
      const outcome = await readTask(taskID)
      if (outcome.ok) onTaskUpdated(outcome.data)
    },
    [onTaskUpdated],
  )
  if (view === "call" && activeCall) {
    return (
      <CallWorkspace
        key={activeCall.id}
        call={activeCall}
        returnTask={task}
        onReturnToTask={task ? () => onTaskUpdated(task) : undefined}
        onOpenRecoveryTask={(taskID) => void openRecoveryTask(taskID)}
      />
    )
  }
  if (view === "task" && task && group && onSelectTask) {
    return <TaskGroupContext key={group.id} group={group} taskRows={taskRows ?? []} onSelect={onSelectTask} onUpdated={onTaskUpdated} />
  }
  if (view === "task" && task) {
    return (
      <TaskWorkspace
        key={task.id}
        task={task}
        onNextTask={onSelectTask && taskRows?.some((row) => row.id !== task.id)
          ? () => onSelectTask(taskRows.find((row) => row.id !== task.id)!) : undefined}
        activeCall={activeCall}
        numberHistory={numberHistory}
        taskCallError={taskCallError}
        onTaskUpdated={onTaskUpdated}
        onReturnToCall={onReturnToCall}
      />
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
}
