"use client"

import { ArrowUpRightIcon, BlocksIcon, BookOpenIcon, ShieldCheckIcon } from "lucide-react"

import { AnimatedAcuityMark } from "@/components/animated-acuity-mark"
import { cn } from "@/lib/utils"

export type AgentArea = "knowledge" | "insurance" | "tools"

export const agentAreas = [
  {
    id: "knowledge",
    title: "Knowledge base",
    description: "Your office’s hours, directions, and answers to common questions.",
    icon: BookOpenIcon,
    position: "@[36rem]:col-start-1 @[36rem]:row-start-1",
    path: "M120 150 C60 150 60 70 0 70",
  },
  {
    id: "insurance",
    title: "Insurance",
    description: "Accepted plans, requirements, and coverage guidance.",
    icon: ShieldCheckIcon,
    position: "@[36rem]:col-start-1 @[36rem]:row-start-2",
    path: "M120 150 C60 150 60 230 0 230",
  },
  {
    id: "tools",
    title: "Tools",
    description: "The actions your agent can take to help callers and your team.",
    icon: BlocksIcon,
    position: "@[36rem]:col-start-3 @[36rem]:row-start-1 @[36rem]:row-span-2 @[36rem]:self-center",
    path: "M120 150 H240",
  },
] as const

export function AgentHomeMap({
  selected,
  detailsID,
  onSelect,
}: {
  selected?: AgentArea
  detailsID: string
  onSelect: (area: AgentArea) => void
}) {
  return (
    <div
      role="group"
      aria-label="Agent connections"
      className="relative isolate flex flex-col gap-3 @[36rem]:grid @[36rem]:grid-cols-[minmax(0,1fr)_9rem_minmax(0,1fr)] @[36rem]:grid-rows-[repeat(2,minmax(8rem,auto))] @[36rem]:gap-x-12 @[36rem]:gap-y-6"
    >
      <svg
        aria-hidden="true"
        viewBox="0 0 240 300"
        preserveAspectRatio="none"
        className="pointer-events-none absolute inset-y-0 left-[calc(50%_-_7.5rem)] hidden h-full w-60 @[36rem]:block"
        fill="none"
      >
        {agentAreas.map((area) => (
          <path
            key={area.id}
            d={area.path}
            vectorEffect="non-scaling-stroke"
            stroke="currentColor"
            strokeWidth="1.25"
            className={selected === area.id ? "text-muted-foreground" : "text-border"}
          />
        ))}
      </svg>
      {agentAreas.map((area) => (
        <button
          key={area.id}
          id={`${detailsID}-${area.id}`}
          type="button"
          aria-label={area.title}
          aria-expanded={selected === area.id}
          aria-controls={detailsID}
          onClick={() => onSelect(area.id)}
          className={cn(
            "relative z-10 min-w-0 rounded-xl border bg-background p-5 text-left transition-colors hover:border-muted-foreground hover:bg-muted/30 focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:outline-none motion-reduce:transition-none @[36rem]:min-h-32",
            selected === area.id && "border-foreground bg-muted/30",
            area.position,
          )}
        >
          <span className="mb-2 flex items-center gap-2 text-sm font-medium">
            <area.icon aria-hidden="true" className="size-4 shrink-0 text-muted-foreground" />
            {area.title}
            <ArrowUpRightIcon aria-hidden="true" className="ml-auto size-3.5 shrink-0 text-muted-foreground" />
          </span>
          <span className="block text-xs leading-relaxed text-muted-foreground">{area.description}</span>
        </button>
      ))}
      <div className="relative z-10 order-first flex flex-col items-center py-6 @[36rem]:col-start-2 @[36rem]:row-start-1 @[36rem]:row-span-2 @[36rem]:self-center @[36rem]:py-0">
        <div className="flex size-32 items-center justify-center bg-background">
          <AnimatedAcuityMark className="size-20" />
        </div>
        <div className="mt-5 text-center @[36rem]:absolute @[36rem]:top-full @[36rem]:w-44">
          <h2 className="text-2xl font-semibold tracking-tight">Acuity</h2>
        </div>
      </div>
    </div>
  )
}
