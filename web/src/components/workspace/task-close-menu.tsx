"use client"

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { StaffTaskCloseReason, Task } from "@/lib/api/generated/types.gen"
import { taskCloseMenuReasons, taskCloseReasonLabels } from "@/lib/vocabulary"

export function offersTaskCloseMenu(task: Pick<Task, "origin">) {
  return task.origin !== "APPOINTMENT_REVIEW" && task.origin !== "INBOUND_MESSAGE_REVIEW"
}

export function TaskCloseMenu({
  trigger,
  children,
  reasons = taskCloseMenuReasons,
  onClose,
}: {
  trigger: React.ReactElement
  children: React.ReactNode
  reasons?: readonly StaffTaskCloseReason[]
  onClose: (reason: StaffTaskCloseReason) => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={trigger}>{children}</DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-40">
        <DropdownMenuGroup>
          <DropdownMenuLabel>Close as</DropdownMenuLabel>
          {reasons.map((reason) => (
            <DropdownMenuItem key={reason} onClick={() => onClose(reason)}>
              {taskCloseReasonLabels[reason]}
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
