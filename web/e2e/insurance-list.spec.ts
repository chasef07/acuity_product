import { randomUUID } from "node:crypto"
import { expect, test, type Page } from "@playwright/test"
import type { InsuranceCatalog, InsurancePublication, InsuranceRevision } from "../src/lib/api/generated/types.gen"
import { openAgentPage, signInAs } from "./support"

const imported: InsuranceRevision = {
  id: randomUUID(),
  createdAt: "2026-10-30T15:00:00Z",
  createdBy: "operator@abita.test",
  reason: "initial import",
  provenance: "import",
}

function catalog(): InsuranceCatalog {
  return {
    carriers: [
      { id: "car10001", name: "EXAMPLE CHOICE HEALTH" },
      { id: "car10003", name: "ZETA VISION" },
    ],
    regions: [
      {
        id: "spring_hill_crystal_river",
        plans: [
          { id: "example-choice-medical", label: "Example Choice PPO", family: "Example", coverage: "medical", carrierCode: "EXC01", carrierId: "car10001", selfPay: false, aliases: ["Example Choice"] },
          { id: "zeta-vision", label: "Zeta Vision Plan", family: "Zeta", coverage: "routine_vision", carrierCode: "ZET01", carrierId: "car10003", selfPay: false, aliases: [] },
        ],
        tables: [
          {
            id: "spring_hill",
            offices: ["spring_hill"],
            doctors: ["Dr. Rivera", "Dr. Chen"],
            rows: [
              { planId: "example-choice-medical", doctors: { "Dr. Chen": "yes", "Dr. Rivera": "yes" }, requires: [{ kind: "prior_authorization", channel: "availity" }], onlyOffices: [], notice: "", note: "Synthetic sheet note" },
              { planId: "zeta-vision", doctors: { "Dr. Rivera": "yes" }, requires: [], onlyOffices: [], notice: "", note: "" },
            ],
          },
          { id: "crystal_river", offices: ["crystal_river"], doctors: ["Dr. Licht"], rows: [] },
        ],
      },
    ],
  }
}

async function serveInsurance(page: Page, canEdit: boolean) {
  const state = { active: { revision: imported, catalog: catalog() }, publications: [] as InsurancePublication[], answers: [] as ("invalid" | "conflict" | "ok")[] }
  await page.route("**/v1/insurance/revisions/query", (route) =>
    route.fulfill({ json: { ...state.active, officeId: "spring_hill", officeNames: { spring_hill: "Spring Hill", crystal_river: "Crystal River" }, canEdit } }),
  )
  await page.route("**/v1/insurance/revisions", (route) => {
    const publication: InsurancePublication = route.request().postDataJSON()
    state.publications.push(publication)
    const answer = state.answers.shift() ?? "ok"
    if (answer === "invalid") {
      return route.fulfill({
        status: 400,
        json: {
          error: {
            code: "INSURANCE_INVALID",
            message: "The insurance list has problems to fix before it can be published. Nothing changed.",
            correlationId: "synthetic",
            retryable: false,
            errors: [{ region: "spring_hill_crystal_river", table: "", planId: "example-choice-medical", rule: "empty_family", message: "Plan example-choice-medical needs a family." }],
          },
        },
      })
    }
    if (answer === "conflict") {
      const theirs = catalog()
      theirs.regions[0].tables[0].rows[1].note = "Someone else's synthetic note"
      state.active = { revision: { ...imported, id: randomUUID(), createdAt: "2026-10-30T17:00:00Z", createdBy: "other@abita.test", provenance: "portal" }, catalog: theirs }
      return route.fulfill({
        status: 409,
        json: { error: { code: "INSURANCE_CHANGED", message: "Someone else changed the insurance list. Reload to see their changes.", correlationId: "synthetic", retryable: false } },
      })
    }
    const revision: InsuranceRevision = {
      id: publication.revisionId,
      createdAt: "2026-10-30T19:30:00Z",
      createdBy: "selected@abita.test",
      reason: publication.reason,
      provenance: "portal",
      previousRevisionId: publication.expectedRevisionId,
    }
    state.active = { revision, catalog: publication.catalog }
    return route.fulfill({ json: revision })
  })
  return state
}

test("staff without access to every office on the table see the insurance list read-only", async ({ page }, testInfo) => {
  await serveInsurance(page, false)
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Insurance list")
  const plan = page.getByRole("listitem").filter({ hasText: "Example Choice PPO" })
  await plan.getByRole("button", { name: /^Example Choice PPO/ }).click()
  await expect(plan.getByText("EXAMPLE CHOICE HEALTH · EXC01 · car10001")).toBeVisible()
  await expect(page.getByRole("button", { name: "Edit", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Add plan", exact: true })).toHaveCount(0)
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-read-only.png") })
})

test("staff change a doctor and a shared plan field, fix a rejected publish, survive a conflict, and see the new revision", async ({ page }, testInfo) => {
  const state = await serveInsurance(page, true)
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Insurance list")
  await expect(page.locator(`time[datetime="${imported.createdAt}"]`)).toBeVisible()

  const plan = page.getByRole("listitem").filter({ hasText: "Example Choice PPO" })
  await plan.getByRole("button", { name: /^Example Choice PPO/ }).click()
  await plan.getByRole("button", { name: "Edit", exact: true }).click()
  await plan.getByLabel("Dr. Chen", { exact: true }).selectOption("no")
  await expect(page.getByText("1 unpublished change")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-editor-change.png") })

  await plan.getByLabel("Family", { exact: true }).fill("")
  await expect(page.getByRole("status").filter({ hasText: "Also changes Crystal River." })).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-shared-row-notice.png") })

  const publish = page.getByRole("button", { name: "Publish", exact: true })
  await expect(publish).toBeDisabled()
  await page.getByLabel("Why are you changing this?").fill("Dr. Chen left the network")
  state.answers.push("invalid", "conflict")
  await publish.click()
  await expect(plan.getByRole("alert")).toHaveText("Plan example-choice-medical needs a family.")
  await expect(page.getByText("Not published. Fix the problems shown beside each plan, then publish again.")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-rejected.png") })

  await plan.getByLabel("Family", { exact: true }).fill("Example Health")
  await publish.click()
  await expect(page.getByText("Someone else changed the insurance list.")).toBeVisible()
  await expect(plan.getByLabel("Dr. Chen", { exact: true })).toHaveValue("no")
  await expect(page.getByText("by other@abita.test")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-conflict.png") })

  await publish.click()
  await expect(publish).toHaveCount(0)
  await expect(page.locator('time[datetime="2026-10-30T19:30:00Z"]')).toBeVisible()
  await expect(page.getByText("by selected@abita.test")).toBeVisible()
  await expect(plan.getByText("Dr. Chen: No")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-published.png") })

  expect(state.publications).toHaveLength(3)
  const [, conflicted, published] = state.publications
  expect(published.expectedRevisionId).not.toEqual(conflicted.expectedRevisionId)
  expect(published.catalog.regions[0].plans[0].family).toBe("Example Health")
  expect(published.catalog.regions[0].tables[0].rows[0].doctors).toEqual({ "Dr. Chen": "no", "Dr. Rivera": "yes" })
  expect(published.catalog.regions[0].tables[0].rows[1].note).toBe("Someone else's synthetic note")
  expect(errors).toEqual([])
})
