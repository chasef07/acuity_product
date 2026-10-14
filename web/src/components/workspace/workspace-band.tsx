"use client"

import { createContext, useContext, type ReactNode, type Ref } from "react"
import { createPortal } from "react-dom"

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
