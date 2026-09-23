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
    html.indexOf("Physician visit copayment") <
      html.indexOf("General plan benefits"),
  )
  assert.equal(document.querySelector("table")?.closest("details"), null)
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
  assert.ok(html.includes("Physician visit copayment not returned."))
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
    benefits: [{ code: "B", name: "Co-payment", serviceTypeCodes: ["98"], benefitAmount: "25", procedureId: "synthetic-procedure", healthCareServiceDelivery: [{ placeOfService: "Office" }], benefitDateInformation: { benefitBegin: "20260101" }, authorizationOrCertificationIndicator: "Yes", additionalInformation: [{ description: "Specific visit", futureQualifier: "retained" }] }],
  }]} />)
  const document = new JSDOM(html).window.document
  const row = document.querySelector("tbody tr")!
  for (const value of ["synthetic-procedure", "Office", "20260101", "Yes"]) {
    const textNodes = [...row.querySelectorAll("span")].filter((node) => node.textContent === value)
    assert.ok(textNodes.some((node) => !node.closest("details:not([open])")), value)
  }
  for (const value of ["date of birth conflict", "synthetic-check-1", "synthetic-search-1", "retained"]) assert.ok(html.includes(value), value)
})


test("only the exact booked doctor and intake are prominent, including a failed check", () => {
  const provider = (name: string, key: string, status: AiEligibilityCheck["status"] = "active"): AiEligibilityCheck => ({ ...check, providerCheck: true, providerName: name, providerNpi: "synthetic-npi", status, appointmentReviewKeys: key ? [key] : [] })
  const checks = [provider("Doctor A", ""), provider("Doctor B", "event-a", "unknown"), provider("Doctor C", "event-b")]
  const document = new JSDOM(renderToStaticMarkup(<EligibilitySummary checks={checks} appointmentReviewKey="event-a" />)).window.document
  for (const name of ["Doctor A", "Doctor C"]) {
    const node = [...document.querySelectorAll("p")].find((node) => node.textContent === name)!
    assert.ok(node.closest("details:not([open])"), name)
  }
  const booked = [...document.querySelectorAll("p")].find((node) => node.textContent === "Doctor B")!
  assert.equal(booked.closest("details"), null)
  assert.ok(booked.parentElement?.parentElement?.textContent?.includes("Unable to confirm coverage"))
  assert.ok(document.body.textContent?.includes("provider tier"))
  const missing = renderToStaticMarkup(<EligibilitySummary checks={checks} appointmentReviewKey="event-missing" />)
  assert.ok(missing.includes("Booked provider eligibility needs verification"))
})

test("appointment never substitutes legacy evidence or silently hides absent linked checks", () => {
  for (const checks of [undefined, [], [check]]) {
    const document = new JSDOM(renderToStaticMarkup(<EligibilitySummary checks={checks} appointmentReviewKey="event-a" />)).window.document
    assert.ok(document.body.textContent?.includes("Booked provider eligibility needs verification"))
    const active = [...document.querySelectorAll("span")].find((node) => node.textContent?.includes("Active coverage"))
    if (active) assert.ok(active.closest("details:not([open])"))
  }
})

test("explicit specialist copay rows precede PCP without becoming an unqualified price", () => {
  const document = new JSDOM(renderToStaticMarkup(<EligibilitySummary checks={[{
    ...check, providerCheck: true,
    benefits: [
      { code: "B", name: "Co-payment", serviceTypeCodes: ["98"], benefitAmount: "40", additionalInformation: [{ description: "Primary care visit" }] },
      { code: "B", name: "Co-payment", serviceTypeCodes: ["98"], benefitAmount: "80", additionalInformation: [{ description: "Specialist visit · designated tier" }] },
    ],
  }]} />)).window.document
  const first = document.querySelector("tbody tr")!
  assert.ok(first.textContent?.includes("$80.00"))
  assert.ok(first.textContent?.includes("designated tier"))
  assert.ok(document.body.textContent?.includes("Specialist copay needs verification"))
})


test("physician copayment and qualifiers stay outside collapsed details without duplicate rows", () => {
  const document = new JSDOM(renderToStaticMarkup(<EligibilitySummary checks={[check]} />)).window.document
  const copayment = document.querySelector('section[aria-label="Physician visit copayment"]')!
  assert.equal(copayment.closest("details"), null)
  assert.ok(copayment.textContent?.includes("$0.00"))
  for (const value of ["$0.00", "specific provider tier"]) {
    const matching = [...copayment.querySelectorAll("p, li")].filter((node) => node.textContent?.includes(value))
    assert.ok(matching.some((node) => node.closest("details") === null), value)
  }
  const other = [...document.querySelectorAll("details")].find((node) => node.querySelector("summary")?.textContent === "Other physician office-visit benefits")!
  assert.equal(other.open, false)
  assert.ok(other.textContent?.includes("20%"))
  assert.ok(!other.textContent?.includes("$0.00"))
})
