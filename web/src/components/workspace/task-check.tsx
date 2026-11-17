"use client"

import { CheckIcon, RotateCcwIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"

export function TaskCheck({
  open,
  label,
  title,
  pending,
  blocked,
  onComplete,
}: {
  open: boolean
  label: string
  title: string
  pending: boolean
  blocked: boolean
  onComplete: () => void
}) {
  if (!open) {
    return (
      <span aria-hidden="true" className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-foreground text-background">
        <CheckIcon className="size-3" />
      </span>
    )
  }
  return (
    <Button
      size="icon-xs"
      variant="outline"
      className="mt-0.5 shrink-0 rounded-full bg-background text-transparent hover:text-success focus-visible:text-success [@media(hover:none)]:text-muted-foreground/50"
      aria-label={label}
      title={title}
      disabled={pending || blocked}
      onClick={onComplete}
    >
      {pending ? <Spinner className="text-muted-foreground" /> : <CheckIcon />}
    </Button>
  )
}

export function ClosedTaskActions({
  className,
  pending,
  settled,
  onNext,
  onReopen,
}: {
  className?: string
  pending: boolean
  settled: boolean
  onNext?: () => void
  onReopen: () => void
}) {
  return (
    <div className={cn("flex items-center gap-1", className)}>
      {onNext && <Button size="sm" onClick={onNext}>Next task</Button>}
      <Button size="sm" variant="ghost" className="text-muted-foreground" disabled={pending || !settled} onClick={onReopen}>
        {pending ? <Spinner data-icon="inline-start" /> : <RotateCcwIcon data-icon="inline-start" />}Reopen
      </Button>
    </div>
  )
}
