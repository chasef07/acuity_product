"use client"

import { ChevronDownIcon, FolderIcon, FolderOpenIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  SidebarGroup,
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuItem,
} from "@/components/ui/sidebar"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"

export function AttentionGroup({
  title,
  count,
  expanded,
  onToggle,
  children,
}: {
  title: string
  count?: number
  expanded: boolean
  onToggle: () => void
  children: React.ReactNode
}) {
  const FolderStateIcon = expanded ? FolderOpenIcon : FolderIcon

  return (
    <Collapsible
      open={expanded}
      onOpenChange={(open) => {
        if (open !== expanded) onToggle()
      }}
      render={<SidebarGroup className="p-0" role="group" aria-label={title} />}
    >
      <div className="flex min-w-0 items-center gap-0.5">
        <CollapsibleTrigger
          render={
            <button
              type="button"
              className="group/disclosure flex h-8 min-w-0 flex-1 shrink-0 items-center gap-2 rounded-md px-2.5 text-left text-sm/5 font-medium text-sidebar-foreground/90 outline-hidden transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            />
          }
        >
          <FolderStateIcon
            aria-hidden="true"
            className="size-4 shrink-0 text-[var(--sidebar-icon-color)] group-hover/disclosure:text-sidebar-foreground"
          />
          <span className="truncate">{title}</span>
          {count !== undefined && (
            <span className="ml-auto text-xs tabular-nums text-muted-foreground">
              {count}
            </span>
          )}
        </CollapsibleTrigger>
      </div>
      <CollapsibleContent>
        <SidebarGroupContent>
          <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
            {children}
          </SidebarMenu>
        </SidebarGroupContent>
      </CollapsibleContent>
    </Collapsible>
  )
}

export function CompletedGroup({
  title,
  expanded,
  onToggle,
  children,
}: {
  title: string
  expanded: boolean
  onToggle: () => void
  children: React.ReactNode
}) {
  return (
    <Collapsible
      open={expanded}
      onOpenChange={(open) => {
        if (open !== expanded) onToggle()
      }}
      render={<SidebarGroup className="p-0" />}
    >
      <div className="flex min-w-0 items-center gap-0.5">
        <CollapsibleTrigger
          render={
            <Button
              variant="ghost"
              className="group/disclosure flex h-8 min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 text-xs font-normal text-muted-foreground hover:bg-transparent hover:text-sidebar-foreground focus-visible:ring-2 focus-visible:ring-sidebar-ring"
            />
          }
        >
          <span className="truncate">{title}</span>
          <span aria-hidden="true" className="h-px flex-1 bg-sidebar-border" />
          <ChevronDownIcon
            aria-hidden="true"
            className={cn(
              "size-3 shrink-0 transition-transform",
              !expanded && "-rotate-90",
            )}
          />
        </CollapsibleTrigger>
      </div>
      <CollapsibleContent>
        <SidebarGroupContent>
          <SidebarMenu className="mx-1 w-auto gap-0.5 px-1.5 py-0">
            {children}
          </SidebarMenu>
        </SidebarGroupContent>
      </CollapsibleContent>
    </Collapsible>
  )
}

export function RailLoading({ label }: { label: string }) {
  return (
    <SidebarMenuItem className="flex items-center gap-2 px-3 py-2 text-xs text-muted-foreground">
      <Spinner />
      {label}
    </SidebarMenuItem>
  )
}

export function RailEmpty({ children }: { children: string }) {
  return (
    <SidebarMenuItem className="px-3 py-2 text-xs text-muted-foreground">
      {children}
    </SidebarMenuItem>
  )
}

export function RailShowMore({
  cursor,
  loading,
  onLoadMore,
}: {
  cursor: string
  loading: boolean
  onLoadMore: () => void
}) {
  if (!cursor) return null
  return (
    <SidebarMenuItem className="px-1 py-1">
      <button
        type="button"
        className="flex h-8 w-full items-center justify-center rounded-md text-xs font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-foreground disabled:pointer-events-none disabled:opacity-60"
        disabled={loading}
        onClick={onLoadMore}
      >
        {loading ? <Spinner /> : "Show more"}
      </button>
    </SidebarMenuItem>
  )
}
