import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { JSDOM } from "jsdom"
import { EligibilitySummary } from "./eligibility-summary.tsx"
import type { AiEligibilityCheck } from "../../lib/api/generated/types.gen.ts"

const check: AiEligibilityCheck = {
  status: "active",
  patientName: "Jane Example",
  submittedName: "Ane Example",
  plan: "Example Health",
  planName: "Example Choice",
  memberIdLast4: "4821",
  checkedAt: "2026-09-23T12:00:00Z",
  reason: "",
  benefits: [
    {
      code: "C",
      name: "Deductible",
      serviceTypeCodes: ["30"],
      benefitAmount: "1500",
      timeQualifier: "Remaining",
      coverageLevel: "Individual",
      inPlanNetworkIndicator: "Yes",
    },
    {
      code: "B",
      name: "Co-payment",
      serviceTypeCodes: ["98", "AL"],
      benefitAmount: "0",
      inPlanNetworkIndicator: "Yes",
      additionalInformation: [
        { description: "Specialist visit · specific provider tier" },
      ],
    },
    {
      code: "A",
      name: "Co-insurance",
      serviceTypeCodes: ["98"],
      benefitPercent: "0.2",
    },
  ],
}

test("office visits lead general benefits and retain amounts, qualifiers, and corrections", () => {
  const html = renderToStaticMarkup(<EligibilitySummary checks={[check]} />)
  const document = new JSDOM(html).window.document
  assert.ok(
    html.indexOf("Physician office-visit benefits") <
      html.indexOf("General plan benefits"),
  )
  assert.equal(document.querySelector("details")?.open, true)
  for (const value of [
    "Active coverage",
    "$0.00",
    "20%",
    "$1,500.00",
    "Remaining",
    "In network",
    "Individual",
    "specific provider tier",
    "Ane Example",
    "Jane Example",
  ])
    assert.ok(html.includes(value), value)
})

test("missing office benefits remain explicit, never inferred from active plan status", () => {
  const html = renderToStaticMarkup(
    <EligibilitySummary
      checks={[{ ...check, benefits: [check.benefits[0]] }]}
    />,
  )
  assert.ok(html.includes("Office-visit benefits not returned."))
  assert.ok(!html.includes("Co-payment"))
})

test("multiple patients and unsuccessful newer checks keep their own status", () => {
  const html = renderToStaticMarkup(
    <EligibilitySummary
      checks={[
        check,
        {
          ...check,
          patientName: "John Other",
          status: "unavailable",
          benefits: [],
          reason: "request_failed",
        },
      ]}
    />,
  )
  assert.ok(html.indexOf("John Other") < html.indexOf("Jane Example"))
  assert.ok(html.includes("Check unavailable"))
  assert.ok(html.includes("Active coverage"))
  const review = renderToStaticMarkup(
    <EligibilitySummary
      checks={[{ ...check, status: "review", reason: "identity_uncertain" }]}
    />,
  )
  assert.ok(review.includes("Needs review"))
  assert.ok(!review.includes("Active coverage"))
})


test("applicability and identity review evidence stay visible with the benefit", () => {
  const html = renderToStaticMarkup(<EligibilitySummary checks={[{
    ...check, status: "review", reason: "identity_uncertain",
    identityReasons: ["date_of_birth_conflict"], checkId: "synthetic-check-1", eligibilitySearchId: "synthetic-search-1",
    benefits: [{ name: "Co-payment", serviceTypeCodes: ["98"], benefitAmount: "25", procedureId: "synthetic-procedure", healthCareServiceDelivery: [{ placeOfService: "Office" }], benefitDateInformation: { benefitBegin: "20260101" }, authorizationOrCertificationIndicator: "Yes", additionalInformation: [{ description: "Specific visit", futureQualifier: "retained" }] }],
  }]} />)
  const document = new JSDOM(html).window.document
  const row = document.querySelector("tbody tr")!
  for (const value of ["synthetic-procedure", "Office", "20260101", "Yes"]) {
    const textNodes = [...row.querySelectorAll("span")].filter((node) => node.textContent === value)
    assert.ok(textNodes.some((node) => !node.closest("details:not([open])")), value)
  }
  for (const value of ["date of birth conflict", "synthetic-check-1", "synthetic-search-1", "retained"]) assert.ok(html.includes(value), value)
})
