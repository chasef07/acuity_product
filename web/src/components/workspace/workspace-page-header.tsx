"use client"

import type { ReactNode } from "react"
import { ChevronRightIcon } from "lucide-react"

export function WorkspaceTopBar({
  section,
  page,
  onSection,
  children,
}: {
  section: string
  page?: string
  onSection?: () => void
  children?: ReactNode
}) {
  return (
    <header className="flex min-h-12 shrink-0 items-center justify-between gap-3 border-b px-4 py-1.5">
      <nav aria-label="Current location" className="min-w-0 text-xs">
        <ol className="flex min-w-0 items-center gap-2">
          <li className="shrink-0">
            {page && onSection ? (
              <button type="button" onClick={onSection} className="rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring">{section}</button>
            ) : <span aria-current={page ? undefined : "page"} className={page ? "text-muted-foreground" : "font-medium"}>{section}</span>}
          </li>
          {page && (
            <li className="flex min-w-0 items-center gap-2">
              <ChevronRightIcon aria-hidden="true" className="size-3 shrink-0 text-muted-foreground" />
              <span aria-current="page" className="truncate font-medium">{page}</span>
            </li>
          )}
        </ol>
      </nav>
      {children && <div className="w-56 max-w-[50%] shrink-0">{children}</div>}
    </header>
  )
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
