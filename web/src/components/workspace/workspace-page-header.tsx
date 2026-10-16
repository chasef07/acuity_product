"use client"

import type { ReactNode } from "react"
import { ChevronRightIcon } from "lucide-react"

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
    <header className="flex h-12 min-w-0 shrink-0 items-center gap-3 px-4 sm:px-6">
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
  )
}
