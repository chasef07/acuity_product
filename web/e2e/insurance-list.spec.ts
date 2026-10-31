import { randomUUID } from "node:crypto"
import { expect, test, type Page } from "@playwright/test"
import type { InsuranceCatalog, InsurancePublication, InsuranceRevision, InsuranceValidationError } from "../src/lib/api/generated/types.gen"
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
      { id: "car10001", code: "EXC01", name: "EXAMPLE CHOICE HEALTH" },
      { id: "car10002", code: "EXS01", name: "EXAMPLE SELECT HEALTH" },
      { id: "car10003", code: "ZET01", name: "ZETA VISION" },
      { id: "car10004", code: "SELF", name: "SELF-PAY" },
    ],
    regions: [
      {
        id: "spring_hill_crystal_river",
        plans: [
          { id: "example-choice-medical", label: "Example Choice PPO", family: "Example", coverage: "medical", carrierId: "car10001", aliases: ["Example Choice"] },
          { id: "zeta-vision", label: "Zeta Vision Plan", family: "Zeta", coverage: "routine_vision", carrierId: "car10003", aliases: [] },
          { id: "self-pay-medical", label: "Self-pay", family: "Self-pay", coverage: "medical", carrierId: "car10004", aliases: ["Cash"] },
          { id: "example-select-hmo", label: "Example Select HMO", family: "Example", coverage: "medical", carrierId: "car10002", aliases: ["Select HMO"] },
        ],
        tables: [
          {
            id: "spring_hill",
            offices: ["spring_hill"],
            doctors: ["Dr. Rivera", "Dr. Chen"],
            rows: [
              { planId: "example-choice-medical", doctors: { "Dr. Chen": "yes", "Dr. Rivera": "yes" }, beforeBooking: "prior_authorization", onlyOffices: [], notice: "" },
              { planId: "zeta-vision", doctors: { "Dr. Rivera": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
              { planId: "self-pay-medical", doctors: { "Dr. Chen": "yes", "Dr. Rivera": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
            ],
          },
          {
            id: "crystal_river",
            offices: ["crystal_river"],
            doctors: ["Dr. Licht"],
            rows: [
              { planId: "example-choice-medical", doctors: { "Dr. Licht": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
              { planId: "example-select-hmo", doctors: { "Dr. Licht": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
            ],
          },
        ],
      },
    ],
  }
}

async function serveInsurance(page: Page, canEdit: boolean) {
  const state = {
    active: { revision: imported, catalog: catalog() },
    revisions: [{ revision: imported, catalog: catalog() }],
    publications: [] as InsurancePublication[],
    answers: [] as ("invalid" | "conflict" | "ok")[],
    invalid: [{ region: "spring_hill_crystal_river", table: "", planId: "example-choice-medical", rule: "empty_family", message: "Plan example-choice-medical needs a family." }] as InsuranceValidationError[],
    published: ["2026-10-30T19:30:00Z"],
  }
  await page.route("**/v1/insurance/revisions/history", (route) =>
    route.fulfill({ json: { revisions: state.revisions.map((item) => item.revision).reverse() } }),
  )
  await page.route("**/v1/insurance/revisions/read", (route) => {
    const { revisionId } = route.request().postDataJSON() as { revisionId: string }
    return route.fulfill({ json: state.revisions.find((item) => item.revision.id === revisionId) })
  })
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
            errors: state.invalid,
          },
        },
      })
    }
    if (answer === "conflict") {
      const theirs = catalog()
      theirs.regions[0].tables[0].rows[1].notice = "Someone else's synthetic notice"
      state.active = { revision: { ...imported, id: randomUUID(), createdAt: "2026-10-30T17:00:00Z", createdBy: "other@abita.test", provenance: "portal" }, catalog: theirs }
      return route.fulfill({
        status: 409,
        json: { error: { code: "INSURANCE_CHANGED", message: "Someone else changed the insurance list. Reload to see their changes.", correlationId: "synthetic", retryable: false } },
      })
    }
    const revision: InsuranceRevision = {
      id: publication.revisionId,
      createdAt: state.published.shift() ?? "2026-10-30T19:30:00Z",
      createdBy: "selected@abita.test",
      reason: publication.reason,
      provenance: publication.provenance ?? "portal",
      previousRevisionId: publication.expectedRevisionId,
    }
    state.active = { revision, catalog: publication.catalog }
    state.revisions.push(state.active)
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
  await expect(plan.getByText("EXAMPLE CHOICE HEALTH (EXC01)")).toBeVisible()
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

  await plan.getByLabel("Company", { exact: true }).fill("")
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

  await plan.getByLabel("Company", { exact: true }).fill("Example Health")
  await publish.click()
  await expect(page.getByText("Someone else changed the insurance list.")).toBeVisible()
  await expect(plan.getByLabel("Dr. Chen", { exact: true })).toHaveValue("no")
  await expect(page.getByText("by other@abita.test")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-conflict.png") })

  await publish.click()
  await expect(publish).toHaveCount(0)
  await expect(page.locator('time[datetime="2026-10-30T19:30:00Z"]')).toBeVisible()
  await expect(page.getByText("by selected@abita.test")).toBeVisible()
  await expect(plan.getByText(/Dr\. Chen: No/)).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-published.png") })

  expect(state.publications).toHaveLength(3)
  const [, conflicted, published] = state.publications
  expect(published.expectedRevisionId).not.toEqual(conflicted.expectedRevisionId)
  expect(published.catalog.regions[0].plans[0].family).toBe("Example Health")
  expect(published.catalog.regions[0].tables[0].rows[0].doctors).toEqual({ "Dr. Chen": "no", "Dr. Rivera": "yes" })
  expect(published.catalog.regions[0].tables[0].rows[1].notice).toBe("Someone else's synthetic notice")
  expect(errors).toEqual([])
})

test("staff publish a change, open History, have a restore rejected, then restore the earlier revision and see the list as it was", async ({ page }, testInfo) => {
  const state = await serveInsurance(page, true)
  state.published = ["2026-10-30T19:30:00Z", "2026-10-30T20:15:00Z"]
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Insurance list")

  const zeta = page.getByRole("listitem").filter({ hasText: "Zeta Vision Plan" })
  await zeta.getByRole("button", { name: /^Zeta Vision Plan/ }).click()
  await zeta.getByRole("button", { name: "Remove", exact: true }).click()
  await page.getByLabel("Why are you changing this?").fill("Zeta Vision left the network")
  await page.getByRole("button", { name: "Publish", exact: true }).click()
  await expect(page.locator('time[datetime="2026-10-30T19:30:00Z"]')).toBeVisible()
  await expect(page.getByRole("button", { name: /^Zeta Vision Plan/ })).toHaveCount(0)

  await page.getByRole("button", { name: "History", exact: true }).click()
  const history = page.getByRole("dialog", { name: "History" })
  const rows = history.getByRole("listitem")
  await expect(rows).toHaveCount(2)
  await expect(rows.nth(0)).toContainText(/Portal edit.*Active.*selected@abita\.test.*Zeta Vision left the network/)
  await expect(rows.nth(1)).toContainText(/Imported.*operator@abita\.test.*initial import/)
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-history-list.png") })

  await rows.nth(1).getByRole("button").click()
  await expect(history.getByText("Restoring this version would make these changes to the active list for Spring Hill.")).toBeVisible()
  await expect(history.getByRole("region", { name: "This office's rows" }).getByRole("listitem")).toHaveText(["AddedZeta Vision Plan"])
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-history-diff.png") })

  await history.getByRole("button", { name: "Restore this version" }).click()
  state.answers.push("invalid")
  state.invalid = [{ region: "spring_hill_crystal_river", table: "spring_hill", planId: "zeta-vision", rule: "unknown_doctor_column", message: "Dr. Rivera is not a doctor column on the spring_hill table." }]
  await history.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(history.getByRole("alert")).toContainText("Not restored. This version no longer passes the insurance list checks. Nothing changed.")
  await expect(history.getByRole("alert")).toContainText("Dr. Rivera is not a doctor column on the spring_hill table.")
  expect(state.revisions).toHaveLength(2)
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-restore-rejected.png") })

  await history.getByRole("button", { name: "Restore", exact: true }).click()
  await expect(rows).toHaveCount(3)
  await expect(rows.nth(0)).toContainText(new RegExp(`Restore.*Active.*selected@abita\\.test.*Restore ${imported.id.slice(0, 8)}`))
  await page.keyboard.press("Escape")
  await expect(history).toHaveCount(0)
  await expect(page.locator('time[datetime="2026-10-30T20:15:00Z"]')).toBeVisible()
  await expect(page.getByRole("button", { name: /^Zeta Vision Plan/ })).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("insurance-restored.png") })

  const restore = state.publications.at(-1)!
  expect(state.publications).toHaveLength(3)
  expect(restore.provenance).toBe("restore")
  expect(restore.reason).toBe(`Restore ${imported.id.slice(0, 8)}`)
  expect(restore.catalog).toEqual(catalog())
  expect(restore.expectedRevisionId).toBe(state.revisions[1].revision.id)
  expect(errors).toEqual([])
})

test("staff read and edit a plan in two sections, keep self-pay fixed, add a region plan from search, and create a new plan once its form is complete", async ({ page }, testInfo) => {
  const state = await serveInsurance(page, true)
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await page.setViewportSize({ width: 1280, height: 1600 })
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Insurance list")
  await expect(page.getByRole("group", { name: "Medical" })).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-grouped-list.png") })

  const plan = page.getByRole("listitem").filter({ hasText: "Example Choice PPO" })
  await plan.getByRole("button", { name: /^Example Choice PPO/ }).click()
  await expect(plan.getByText("Before booking")).toBeVisible()
  await expect(plan.getByText("Prior auth")).toBeVisible()
  await plan.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-plan-read.png") })

  await plan.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(plan.getByRole("heading", { name: "This plan" })).toBeVisible()
  await expect(plan.getByRole("heading", { name: "At Spring Hill" })).toBeVisible()
  await expect(plan.getByText("Shared with Crystal River, so changes here apply there too.")).toBeVisible()
  await plan.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-plan-edit.png") })
  await plan.getByRole("button", { name: "Done", exact: true }).click()

  const selfPay = page.getByRole("listitem").filter({ hasText: /^Self-pay/ })
  await selfPay.getByRole("button", { name: /^Self-pay/ }).click()
  await expect(selfPay.getByRole("button", { name: "Remove", exact: true })).toHaveCount(0)
  await selfPay.getByRole("button", { name: "Edit", exact: true }).click()
  await expect(selfPay.getByText("so this plan's details can't be changed.")).toBeVisible()
  await expect(selfPay.getByLabel("Plan name")).toHaveCount(0)
  await selfPay.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-self-pay.png") })
  await selfPay.getByRole("button", { name: "Done", exact: true }).click()

  await page.getByRole("button", { name: "Add plan", exact: true }).click()
  const add = page.getByRole("region", { name: "Add plan" })
  await add.getByLabel("Find a plan").fill("select")
  await expect(add.getByRole("list", { name: "Matching plans" }).getByRole("button")).toHaveText(["Example Select HMOExample · Medical"])
  await add.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-add-plan-search.png") })
  await add.getByRole("button", { name: /^Example Select HMO/ }).click()
  const added = page.getByRole("listitem").filter({ hasText: "Example Select HMO" })
  await expect(added.getByRole("heading", { name: "At Spring Hill" })).toBeVisible()
  await added.getByLabel("Dr. Chen", { exact: true }).selectOption("yes")

  await page.getByRole("button", { name: "Add plan", exact: true }).click()
  await page.getByRole("region", { name: "Add plan" }).getByLabel("Find a plan").fill("Acme Vision")
  await page.getByRole("button", { name: "Create new plan" }).click()
  const form = page.getByRole("region", { name: "Create new plan" })
  const submit = form.getByRole("button", { name: "Add", exact: true })
  await form.getByLabel("Dr. Rivera", { exact: true }).selectOption("yes")
  await expect(submit).toBeDisabled()
  await expect(form.getByText("To add this plan, enter the company, choose the visit type, and choose the AMD carrier, because a doctor takes this plan.")).toBeVisible()
  await form.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-create-plan-disabled.png") })
  await form.getByLabel("Company").fill("Acme")
  await form.getByLabel("Visit type").selectOption("routine_vision")
  await form.getByLabel("AMD carrier").selectOption("car10003")
  await form.getByLabel("Note for callers").fill("Bring your Acme Vision card.")
  await expect(submit).toBeEnabled()
  await form.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-create-plan-valid.png") })
  await submit.click()

  await page.getByLabel("Why are you changing this?").fill("Add Example Select and Acme Vision")
  await page.getByRole("button", { name: "Publish", exact: true }).click()
  await expect(page.locator('time[datetime="2026-10-30T19:30:00Z"]')).toBeVisible()
  const published = state.publications.at(-1)!
  expect(published.catalog.regions[0].plans.at(-1)).toEqual({ id: "acme-vision", label: "Acme Vision", family: "Acme", coverage: "routine_vision", carrierId: "car10003", aliases: [] })
  expect(published.catalog.regions[0].tables[0].rows.slice(-2)).toEqual([
    { planId: "example-select-hmo", doctors: { "Dr. Chen": "yes" }, beforeBooking: "none", onlyOffices: [], notice: "" },
    { planId: "acme-vision", doctors: { "Dr. Rivera": "yes", "Dr. Chen": "no" }, beforeBooking: "none", onlyOffices: [], notice: "Bring your Acme Vision card." },
  ])

  await page.getByRole("button", { name: "History", exact: true }).click()
  const history = page.getByRole("dialog", { name: "History" })
  await history.getByRole("listitem").nth(1).getByRole("button").click()
  await expect(history.getByRole("region", { name: "This office's rows" }).getByRole("listitem")).toHaveText(["RemovedExample Select HMO", "RemovedAcme Vision"])
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("v2-history-diff.png") })
  expect(errors).toEqual([])
})
