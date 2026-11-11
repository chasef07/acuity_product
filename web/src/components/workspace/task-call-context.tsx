"use client"

import { type ReactNode, useCallback, useMemo } from "react"

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
  callControlsShowing: boolean
  view: "none" | "task" | "call"
  numberHistory?: ConversationTimelineItem[]
  taskCallError: string
  onTaskUpdated: (task: Task, advance?: boolean) => void
  onOpenTask: (task: Task) => void
  onReturnToCall: () => void
  patientTasks: (phone: string, placement: PatientTasksPlacement) => ReactNode
}

export type PatientTasksPlacement =
  | { beside: "task"; otherThan: readonly Task[] }
  | { beside: "call"; callTaskID?: string }

export function TaskCallContext({
  task,
  group,
  taskRows,
  onSelectTask,
  activeCall,
  callControlsShowing,
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
  const shownTasks = useMemo(
    () => group?.groupMembers ?? (task ? [task] : []),
    [group, task],
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
    const otherTasks = patientTasks(task.phone, { beside: "task", otherThan: shownTasks })
    return (
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {group && onSelectTask ? (
          <>
            <TaskGroupContext key={group.id} group={group} taskRows={taskRows ?? []} onSelect={onSelectTask} onUpdated={onTaskUpdated} />
            {otherTasks}
          </>
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
          >
            {otherTasks}
          </TaskWorkspace>
        )}
      </div>
    )
  }
  return <section aria-label="No Task selected" className="min-h-0 flex-1" />
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
