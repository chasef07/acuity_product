"use client"

import { useState } from "react"
import { ChevronRightIcon } from "lucide-react"

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
import type {
  InsuranceCoverage,
  InsuranceOutcome,
  InsurancePlanRule,
  Location,
} from "@/lib/api/generated/types.gen"
import { useInsurancePlans } from "@/lib/clients/insurance-plans"
import {
  AgentPageFailure,
  AgentPageHeader,
  AgentPageLoading,
  LocationSelect,
  NoLocation,
  OptionSelect,
  initialLocationID,
} from "./manage-agent-page"

const coverages: { value: InsuranceCoverage; label: string }[] = [
  { value: "medical", label: "Medical" },
  { value: "routine_vision", label: "Routine vision" },
]

const groups: { outcome: InsuranceOutcome; title: string }[] = [
  { outcome: "accepted", title: "Accepted" },
  { outcome: "needs_staff_task", title: "Staff confirms" },
  { outcome: "not_accepted", title: "Not accepted" },
]

const requirements: Record<InsurancePlanRule["requirements"][number], string> = {
  prior_authorization: "Prior auth",
  pcp_referral: "PCP referral",
  staff_verify: "Staff verifies",
}

const list = new Intl.ListFormat("en", { type: "conjunction" })

export function InsuranceList({
  practiceID,
  locationScopeID,
  locations,
}: {
  practiceID: string
  locationScopeID: string
  locations: Location[]
}) {
  const [locationID, setLocationID] = useState(() =>
    initialLocationID(locations, locationScopeID),
  )
  const [coverage, setCoverage] = useState<InsuranceCoverage>("medical")
  const plans = useInsurancePlans({ practiceID, locationID, coverage })
  const coverageLabel = coverages.find((item) => item.value === coverage)!.label

  return (
    <section aria-label="Insurance list" className="flex min-h-0 flex-1 flex-col">
      <AgentPageHeader title="Insurance list">
        {locationID && (
          <>
            <LocationSelect locations={locations} value={locationID} onChange={setLocationID} />
            <OptionSelect
              label="Visit coverage"
              value={coverage}
              items={coverages}
              onChange={(value) => setCoverage(value as InsuranceCoverage)}
            />
          </>
        )}
      </AgentPageHeader>
      <div className="min-h-0 flex-1 overflow-auto px-4 pb-6 sm:px-6">
        {!locationID ? (
          <NoLocation />
        ) : plans.status === "loading" ? (
          <AgentPageLoading label="insurance list" />
        ) : plans.status === "failed" ? (
          <AgentPageFailure
            failure={plans.failure}
            subject="insurance rules"
            unavailable="Insurance rules aren't available right now."
            onRetry={plans.retry}
          />
        ) : plans.data.plans.length === 0 ? (
          <Empty className="py-16">
            <EmptyHeader>
              <EmptyTitle>
                This Location doesn&apos;t offer {coverageLabel.toLowerCase()} visits
              </EmptyTitle>
              <EmptyDescription>Choose another Location or visit coverage.</EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="flex flex-col gap-4">
            {groups.map((group) => {
              const rows = plans.data.plans
                .filter((plan) => plan.outcome === group.outcome)
                .sort((left, right) =>
                  left.label.localeCompare(right.label, undefined, { sensitivity: "base" }),
                )
              return rows.length ? (
                <PlanGroup key={group.outcome} title={group.title} plans={rows} />
              ) : null
            })}
          </div>
        )}
      </div>
    </section>
  )
}

function PlanGroup({ title, plans }: { title: string; plans: InsurancePlanRule[] }) {
  return (
    <Collapsible defaultOpen render={<section aria-label={title} />}>
      <CollapsibleTrigger
        render={
          <button
            type="button"
            className="group/disclosure flex h-8 w-full items-center gap-2 rounded-md px-2 text-left text-sm font-medium outline-hidden transition-colors hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring"
          />
        }
      >
        <ChevronRightIcon
          aria-hidden="true"
          className="size-4 shrink-0 text-muted-foreground transition-transform group-aria-expanded/disclosure:rotate-90 motion-reduce:transition-none"
        />
        <span>{title}</span>
        <span className="text-xs tabular-nums text-muted-foreground">{plans.length}</span>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <ul className="mt-1 divide-y overflow-hidden rounded-lg border bg-card">
          {plans.map((plan) => (
            <PlanRow key={plan.planId} plan={plan} />
          ))}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  )
}

function PlanRow({ plan }: { plan: InsurancePlanRule }) {
  const summary = planSummary(plan)
  const carrier = [plan.carrierId, plan.carrierName].filter(Boolean).join(" · ")
  const details = [
    { label: "Callers also say", value: plan.names.join(", ") },
    { label: "AMD carrier", value: carrier },
    { label: "Sheet note", value: plan.note ?? "" },
    { label: "Callers hear", value: plan.callerNotice ?? "" },
  ].filter((item) => item.value)
  return (
    <Collapsible render={<li />}>
      <CollapsibleTrigger
        render={
          <button
            type="button"
            className="group/plan flex min-h-10 w-full items-baseline gap-3 px-3 py-2 text-left text-sm outline-hidden transition-colors hover:bg-muted/50 focus-visible:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
          />
        }
      >
        <ChevronRightIcon
          aria-hidden="true"
          className="size-3.5 shrink-0 translate-y-0.5 self-start text-muted-foreground transition-transform group-aria-expanded/plan:rotate-90 motion-reduce:transition-none"
        />
        <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-2">
          <span className="font-medium">{plan.label}</span>
          {summary && <span className="text-muted-foreground">{summary}</span>}
        </span>
        {plan.carrierCode && (
          <span className="shrink-0 font-mono text-xs text-muted-foreground">
            {plan.carrierCode}
          </span>
        )}
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="px-3 pb-3 pl-9 text-sm">
          {details.length ? (
            <dl className="grid gap-x-4 gap-y-1.5 sm:grid-cols-[9rem_1fr]">
              {details.map((item) => (
                <div key={item.label} className="contents">
                  <dt className="text-muted-foreground">{item.label}</dt>
                  <dd className="whitespace-pre-line">{item.value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="text-muted-foreground">No other details on the sheet.</p>
          )}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

function planSummary(plan: InsurancePlanRule) {
  if (plan.outcome === "accepted") return plan.allowedProviders.join(", ")
  if (plan.outcome === "needs_staff_task")
    return plan.requirements.length > 0
      ? plan.requirements.map((item) => requirements[item]).join(", ")
      : "Pending on the sheet"
  if (plan.acceptedAt.length > 0)
    return `${list.format(plan.acceptedAt)} ${plan.acceptedAt.length === 1 ? "takes it" : "take it"}`
  return plan.callerNotice ?? ""
}
