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
  await expect(page.getByTestId("task-row").first()).toContainText(`${key} urgent`)
  await page.getByRole("button", { name: `${key} normal`, exact: true }).click()
  await page.getByRole("button", { name: `${key} urgent`, exact: true }).click()
  const taskCard = page.getByRole("button", { name: `View task: ${key} urgent`, exact: true })
  const taskPanel = page.getByRole("complementary", { name: "Task context" })
  await expect(taskPanel).toBeVisible()
  await taskCard.click()
  await expect(taskPanel).toBeHidden()
  await expect(page.getByRole("region", { name: "Conversation activity" })).toBeVisible()
  await taskCard.click()
  await expect(taskPanel).toBeVisible()
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
  await page.getByRole("button", { name: "(202) 555-0203", exact: true }).hover()
  await expect(page.getByTestId("rail-hover-details")).toContainText("Synthetic latest text preview")
  await expect(page.getByTestId("task-row").filter({ hasText: key })).toHaveCount(1)
  await workload.getByRole("button", { name: /^Missed Calls & Voicemails/ }).click()
  await expect(page.getByRole("button", { name: "(202) 555-0203", exact: true })).toBeVisible()
  await search.fill("5550203")
  await search.press("Enter")
  await expect(workload.getByRole("button", { name: /^My Tasks/ })).toHaveText("My Tasks0")
  await page.getByRole("button", { name: "Filter My Tasks", exact: true }).click()
  await page.getByRole("menuitemradio", { name: "All Tasks", exact: true }).click()
  await expect(workload.getByRole("button", { name: /^All Tasks/ })).toHaveText("All Tasks0")
  await expect(page.getByRole("group", { name: "All Tasks", exact: true }).getByTestId("task-row")).toHaveCount(0)
  await expect(page.getByRole("group", { name: "Texts", exact: true }).getByRole("button", { name: "(202) 555-0203", exact: true })).toBeVisible()
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
  const { token } = await (await page.request.get("/api/auth/token")).json()
  const discovery = await (await page.request.get(`${portalURL}/v1/access`, { headers: { authorization: `Bearer ${token}` } })).json()
  // Webhook acceptance precedes worker projection; an empty Texts folder alone
  // does not prove that the aged review has been created and preserved.
  await expect.poll(async () => {
    const history = await page.request.post(`${portalURL}/v1/tasks/query`, { headers: { authorization: `Bearer ${token}` }, data: { practiceId: discovery.practices[0].id, search: "5550209", state: "OPEN" } })
    expect(history.ok()).toBeTruthy()
    return (await history.json()).items.length
  }).toBe(1)

  const expiresAt = Date.now() + 15_000
  await inbound("near-boundary", new Date(expiresAt - 120 * 60 * 60 * 1000).toISOString())
  await expect(page.getByRole("button", { name: "(202) 555-0209", exact: true })).toBeVisible()
  await expect(texts).toHaveText("Texts1")
  await expect.poll(() => Date.now() > expiresAt, { timeout: 20_000 }).toBe(true)
  await page.clock.fastForward(61_000)
  await expect(page.getByTestId("task-row")).toHaveCount(0)
  await expect(texts).toHaveText("Texts0")
  await inbound("new", new Date().toISOString())
  await expect(page.getByRole("button", { name: "(202) 555-0209", exact: true })).toBeVisible()
  await expect(texts).toHaveText("Texts1")
})

test("sidebar folders open independently and remember their state", async ({ page }) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  const folders = page.getByLabel("Workspace folders")
  const tasks = folders.getByRole("button", { name: /^My Tasks/ })
  const texts = folders.getByRole("button", { name: /^Texts/ })
  await tasks.click()
  await expect(tasks).toHaveAttribute("aria-expanded", "false")
  await tasks.click()
  await texts.click()
  await expect(tasks).toHaveAttribute("aria-expanded", "true")
  await expect(texts).toHaveAttribute("aria-expanded", "true")
  await page.reload()
  await expect(tasks).toHaveAttribute("aria-expanded", "true")
  await expect(texts).toHaveAttribute("aria-expanded", "true")
  await texts.click()
  await expect(texts).toHaveAttribute("aria-expanded", "false")
  await expect(tasks).toHaveAttribute("aria-expanded", "true")
  await page.getByRole("button", { name: "Toggle Sidebar", exact: true }).click()
  await expect(page.locator('[data-slot="sidebar"]')).toHaveAttribute("data-state", "collapsed")
  await page.reload()
  await expect(page.locator('[data-slot="sidebar"]')).toHaveAttribute("data-state", "collapsed")
  await page.getByTestId("mounted-workspace").getByRole("button", { name: "Toggle Sidebar", exact: true }).click()
  await expect(tasks).toBeVisible()
})

for (const coverageType of ["medical", "routine_vision"] as const) {
test(`appointment review folder is Spring Hill only and does not inflate My Tasks (${coverageType})`, async ({ page }, testInfo) => {
  const vision = coverageType === "routine_vision"
  const copayTitle = vision ? "Vision exam copayment" : "Physician visit copayment"
  const copayAmount = vision ? "$0.00" : "$35.00"
  const phone = vision ? "+12025550281" : "+12025550280"
  const phoneLabel = vision ? "(202) 555-0281" : "(202) 555-0280"
  const message = vision ? "VISION EXAM" : "Specialist visit · specific provider tier"
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "admin@abita.test", "Synthetic Admin")
  for (const [officeKey, officePhone] of [["spring-hill", "+17275550101"], ["hollywood", "+17275550103"]]) {
    const now = new Date()
    const sourceCallId = `review-scope-${officeKey}-${Date.now()}`
    const batchId = `synthetic-batch-${officeKey}`
    const appointmentOutcome = {
      action: "BOOKED", occurredAt: now.toISOString(), externalPatientId: "synthetic-patient",
      newAppointmentId: sourceCallId,
      bookingResult: { status: "booked", appointmentId: sourceCallId, eligibilityCheckId: batchId, providerProfileId: "doctor-b" },
    }

    const response = await page.request.post(`${portalURL}/v1/ai/interactions`, {
      headers: { authorization: "Bearer synthetic-production-token" },
      data: { kind: "CLOSEOUT", officeKey, officePhone, sourceCallId, callerPhone: phone,
        startedAt: new Date(now.getTime() - 60_000).toISOString(), endedAt: now.toISOString(), status: "COMPLETED",
        summary: "Synthetic appointment booked", closeoutPayload: {
          domainOutcomes: [{ status: "success", evidence: appointmentOutcome }],
          eligibilityChecks: [{
            id: batchId, externalPatientId: "synthetic-patient", status: "complete",
            request: { coverageType, firstName: "Jane", lastName: "Example", plan: "Example Health", memberId: "synthetic-4821" },
            result: { insuranceResolution: { status: "unmapped", plans: ["SILVERELITE"] }, providerResults: ["doctor-a", "doctor-b", "doctor-c"].map((profileId) => ({
              provider: { profileId, firstName: "Synthetic", lastName: profileId, npi: "synthetic-npi" },
              status: "active", checkedAt: now.toISOString(), identity: { status: "exact_name_dob", reviewRequired: false }, providerResponse: {
                benefitsInformation: [
                  { code: "B", name: "Co-payment", serviceTypeCodes: [vision ? "AL" : "98"], serviceTypes: [vision ? "Vision (Optometry)" : "Professional (Physician) Visit - Office"], planCoverage: "SILVERELITE", benefitAmount: profileId === "doctor-b" ? (vision ? "0.00" : "35") : "80", coverageLevel: "Individual", inPlanNetworkIndicator: "Yes", additionalInformation: [{ description: message }] },
                  { code: "C", name: "Deductible", serviceTypeCodes: ["30"], benefitAmount: "1500", coverageLevel: "Individual", timeQualifier: "Remaining", inPlanNetworkIndicator: "Yes" },
                ],
              },
            })) },
          }],
        },
        appointmentOutcome,
      },
    })
    expect([200, 201]).toContain(response.status())
  }
  const search = page.getByLabel("Search tasks, names, or phone")
  await search.fill(phone.slice(-7))
  await search.press("Enter")
  await expect(page.getByRole("button", { name: /^My Tasks/ })).toHaveText("My Tasks0")
  const appointments = page.getByRole("button", { name: /^Appointments/ })
  await expect(appointments).toHaveText("Appointments1")
  await appointments.click()
  await expect(page.getByRole("group", { name: "Appointments", exact: true }).getByTestId("task-row")).toHaveCount(1)
  await page.getByRole("button", { name: "Filter My Tasks", exact: true }).click()
  await expect(page.getByRole("menuitemradio", { name: /^Appointments/ })).toHaveCount(0)
  await expect(page.getByRole("menuitemradio", { name: "Scheduling follow-up 0" })).toHaveCount(0)
  await page.getByRole("menuitemradio", { name: "All Tasks", exact: true }).click()
  await page.getByRole("button", { name: "Filter All Tasks", exact: true }).click()
  await expect(page.getByRole("menuitemradio", { name: "Scheduling follow-up 0" })).toBeVisible()
  await page.getByRole("button", { name: "Filter All Tasks", exact: true }).click()
  await expect(page.getByRole("menu")).toBeHidden()
  await page.getByRole("group", { name: "Appointments", exact: true }).getByTestId("task-row").getByRole("button").first().click()
  await expect(page.getByRole("heading", { name: phoneLabel, exact: true })).toBeVisible()
  await page.mouse.move(700, 50)
  await expect(page.getByTestId("rail-hover-details")).toBeHidden()
  const insurance = page.getByRole("region", { name: "Insurance eligibility" })
  await expect(page.getByRole("button", { name: "Verify & next" })).toBeVisible()
  await expect(insurance).toContainText("Active coverage")
  const mapping = insurance.getByRole("region", { name: "Insurance plan mapping" }).filter({ visible: true })
  await expect(mapping).toContainText("No specific plan mapping found")
  await expect(mapping).toContainText("original accepted insurance selection may be used")
  await expect(mapping).toContainText("SILVERELITE")
  await expect(insurance.getByText("Synthetic doctor-b", { exact: true })).toBeVisible()
  await expect(insurance.getByText("Synthetic doctor-a", { exact: true })).not.toBeVisible()
  await expect(insurance.getByText("Synthetic doctor-c", { exact: true })).not.toBeVisible()
  await expect(insurance).toContainText("Jane Example")
  await expect(insurance.getByRole("table", { name: copayTitle }).filter({ visible: true })).toBeVisible()
  await expect(insurance).toContainText(copayAmount)
  await expect(insurance).toContainText(message)
  await insurance.locator("summary").filter({ hasText: /^General plan benefits$/, visible: true }).click()
  await expect(insurance.getByRole("table", { name: "General plan benefits" }).filter({ visible: true })).toBeVisible()
  await expect(insurance).toContainText("$1,500.00")
  // Closing every expanded detail must not hide the booked doctor's copayment.
  for (const summary of await insurance.locator("details[open] > summary").all()) await summary.click()
  await expect(insurance.getByRole("table", { name: copayTitle })).toBeVisible()
  await expect(insurance.getByText(copayAmount, { exact: true })).toBeVisible()
  await expect(insurance.getByText(message, { exact: true }).filter({ visible: true })).toBeVisible()
  await expect(insurance.getByText("$80.00", { exact: true }).filter({ visible: true })).toHaveCount(0)

  await page.setViewportSize({ width: 1440, height: 1600 })
  await insurance.screenshot({ path: testInfo.outputPath("eligibility-sidebar.png") })
  await page.screenshot({ path: testInfo.outputPath("spring-hill-folder.png"), fullPage: true })
  await page.getByRole("button", { name: "Workspace selector" }).click()
  await page.getByRole("button", { name: "Fixture Location 3", exact: true }).click()
  await expect(page.getByRole("button", { name: /^Appointments/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^All Tasks/ })).toHaveText("All Tasks0")
})
}
