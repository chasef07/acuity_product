"use client"

import { type ReactNode, useCallback } from "react"

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
  onOpenTask: (task: Task) => void
  onReturnToCall: () => void
  patientTasks: (phone: string, currentTaskID?: string) => ReactNode
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
  onOpenTask,
  onReturnToCall,
  patientTasks,
}: TaskCallContextProps) {
  const openRecoveryTask = useCallback(
    async (taskID: string) => {
      const outcome = await readTask(taskID)
      if (outcome.ok) onOpenTask(outcome.data)
    },
    [onOpenTask],
  )
  if (view === "call" && activeCall) {
    return (
      <CallWorkspace
        key={activeCall.id}
        call={activeCall}
        returnTask={task}
        onReturnToTask={task ? () => onTaskUpdated(task) : undefined}
        onOpenRecoveryTask={(taskID) => void openRecoveryTask(taskID)}
        patientTasks={patientTasks(activeCall.phone)}
      />
    )
  }
  if (view === "task" && task) {
    return (
      <div className="flex min-h-0 flex-1 flex-col">
        {patientTasks(task.phone, task.id)}
        {group && onSelectTask ? (
          <TaskGroupContext key={group.id} group={group} taskRows={taskRows ?? []} onSelect={onSelectTask} onUpdated={onTaskUpdated} />
        ) : (
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
        )}
      </div>
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
}
