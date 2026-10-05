"use client"

import type { ReactNode } from "react"

import { Alert, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
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
import type { PortalFailure } from "@/lib/clients/portal-request"

export function AgentPageHeader({
  title,
  action,
  children,
}: {
  title: string
  action?: ReactNode
  children?: ReactNode
}) {
  return (
    <>
      <header className="flex shrink-0 items-center gap-3 px-4 pb-3 pt-6 sm:px-6">
        <h1 className="flex-1 text-2xl font-semibold tracking-tight">{title}</h1>
        {action}
      </header>
      {children && (
        <div className="flex flex-wrap items-center gap-2 px-4 pb-5 pt-2 sm:px-6">
          {children}
        </div>
      )}
    </>
  )
}

export function OptionSelect({
  label,
  value,
  items,
  onChange,
}: {
  label: string
  value: string
  items: { value: string; label: string }[]
  onChange: (value: string) => void
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

export function LocationSelect({
  locations,
  value,
  onChange,
}: {
  locations: Location[]
  value: string
  onChange: (value: string) => void
}) {
  return (
    <OptionSelect
      label="Location"
      value={value}
      items={locations.map((location) => ({
        value: location.id,
        label: location.name,
      }))}
      onChange={onChange}
    />
  )
}

export function initialLocationID(locations: Location[], locationScopeID: string) {
  return locations.some((location) => location.id === locationScopeID)
    ? locationScopeID
    : (locations[0]?.id ?? "")
}

export function AgentPageFailure({
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
  const message =
    failure.kind === "signedOut" || failure.kind === "unauthenticated"
      ? `Sign in again to view ${subject}.`
      : failure.kind === "unauthorized"
        ? `You don't have access to this Location's ${subject}.`
        : failure.kind === "unavailable"
          ? (failure.message ?? unavailable)
          : `The ${subject} could not be loaded.`
  return (
    <Alert variant="destructive" className="my-6">
      <AlertTitle>{message}</AlertTitle>
      {failure.retryable && (
        <Button className="mt-3 w-fit" variant="outline" onClick={onRetry}>
          Try again
        </Button>
      )}
    </Alert>
  )
}

export function AgentPageLoading({ label }: { label: string }) {
  return (
    <div role="status" aria-label={`Loading ${label}`} className="flex flex-col gap-3 py-2">
      <span className="sr-only">Loading {label}…</span>
      {Array.from({ length: 6 }, (_, index) => (
        <Skeleton key={index} className="h-9 w-full" />
      ))}
    </div>
  )
}

export function NoLocation() {
  return (
    <Empty className="py-16">
      <EmptyHeader>
        <EmptyTitle>No Location available</EmptyTitle>
        <EmptyDescription>
          This page shows one authorized Location at a time.
        </EmptyDescription>
      </EmptyHeader>
    </Empty>
  )
}
