import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const fixtureURL = process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"

test("visible workload filters show message previews and completion opens the next task", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  const key = `shift-${Date.now()}`
  for (const [suffix, urgency, phone] of [["normal", "normal", "+12025550201"], ["urgent", "high_priority", "+12025550202"]] as const) {
    const result = await page.request.post(`${portalURL}/v1/tasks`, { headers: { authorization: "Bearer synthetic-service-token" }, data: {
      callId: `${key}-${suffix}`, callerPhone: phone, category: "optical", idempotencyKey: `${key}-${suffix}`,
      officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency,
      summary: `${key} ${suffix}`, message: "Synthetic shift work.",
    } })
    expect(result.ok()).toBeTruthy()
  }
  const search = page.getByLabel("Search tasks, names, or phone")
  await search.fill(key)
  await search.press("Enter")
  const workload = page.getByLabel("Workspace folders")
  await workload.getByRole("button", { name: /^My Tasks/ }).click()
  await expect(page.getByTestId("task-row").first()).toContainText(`${key} urgent`)
  await page.getByRole("button", { name: `${key} urgent`, exact: true }).click()
  await page.getByRole("button", { name: "Complete & next", exact: true }).click()
  await expect(page.getByRole("heading", { name: `${key} normal`, exact: true })).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath("compact-workspace.png"), fullPage: true })
  await search.fill("")
  await search.press("Enter")
  for (const [index, text] of ["Synthetic first question", "Synthetic latest text preview"].entries()) {
    const result = await page.request.post(`${fixtureURL}/fixture/message-inbound`, { headers: { authorization: "Bearer fixture-control" }, data: {
      eventId: `${key}-text-${index}`, providerMessageId: `${key}-provider-${index}`, from: "+12025550203", to: "+17275550101", text,
    } })
    expect(result.ok()).toBeTruthy()
  }
  await workload.getByRole("button", { name: /^Texts/ }).click()
  await expect(page.getByTestId("task-row").filter({ hasText: "(202) 555-0203" })).toContainText("Synthetic latest text preview")
  await expect(page.getByTestId("task-row").filter({ hasText: key })).toHaveCount(0)
  await workload.getByRole("button", { name: /^Missed Calls & Voicemails/ }).click()
  await expect(page.getByTestId("task-row").filter({ hasText: "Synthetic latest text preview" })).toHaveCount(0)
})

test("Texts ages out after five days without completing work and new inbound restores it", async ({ page }) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await page.clock.install()
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  const key = `text-age-${Date.now()}`
  const phone = "+12025550209"
  const inbound = async (suffix: string, occurredAt: string) => {
    const result = await page.request.post(`${fixtureURL}/fixture/message-inbound`, {
      headers: { authorization: "Bearer fixture-control" },
      data: { eventId: `${key}-${suffix}`, providerMessageId: `${key}-provider-${suffix}`, from: phone, to: "+17275550101", text: `Synthetic ${suffix} question`, occurredAt },
    })
    expect(result.ok()).toBeTruthy()
  }
  await inbound("old", new Date(Date.now() - 6 * 24 * 60 * 60 * 1000).toISOString())
  const search = page.getByLabel("Search tasks, names, or phone")
  await search.fill("5550209")
  await search.press("Enter")
  const folders = page.getByLabel("Workspace folders")
  const texts = folders.getByRole("button", { name: /^Texts/ })
  await texts.click()
  await expect(texts).toHaveText("Texts0")
  await expect(page.getByTestId("task-row")).toHaveCount(0)
  await folders.getByRole("button", { name: /^My Tasks/ }).click()
  await expect(page.getByTestId("task-row")).toHaveCount(1)
  await texts.click()

  const expiresAt = Date.now() + 15_000
  await inbound("near-boundary", new Date(expiresAt - 120 * 60 * 60 * 1000).toISOString())
  await expect(page.getByTestId("task-row")).toContainText("Synthetic near-boundary question")
  await expect(texts).toHaveText("Texts1")
  await expect.poll(() => Date.now() > expiresAt, { timeout: 20_000 }).toBe(true)
  await page.clock.fastForward(61_000)
  await expect(page.getByTestId("task-row")).toHaveCount(0)
  await expect(texts).toHaveText("Texts0")
  await inbound("new", new Date().toISOString())
  await expect(page.getByTestId("task-row")).toContainText("Synthetic new question")
  await expect(texts).toHaveText("Texts1")
})
