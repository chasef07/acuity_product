import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const fixtureURL = process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"

test("visible workload filters show message previews and completion opens the next task", async ({ page }) => {
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
  const workload = page.getByLabel("Open workload")
  await workload.getByRole("button", { name: /^All work/ }).click()
  await expect(page.getByTestId("task-row").first()).toContainText(`${key} urgent`)
  await page.getByRole("button", { name: `${key} urgent`, exact: true }).click()
  await page.getByRole("button", { name: "Complete & next", exact: true }).click()
  await expect(page.getByRole("heading", { name: `${key} normal`, exact: true })).toBeVisible()
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
  await workload.getByRole("button", { name: /^Calls & voicemail/ }).click()
  await expect(page.getByTestId("task-row").filter({ hasText: "Synthetic latest text preview" })).toHaveCount(0)
})
