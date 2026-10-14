"use client"

import { createContext, useContext, type ReactNode, type Ref } from "react"
import { createPortal } from "react-dom"
import { ChevronRightIcon } from "lucide-react"

import { useSidebar } from "@/components/ui/sidebar"
import { cn } from "@/lib/utils"

export const workspaceBand = "flex shrink-0 items-center empty:hidden md:h-10 md:empty:flex"

export const WorkspaceBandContext = createContext<HTMLElement | null>(null)

export function WorkspaceBand({ ref }: { ref: Ref<HTMLDivElement> }) {
  const { open, isMobile } = useSidebar()
  return (
    <div
      ref={ref}
      className={cn(workspaceBand, "transition-[padding] duration-200 ease-linear", open && !isMobile && "md:pl-(--sidebar-width)")}
    />
  )
}

export function InWorkspaceBand({ children }: { children: ReactNode }) {
  const band = useContext(WorkspaceBandContext)
  return band ? createPortal(children, band) : children
}

export function WorkspacePageHeader({
  titleID,
  section,
  title,
  detail,
  children,
}: {
  titleID?: string
  section?: string
  title: string
  detail?: ReactNode
  children?: ReactNode
}) {
  return (
    <InWorkspaceBand>
      <header className="flex h-12 min-w-0 flex-1 shrink-0 items-center gap-3 px-4 sm:px-6 md:h-10">
        <div className="flex min-w-0 flex-1 items-center gap-2 text-base md:text-sm">
          {section && (
            <>
              <span className="shrink-0 text-muted-foreground">{section}</span>
              <ChevronRightIcon aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
            </>
          )}
          <h1 id={titleID} className="shrink-0 font-medium tracking-tight">
            {title}
          </h1>
          {detail && <p className="min-w-0 truncate text-muted-foreground">{detail}</p>}
        </div>
        {children}
      </header>
    </InWorkspaceBand>
  )
}
