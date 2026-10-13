"use client"

import { useId, type ReactNode } from "react"
import { ChevronRightIcon } from "lucide-react"

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
import { Skeleton } from "@/components/ui/skeleton"
import type { Location } from "@/lib/api/generated/types.gen"
import type { PortalFailure, PortalQuery } from "@/lib/clients/portal-request"

export type AgentLocationProps = {
  practiceID: string
  locations: Location[]
  locationID: string
  onLocation: (locationID: string) => void
}

export function AgentPage({
  title,
  action,
  controls,
  children,
}: {
  title: string
  action?: ReactNode
  controls: ReactNode
  children: ReactNode
}) {
  const titleID = useId()
  return (
    <section aria-labelledby={titleID} className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex shrink-0 items-center gap-3 px-4 pb-3 pt-6 sm:px-6">
        <h1 id={titleID} className="flex-1 text-2xl font-semibold tracking-tight">
          {title}
        </h1>
        {action}
      </header>
      <div className="flex flex-wrap items-center gap-2 px-4 pb-5 pt-2 sm:px-6">{controls}</div>
      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6 sm:px-6">{children}</div>
    </section>
  )
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

export function LocationSelect({ locations, locationID, onLocation }: AgentLocationProps) {
  return locationID ? (
    <OptionSelect
      label="Location"
      value={locationID}
      items={locations.map((location) => ({ value: location.id, label: location.name }))}
      onChange={onLocation}
    />
  ) : null
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
  if (missingLocation) {
    return (
      <AgentEmpty
        title="No Location available"
        description="This page shows one authorized Location at a time."
      />
    )
  }
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
    return (
      <Alert variant="destructive" className="my-6">
        <AlertTitle>{failureMessage(query.failure, subject, unavailable)}</AlertTitle>
        {query.failure.retryable && (
          <Button className="mt-3 w-fit" variant="outline" onClick={onRetry}>
            Try again
          </Button>
        )}
      </Alert>
    )
  }
  if (isEmpty(query.data)) {
    return <AgentEmpty title={emptyTitle} description={emptyDescription} />
  }
  return children(query.data)
}

function failureMessage(failure: PortalFailure, subject: string, unavailable: string) {
  if (failure.kind === "signedOut" || failure.kind === "unauthenticated")
    return `Sign in again to view ${subject}.`
  if (failure.kind === "unauthorized") return `You don't have access to this Location's ${subject}.`
  if (failure.kind === "unavailable") return failure.message ?? unavailable
  return `The ${subject} could not be loaded.`
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
