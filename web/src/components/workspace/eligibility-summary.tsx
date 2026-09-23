import type { AiEligibilityCheck } from "@/lib/api/generated/types.gen"

type Benefit = Record<string, unknown>
const text = (value: unknown) => (typeof value === "string" ? value : "")
const strings = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.filter((v): v is string => typeof v === "string")
    : []
const codes = (row: Benefit) => strings(row.serviceTypeCodes)
// Ordering is only a reading aid; it does not establish the applicable copay.
const mentionsSpecialistCopay = (row: Benefit) => row.code === "B" && (
  Array.isArray(row.additionalInformation) && row.additionalInformation.some((item) =>
    item && typeof item === "object" && "description" in item &&
    /\bspecialist\b/i.test(text(item.description)) && !/\b(?:non[ -]?|not a )specialist\b/i.test(text(item.description)),
  )
)
const labels: Record<AiEligibilityCheck["status"], string> = {
  active: "Active coverage",
  inactive: "Inactive coverage reported",
  review: "Needs review",
  unknown: "Unable to confirm coverage",
  unavailable: "Check unavailable",
  pending: "Check pending",
}
const reasons: Record<string, string> = {
  identity_uncertain: "Patient details need review.",
  subscriber_details_required: "Subscriber details are needed.",
  payer_rejected: "The payer could not validate the submitted details.",
  payer_eligibility_not_supported:
    "Eligibility checks are not supported for this plan.",
  provider_not_configured: "Eligibility is not configured for this Location.",
  general_coverage_unknown: "The payer did not confirm general plan coverage.",
  request_failed: "The eligibility request failed.",
  unverified_response: "No verified eligibility response was received.",
}

function checkedAt(value: string) {
  const date = new Date(value)
  return value && Number.isFinite(date.getTime())
    ? date.toLocaleString("en-US", {
        month: "short",
        day: "numeric",
        year: "numeric",
        hour: "numeric",
        minute: "2-digit",
      })
    : "Time not recorded"
}

export function EligibilitySummary({
  checks,
  appointmentReviewKey,
}: {
  checks?: AiEligibilityCheck[]
  appointmentReviewKey?: string
}) {
  if (!checks?.length && !appointmentReviewKey) return null
  const selected = (check: AiEligibilityCheck) => !appointmentReviewKey || check.appointmentReviewKeys?.includes(appointmentReviewKey)
  const primary = (checks ?? []).filter(selected).toReversed()
  const other = (checks ?? []).filter((check) => !selected(check)).toReversed()
  return (
    <section aria-label="Insurance eligibility" className="border-t px-5 py-4">
      <h3 className="mb-3 text-sm font-semibold">Insurance</h3>
      {appointmentReviewKey && primary.length === 0 && (
        <p className="text-sm">Booked provider eligibility needs verification. No check is linked to this appointment’s provider.</p>
      )}
      {primary.map((check, index) => <CheckDetails key={index} check={check} />)}
      {other.length > 0 && (
        <details className="mt-4 border-t pt-3">
          <summary className="cursor-pointer text-xs font-medium">Other provider and intake checks</summary>
          <p className="mt-2 text-xs text-muted-foreground">These checks do not establish benefits for this appointment’s provider.</p>
          {other.map((check, index) => <CheckDetails key={index} check={check} />)}
        </details>
      )}
    </section>
  )
}

function CheckDetails({ check }: { check: AiEligibilityCheck }) {
  const officeBenefits = check.benefits.filter((row) => codes(row).includes("98"))
  const copayments = officeBenefits.filter((row) => row.code === "B").toSorted(
    (a, b) => Number(mentionsSpecialistCopay(b)) - Number(mentionsSpecialistCopay(a)),
  )
  const statusColor = check.status === "active"
    ? "text-emerald-700 dark:text-emerald-400"
    : check.status === "review" || check.status === "inactive"
      ? "text-amber-700 dark:text-amber-400"
      : "text-muted-foreground"
  return (
    <div className="mt-4 border-t pt-4 first:mt-0 first:border-0 first:pt-0">
      {check.providerCheck && (
        <div className="mb-3">
          <p className="text-sm font-medium">{check.providerName || "Provider not identified"}</p>
          {check.providerNpi && <p className="text-xs text-muted-foreground">NPI {check.providerNpi}</p>}
        </div>
      )}
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="text-sm font-medium break-words">
                {check.plan || "Insurance plan"}
              </p>
              {check.planName && (
                <p className="mt-1 text-xs text-muted-foreground break-words">
                  {check.planName}
                </p>
              )}
            </div>
            <span
              className={`max-w-[48%] shrink-0 text-right text-xs font-medium ${statusColor}`}
            >
              {check.status === "active" && <span aria-hidden="true">✓ </span>}
              {labels[check.status]}
            </span>
          </div>
          <p className="mt-2 text-xs break-words">
            {check.patientName || "Patient name not recorded"}
            {check.memberIdLast4 &&
              ` · Member ID ending ${check.memberIdLast4}`}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {check.checkedAt
              ? `Checked ${checkedAt(check.checkedAt)}`
              : "Check time not recorded"}
          </p>
          {check.reason && (
            <p className="mt-2 text-xs leading-5">
              {reasons[check.reason] ||
                "Review the check details; coverage could not be fully verified."}
            </p>
          )}
          {check.status !== "active" && check.benefits.length > 0 && (
            <p className="mt-2 text-xs text-muted-foreground">
              Returned benefits are evidence; they do not resolve this check’s
              status.
            </p>
          )}
          {check.providerCheck && officeBenefits.length > 0 && (
            <p className="mt-3 text-xs text-muted-foreground">
              Specialist copay needs verification. Review the listed service,
              network, and provider tier; active coverage alone does not establish
              the applicable amount.
            </p>
          )}
          <section aria-label="Physician visit copayment" className="mt-4 border-t pt-3">
            <h4 className="text-xs font-medium">Physician visit copayment</h4>
            <BenefitTable
              title="Physician visit copayment"
              rows={copayments}
              empty="Physician visit copayment not returned."
            />
          </section>
          <BenefitGroup
            title="Other physician office-visit benefits"
            rows={officeBenefits.filter((row) => row.code !== "B")}
          />
          <BenefitGroup
            title="General plan benefits"
            rows={check.benefits.filter((row) => codes(row).includes("30"))}
            empty="General plan benefits not returned."
          />
          <BenefitGroup
            title="Other returned benefits"
            rows={check.benefits.filter(
              (row) => !codes(row).includes("98") && !codes(row).includes("30"),
            )}
          />
          <details className="mt-3 text-xs">
            <summary className="cursor-pointer font-medium focus-visible:outline-2 focus-visible:outline-offset-4">
              Check details
            </summary>
            <dl className="mt-2 space-y-2 text-muted-foreground">
              <div>
                <dt>Submitted name</dt>
                <dd className="text-foreground">
                  {check.submittedName || "Not recorded"}
                </dd>
              </div>
              {check.patientName !== check.submittedName && (
                <div>
                  <dt>Payer-returned name</dt>
                  <dd className="text-foreground">{check.patientName}</dd>
                </div>
              )}
              {check.identityReasons?.length ? <div>
                <dt>Patient matching</dt>
                <dd className="text-foreground">
                  {check.identityReasons.map((reason) => reason.replaceAll("_", " ")).join(" · ")}
                </dd>
              </div> : null}
              {check.checkId && <div><dt>Check ID</dt><dd className="break-all">{check.checkId}</dd></div>}
              {check.eligibilitySearchId && <div><dt>Eligibility reference</dt><dd className="break-all">{check.eligibilitySearchId}</dd></div>}
              <div>
                <dt>Source</dt>
                <dd>
                  Eligibility checked during this call. Each check retains its
                  own submitted patient details.
                </dd>
              </div>
              {check.reason && (
                <div>
                  <dt>Result reason</dt>
                  <dd className="break-words">
                    {check.reason.replaceAll("_", " ")}
                  </dd>
                </div>
              )}
            </dl>
          </details>
          <p className="mt-3 text-[11px] leading-4 text-muted-foreground">
            Coverage is as of the check date. Benefits depend on the listed
            service, network, and plan conditions.
          </p>
    </div>
  )
}

function BenefitGroup({
  title,
  rows,
  empty,
}: {
  title: string
  rows: Benefit[]
  empty?: string
}) {
  if (!rows.length && !empty) return null
  return (
    <details className="mt-4 border-t pt-3">
      <summary className="cursor-pointer text-xs font-medium focus-visible:outline-2 focus-visible:outline-offset-4">
        {title}
      </summary>
      <BenefitTable title={title} rows={rows} empty={empty} />
    </details>
  )
}

function BenefitTable({ title, rows, empty }: {
  title: string
  rows: Benefit[]
  empty?: string
}) {
  if (!rows.length) return <p className="mt-3 text-xs text-muted-foreground">{empty}</p>
  return (
    <table className="mt-3 w-full table-fixed text-left text-xs">
      <caption className="sr-only">{title}</caption>
      <thead>
        <tr className="text-muted-foreground">
          <th className="w-[34%] pb-2 pr-3 font-normal">Benefit</th>
          <th className="pb-2 font-normal">Returned details</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row, index) => <BenefitRow key={index} row={row} />)}
      </tbody>
    </table>
  )
}

function benefitValue(row: Benefit): string {
  const amount = text(row.benefitAmount).trim(),
    percent = text(row.benefitPercent).trim()
  const values: string[] = []
  if (row.code === "B" && !amount && !percent) values.push("Amount not returned")
  if (amount)
    values.push(
      Number.isFinite(Number(amount))
        ? new Intl.NumberFormat("en-US", {
            style: "currency",
            currency: "USD",
          }).format(Number(amount))
        : amount,
    )
  if (percent)
    values.push(
      Number.isFinite(Number(percent))
        ? `${Number((Number(percent) * 100).toFixed(4))}%`
        : percent,
    )
  const period = text(row.timeQualifier)
  return [...values, period].filter(Boolean).join(" · ")
}

const applicability = new Set([
  "procedureId", "procedureIdentifier", "procedureCode", "procedureModifier", "procedureModifiers",
  "benefitDateInformation", "eligibilityAdditionalInformation",
  "authorizationOrCertificationIndicator", "healthCareServiceDelivery", "quantity", "quantityQualifier",
])

const displayed = new Set([
  "name",
  "code",
  "benefitAmount",
  "benefitPercent",
  "timeQualifier",
  "coverageLevel",
  "inPlanNetworkIndicator",
  "serviceTypes",
])
function BenefitRow({ row }: { row: Benefit }) {
  const messages = Array.isArray(row.additionalInformation)
    ? row.additionalInformation.flatMap((item) =>
        item &&
        typeof item === "object" &&
        "description" in item &&
        typeof item.description === "string"
          ? [item.description]
          : [],
      )
    : []
  const qualifiers = [
    text(row.coverageLevel),
    text(row.inPlanNetworkIndicator) === "Yes"
      ? "In network"
      : text(row.inPlanNetworkIndicator) === "No"
        ? "Out of network"
        : text(row.inPlanNetworkIndicator),
  ].filter(Boolean)
  const remaining = Object.entries(row).filter(
    ([key, value]) =>
      !displayed.has(key) && value !== null && value !== undefined,
  )
  const visibleDetails = remaining.filter(([key]) => applicability.has(key))
  const extraDetails = remaining.filter(([key]) => !applicability.has(key))
  return (
    <tr className="border-t align-top">
      <th scope="row" className="py-3 pr-3 font-medium break-words">
        {text(row.name) || `Benefit ${text(row.code) || "detail"}`}
      </th>
      <td className="py-3 break-words">
        {benefitValue(row) && (
          <p className={row.code === "B" ? "text-base font-semibold tabular-nums" : "font-medium tabular-nums"}>
            {benefitValue(row)}
          </p>
        )}
        {qualifiers.length > 0 && (
          <p className="mt-1 text-muted-foreground">{qualifiers.join(" · ")}</p>
        )}
        {strings(row.serviceTypes).length > 0 && (
          <p className="mt-1 text-muted-foreground">
            {strings(row.serviceTypes).join(" · ")}
          </p>
        )}
        {messages.length > 0 && (
          <ul className="mt-2 space-y-1 leading-5">
            {messages.map((message, index) => (
              <li key={index}>{message}</li>
            ))}
          </ul>
        )}
        {visibleDetails.length > 0 && (
          <dl className="mt-2 space-y-2">
            {visibleDetails.map(([key, value]) => (
              <div key={key}>
                <dt className="text-muted-foreground">{fieldLabel(key)}</dt>
                <dd><ReturnedDetail value={value} /></dd>
              </div>
            ))}
          </dl>
        )}
        {extraDetails.length > 0 && (
          <details className="mt-2">
            <summary className="cursor-pointer text-muted-foreground">
              Additional details
            </summary>
            <dl className="mt-2 space-y-2">
              {extraDetails.map(([key, value]) => (
                <div key={key}>
                  <dt className="text-muted-foreground">{fieldLabel(key)}</dt>
                  <dd className="whitespace-pre-wrap break-words">
                    {<ReturnedDetail value={value} />}
                  </dd>
                </div>
              ))}
            </dl>
          </details>
        )}
      </td>
    </tr>
  )
}

function fieldLabel(key: string) {
  return key
    .replace(/([a-z])([A-Z])/g, "$1 $2")
    .replace(/^./, (letter) => letter.toUpperCase())
}

function ReturnedDetail({ value }: { value: unknown }) {
  if (value === null || value === undefined) return <span>Not returned</span>
  if (Array.isArray(value))
    return (
      <ul className="space-y-1">
        {value.map((item, index) => (
          <li key={index}>
            <ReturnedDetail value={item} />
          </li>
        ))}
      </ul>
    )
  if (typeof value === "object")
    return (
      <dl className="space-y-1">
        {Object.entries(value).map(([key, item]) => (
          <div key={key}>
            <dt className="text-muted-foreground">{fieldLabel(key)}</dt>
            <dd>
              <ReturnedDetail value={item} />
            </dd>
          </div>
        ))}
      </dl>
    )
  return <span>{String(value)}</span>
}
