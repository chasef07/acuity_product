"use client"

import type { InsuranceCoverage, InsurancePlanRule } from "@/lib/api/generated/types.gen"
import { useInsurancePlans } from "@/lib/clients/insurance-plans"
import {
  insuranceCoverageLabels,
  insuranceOutcomeLabels,
  insuranceRequirementLabels,
  labelOptions,
} from "@/lib/vocabulary"
import {
  AgentPage,
  AgentQueryBody,
  DisclosureRow,
  OptionSelect,
  type AgentLocationProps,
} from "./manage-agent-page"

const coverages = labelOptions(insuranceCoverageLabels)
const groups = labelOptions(insuranceOutcomeLabels)

const list = new Intl.ListFormat("en", { type: "conjunction" })

export function InsuranceList({
  coverage,
  onCoverage,
  ...props
}: AgentLocationProps & {
  coverage: InsuranceCoverage
  onCoverage: (coverage: InsuranceCoverage) => void
}) {
  const plans = useInsurancePlans({ ...props, coverage })
  const coverageLabel = insuranceCoverageLabels[coverage]

  return (
    <AgentPage
      title="Insurance list"
      controls={
        props.locationID ? (
          <OptionSelect
            label="Visit coverage"
            value={coverage}
            items={coverages}
            onChange={onCoverage}
          />
        ) : null
      }
    >
      <AgentQueryBody
        query={plans}
        subject="insurance rules"
        unavailable="Insurance rules aren't available right now."
        onRetry={plans.retry}
        missingLocation={!props.locationID}
        isEmpty={(data) => data.plans.length === 0}
        emptyTitle={`No ${coverageLabel.toLowerCase()} plans are on this office's insurance list.`}
        emptyDescription="Choose another office or visit coverage."
      >
        {(data) => (
          <div className="flex flex-col gap-4">
            {groups.map((group) => {
              const rows = data.plans
                .filter((plan) => plan.outcome === group.value)
                .sort((left, right) =>
                  left.label.localeCompare(right.label, undefined, { sensitivity: "base" }),
                )
              return rows.length ? (
                <DisclosureRow key={group.value} group title={group.label} detail={rows.length}>
                  <ul className="mt-1 divide-y overflow-hidden rounded-lg border bg-card">
                    {rows.map((plan) => (
                      <PlanRow key={plan.planId} plan={plan} />
                    ))}
                  </ul>
                </DisclosureRow>
              ) : null
            })}
          </div>
        )}
      </AgentQueryBody>
    </AgentPage>
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
    <DisclosureRow title={plan.label} detail={summary} aside={plan.carrierCode}>
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
    </DisclosureRow>
  )
}

function planSummary(plan: InsurancePlanRule) {
  if (plan.outcome === "accepted") return plan.allowedProviders.join(", ")
  if (plan.outcome === "needs_staff_task")
    return plan.requirements.length > 0
      ? plan.requirements.map((item) => insuranceRequirementLabels[item]).join(", ")
      : "Pending on the sheet"
  if (plan.acceptedAt.length > 0)
    return `${list.format(plan.acceptedAt)} ${plan.acceptedAt.length === 1 ? "takes it" : "take it"}`
  return plan.callerNotice ?? ""
}
