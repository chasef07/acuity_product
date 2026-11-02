"use client"

import { useId, type ReactNode } from "react"
import { ChevronRightIcon, HistoryIcon } from "lucide-react"

import { Alert, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { signInRequired, type PortalFailure } from "@/lib/clients/portal-request"
import type { PortalQuery } from "@/lib/data/queries"
import { WorkspacePageHeader } from "./workspace-page-header"

export type AgentLocationProps = {
  practiceID: string
  locationID: string
}

export function AgentPage({
  title,
  detail,
  actions,
  controls,
  children,
}: {
  title: string
  detail?: ReactNode
  actions?: ReactNode
  controls: ReactNode
  children: ReactNode
}) {
  const titleID = useId()
  return (
    <section aria-labelledby={titleID} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <WorkspacePageHeader titleID={titleID} title={title} detail={detail}>
        {actions}
      </WorkspacePageHeader>
      <div className="flex flex-wrap items-center gap-2 px-4 pt-4 empty:hidden sm:px-6">{controls}</div>
      <div className="min-h-0 flex-1 overflow-auto px-4 pt-4 pb-6 sm:px-6">{children}</div>
    </section>
  )
}

export function HistorySheet({ description, children }: { description: string; children: ReactNode }) {
  return (
    <Sheet>
      <SheetTrigger render={<Button variant="outline" size="sm" />}>
        <HistoryIcon data-icon="inline-start" />
        History
      </SheetTrigger>
      <SheetContent className="h-full overflow-y-auto data-[side=right]:w-full data-[side=right]:sm:max-w-xl">
        <SheetHeader className="border-b px-5 py-4">
          <SheetTitle>History</SheetTitle>
          <SheetDescription>{description}</SheetDescription>
        </SheetHeader>
        {children}
      </SheetContent>
    </Sheet>
  )
}

export function PublishBarFrame({
  draft,
  count,
  canPublish,
  onReason,
  onDiscard,
  onPublish,
  children,
}: {
  draft: { reason: string; publish: { status: string } }
  count: number
  canPublish: boolean
  onReason: (reason: string) => void
  onDiscard: () => void
  onPublish: () => void
  children: ReactNode
}) {
  const reasonID = useId()
  const publishing = draft.publish.status === "publishing"
  return (
    <fieldset
      disabled={publishing}
      className="flex min-w-0 flex-1 basis-xl flex-wrap items-center gap-2 rounded-lg border border-primary bg-primary/5 p-2"
    >
      {count > 0 && (
        <p className="shrink-0 text-sm font-medium">
          {count} unpublished {count === 1 ? "change" : "changes"}
        </p>
      )}
      <label htmlFor={reasonID} className="sr-only">
        Why are you changing this?
      </label>
      <Input
        id={reasonID}
        className="min-w-40 flex-1 bg-background"
        placeholder="Why are you changing this?"
        value={draft.reason}
        maxLength={2000}
        onChange={(event) => onReason(event.target.value)}
      />
      <Button variant="ghost" onClick={onDiscard}>
        Discard changes
      </Button>
      <Button disabled={!canPublish} onClick={onPublish}>
        {publishing && <Spinner data-icon="inline-start" />}
        {publishing ? "Publishing…" : "Publish"}
      </Button>
      {children}
    </fieldset>
  )
}

export function publishFailureMessage(failure: PortalFailure) {
  if (signInRequired(failure)) return "Sign in again to publish."
  if (failure.kind === "unauthorized") return "You don't have access to change this Location's knowledge."
  return failure.message || "Not published. Nothing changed. Try again."
}

export function OptionSelect<V extends string>({
  label,
  value,
  items,
  onChange,
}: {
  label: string
  value: V
  items: { value: V; label: string }[]
  onChange: (value: V) => void
}) {
  return (
    <Select
      value={value}
      items={items}
      onValueChange={(next) => next && onChange(next)}
    >
      <SelectTrigger aria-label={label}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectGroup>
          {items.map((item) => (
            <SelectItem key={item.value} value={item.value}>
              {item.label}
            </SelectItem>
          ))}
        </SelectGroup>
      </SelectContent>
    </Select>
  )
}

export function AgentQueryBody<T>({
  query,
  subject,
  unavailable,
  onRetry,
  missingLocation = false,
  isEmpty,
  emptyTitle,
  emptyDescription,
  children,
}: {
  query: PortalQuery<T>
  subject: string
  unavailable: string
  onRetry: () => void
  missingLocation?: boolean
  isEmpty: (data: T) => boolean
  emptyTitle: string
  emptyDescription: string
  children: (data: T) => ReactNode
}) {
  if (missingLocation) return <ChooseOffice />
  if (query.status === "loading") {
    return (
      <div role="status" aria-label={`Loading ${subject}`} className="flex flex-col gap-3 py-2">
        <span className="sr-only">Loading {subject}…</span>
        {Array.from({ length: 6 }, (_, index) => (
          <Skeleton key={index} className="h-9 w-full" />
        ))}
      </div>
    )
  }
  if (query.status === "failed") {
    return <QueryFailure failure={query.failure} subject={subject} unavailable={unavailable} onRetry={onRetry} />
  }
  return (
    <>
      {query.refreshFailure && (
        <QueryFailure failure={query.refreshFailure} subject={subject} unavailable={unavailable} onRetry={onRetry} />
      )}
      {isEmpty(query.data) ? <AgentEmpty title={emptyTitle} description={emptyDescription} /> : children(query.data)}
    </>
  )
}

function QueryFailure({
  failure,
  subject,
  unavailable,
  onRetry,
}: {
  failure: PortalFailure
  subject: string
  unavailable: string
  onRetry: () => void
}) {
  return (
    <Alert variant="destructive" className="my-6">
      <AlertTitle>{failureMessage(failure, subject, unavailable)}</AlertTitle>
      {failure.retryable && (
        <Button className="mt-3 w-fit" variant="outline" onClick={onRetry}>
          Try again
        </Button>
      )}
    </Alert>
  )
}

function failureMessage(failure: PortalFailure, subject: string, unavailable: string) {
  if (signInRequired(failure)) return `Sign in again to view ${subject}.`
  if (failure.kind === "unauthorized") return `You don't have access to this Location's ${subject}.`
  if (failure.kind === "unavailable") return failure.message ?? unavailable
  return `The ${subject} could not be loaded.`
}

function ChooseOffice() {
  return (
    <AgentEmpty
      title="Choose an office"
      description="This shows one office at a time. Choose an office using the office picker above."
    />
  )
}

function AgentEmpty({ title, description }: { title: string; description: string }) {
  return (
    <Empty className="py-16">
      <EmptyHeader>
        <EmptyTitle>{title}</EmptyTitle>
        <EmptyDescription>{description}</EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}

export function DisclosureRow({
  title,
  detail,
  aside,
  group = false,
  defaultOpen = group,
  children,
}: {
  title: string
  detail?: ReactNode
  aside?: ReactNode
  group?: boolean
  defaultOpen?: boolean
  children: ReactNode
}) {
  const titleID = useId()
  return (
    <Collapsible
      defaultOpen={defaultOpen}
      render={group ? <div role="group" aria-labelledby={titleID} /> : <li />}
    >
      <CollapsibleTrigger
        render={
          <button
            type="button"
            className="group/disclosure flex min-h-10 w-full items-baseline gap-3 px-3 py-2 text-left text-sm outline-hidden transition-colors hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          />
        }
      >
        <ChevronRightIcon
          aria-hidden="true"
          className="size-3.5 shrink-0 translate-y-0.5 self-start text-muted-foreground transition-transform group-aria-expanded/disclosure:rotate-90 motion-reduce:transition-none"
        />
        <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2">
          <span id={titleID} className="font-medium">
            {title}
          </span>
          {detail ? <span className="text-muted-foreground">{detail}</span> : null}
        </span>
        {aside ? (
          <span className="shrink-0 font-mono text-xs text-muted-foreground">{aside}</span>
        ) : null}
      </CollapsibleTrigger>
      <CollapsibleContent hiddenUntilFound>{children}</CollapsibleContent>
    </Collapsible>
  )
}
