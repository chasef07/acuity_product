"use client"

import { type ReactNode, useCallback } from "react"

import type {
  CallingCall,
  ConversationTimelineItem,
  Task,
} from "@/lib/api/generated/types.gen"
import { readTask } from "@/lib/clients/tasks"

import { CallWorkspace } from "./call-workspace"
import type { ExpandedTask } from "./patient-tasks-panel"
import { TaskWorkspace } from "./task-workspace"

type TaskCallContextProps = {
  task: Task | undefined
  nextTask?: Task
  onSelectTask?: (task: Task) => void
  activeCall: CallingCall | undefined
  callControlsShowing: boolean
  view: "none" | "task" | "call"
  numberHistory?: ConversationTimelineItem[]
  onTaskUpdated: (task: Task) => void
  onOpenTask: (task: Task) => void
  onReturnToCall: () => void
  patientTasks: (phone: string, placement: PatientTasksPlacement) => ReactNode
}

export type PatientTasksPlacement =
  | { beside: "task"; expanded?: ExpandedTask }
  | { beside: "call"; callTaskID?: string }

export function TaskCallContext({
  task,
  nextTask,
  onSelectTask,
  activeCall,
  callControlsShowing,
  view,
  numberHistory,
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
        live={callControlsShowing}
        returnTask={task}
        onReturnToTask={task ? () => onOpenTask(task) : undefined}
        onOpenRecoveryTask={(taskID) => void openRecoveryTask(taskID)}
      >
        {patientTasks(activeCall.phone, {
          beside: "call",
          callTaskID: activeCall.recoveryTask?.id ?? activeCall.taskId,
        })}
      </CallWorkspace>
    )
  }
  if (view === "task" && task) {
    return (
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {patientTasks(task.phone, reviewedInConversation(task) ? { beside: "task" } : {
          beside: "task",
          expanded: {
            task,
            content: (
              <TaskWorkspace
                key={task.id}
                task={task}
                onNextTask={onSelectTask && nextTask ? () => onSelectTask(nextTask) : undefined}
                activeCall={activeCall}
                numberHistory={numberHistory}
                onTaskUpdated={onTaskUpdated}
                onReturnToCall={onReturnToCall}
              />
            ),
          },
        })}
      </div>
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
}

export function reviewedInConversation(task: Pick<Task, "origin">) {
  return task.origin === "INBOUND_MESSAGE_REVIEW"
}

export function DetailsAbovePatientTasks({
  children,
  patientTasks,
}: {
  children: ReactNode
  patientTasks: ReactNode
}) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <div className="flex min-h-12 flex-col overflow-hidden">{children}</div>
      {patientTasks}
    </div>
  )
}
