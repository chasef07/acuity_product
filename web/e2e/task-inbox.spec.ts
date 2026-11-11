import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { patientRow, railFilter, railScope, showPatientTask, signInAs } from "./support"

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
  const urgent = patientRow(page, "+12025550202")
  const normal = patientRow(page, "+12025550201")
  await expect(page.getByTestId("patient-row")).toHaveCount(2)
  await expect(page.getByTestId("patient-row").first()).toHaveAttribute("data-phone", "+12025550202")
  await expect(urgent.getByTestId("patient-row-name")).toHaveText("(202) 555-0202")
  await expect(urgent.getByTestId("patient-row-reason")).toHaveText(`${key} urgent`)
  await expect(urgent.getByTestId("patient-row-count")).toHaveCount(0)
  await expect(page.getByTestId("patient-work-footer")).toContainText(/^\d+ open for \d+ patients?/)
  await normal.getByRole("button").click()
  await urgent.getByRole("button").click()
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
  const texter = patientRow(page, "+12025550203")
  await railFilter(page, "texts").click()
  await expect(texter.getByTestId("patient-row-reason")).toHaveText("Synthetic latest text preview")
  await texter.getByRole("button").hover()
  await expect(page.getByTestId("rail-hover-details")).toContainText("Synthetic latest text preview")
  await expect(page.getByTestId("patient-row").filter({ hasText: key })).toHaveCount(0)
  await railFilter(page, "callBack").click()
  await expect(texter).toHaveCount(0)
  await railFilter(page, "all").click()
  await expect(page.getByTestId("patient-row").filter({ hasText: key })).toHaveCount(1)
  await expect(texter).toBeVisible()
  await search.fill("5550203")
  await search.press("Enter")
  for (const scope of ["My Tasks", "Everyone"] as const) {
    await railScope(page, scope).click()
    await expect(railScope(page, scope)).toHaveAttribute("aria-pressed", "true")
    await expect(railFilter(page, "all")).toHaveText("All1")
    await expect(railFilter(page, "texts")).toHaveText("Texts1")
    await expect(railFilter(page, "callBack")).toHaveText("Call back0")
    await expect(page.getByTestId("patient-row")).toHaveCount(1)
    await expect(texter).toBeVisible()
  }
})

test("a text review expires after five days and a new inbound text opens another", async ({ page }) => {
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
  const texts = railFilter(page, "texts")
  await texts.click()
  await expect(texts).toHaveText("Texts0")
  await expect(page.getByTestId("patient-row")).toHaveCount(0)
  const { token } = await (await page.request.get("/api/auth/token")).json()
  const discovery = await (await page.request.get(`${portalURL}/v1/access`, { headers: { authorization: `Bearer ${token}` } })).json()
  await expect.poll(async () => {
    const history = await page.request.post(`${portalURL}/v1/tasks/query`, { headers: { authorization: `Bearer ${token}` }, data: { practiceId: discovery.practices[0].id, search: "5550209", state: "COMPLETED" } })
    expect(history.ok()).toBeTruthy()
    return (await history.json()).items.map((task: { closeReason?: string }) => task.closeReason)
  }).toEqual(["EXPIRED"])

  const expiresAt = Date.now() + 15_000
  await inbound("near-boundary", new Date(expiresAt - 120 * 60 * 60 * 1000).toISOString())
  await expect(patientRow(page, phone).getByTestId("patient-row-name")).toHaveText("(202) 555-0209")
  await expect(texts).toHaveText("Texts1")
  await expect.poll(() => Date.now() > expiresAt, { timeout: 20_000 }).toBe(true)
  await page.clock.fastForward(61_000)
  await expect(page.getByTestId("patient-row")).toHaveCount(0)
  await expect(texts).toHaveText("Texts0")
  await inbound("new", new Date().toISOString())
  await expect(patientRow(page, phone).getByTestId("patient-row-reason")).toHaveText("Synthetic new question")
  await expect(texts).toHaveText("Texts1")
})

test("the sidebar remembers its scope, its filter, and whether it is collapsed", async ({ page }) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await expect(railScope(page, "My Tasks")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "all")).toHaveAttribute("aria-pressed", "true")
  await railScope(page, "Everyone").click()
  await railFilter(page, "texts").click()
  await expect(railScope(page, "Everyone")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "texts")).toHaveAttribute("aria-pressed", "true")
  await page.reload()
  await expect(railScope(page, "Everyone")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "texts")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "all")).toHaveAttribute("aria-pressed", "false")
  await railFilter(page, "all").click()
  await railScope(page, "My Tasks").click()
  await expect(railScope(page, "My Tasks")).toHaveAttribute("aria-pressed", "true")
  await page.getByRole("button", { name: "Work", exact: true }).click()
  await expect(page.locator('[data-slot="sidebar"]')).toHaveAttribute("data-state", "collapsed")
  await page.reload()
  await expect(page.locator('[data-slot="sidebar"]')).toHaveAttribute("data-state", "collapsed")
  await page.getByRole("button", { name: "Work", exact: true }).click()
  await expect(railScope(page, "My Tasks")).toBeVisible()
  await expect(railFilter(page, "all")).toHaveAttribute("aria-pressed", "true")
})

test("the Appointments filter lists Spring Hill reviews only and they do not count as work without an owner", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "admin@abita.test", "Synthetic Admin")
  for (const [officeKey, officePhone] of [["spring-hill", "+17275550101"], ["hollywood", "+17275550103"]]) {
    const now = new Date()
    const sourceCallId = `review-scope-${officeKey}-${Date.now()}`
    const response = await page.request.post(`${portalURL}/v1/ai/interactions`, {
      headers: { authorization: "Bearer synthetic-production-token" },
      data: { kind: "CLOSEOUT", officeKey, officePhone, sourceCallId, callerPhone: "+12025550280",
        startedAt: new Date(now.getTime() - 60_000).toISOString(), endedAt: now.toISOString(), status: "COMPLETED",
        summary: "Synthetic appointment booked", closeoutPayload: { callId: sourceCallId },
        appointmentOutcome: { action: "BOOKED", occurredAt: now.toISOString(), newAppointmentId: sourceCallId, bookingResult: { status: "booked", appointmentId: sourceCallId } },
      },
    })
    expect([200, 201]).toContain(response.status())
  }
  const search = page.getByLabel("Search tasks, names, or phone")
  await search.fill("5550280")
  await search.press("Enter")
  const appointments = railFilter(page, "appointments")
  const caller = patientRow(page, "+12025550280")
  await expect(appointments).toHaveText("Appointments1")
  await expect(railFilter(page, "all")).toHaveText("All1")
  await expect(railFilter(page, "unowned")).toHaveText("No owner0")
  await expect(page.getByTestId("patient-row")).toHaveCount(1)
  await expect(caller.getByTestId("patient-row-count")).toHaveCount(0)
  await showPatientTask(page, "+12025550280")
  await expect(page.getByRole("heading", { name: "(202) 555-0280", exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Verify & next", exact: true })).toBeVisible()
  await appointments.click()
  await expect(appointments).toHaveAttribute("aria-pressed", "true")
  await expect(page.getByTestId("patient-row")).toHaveCount(1)
  await railScope(page, "Everyone").click()
  await expect(appointments).toHaveText("Appointments1")
  await expect(page.getByTestId("patient-row")).toHaveCount(1)
  await railScope(page, "My Tasks").click()
  await expect(page.getByRole("button", { name: "Verify & next", exact: true })).toBeVisible()
  await page.mouse.move(700, 50)
  await expect(page.getByTestId("rail-hover-details")).toBeHidden()
  await page.screenshot({ path: testInfo.outputPath("spring-hill-appointments.png"), fullPage: true })
  await railFilter(page, "all").click()
  await page.getByRole("button", { name: "Workspace selector" }).click()
  await page.getByRole("button", { name: "Fixture Location 3", exact: true }).click()
  await expect(railFilter(page, "all")).toHaveText("All0")
  await expect(railFilter(page, "appointments")).toHaveCount(0)
  await expect(page.getByTestId("patient-row")).toHaveCount(0)
})

test("the workspace keeps one frame from loading to loaded", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  let release = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route(/\/v1\/workspace(\?|$)/, async (route) => {
    await gate
    await route.continue()
  })
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  const panel = page.getByTestId("workspace-panel")
  const frame = () =>
    panel.evaluate((element) => {
      let ground = element.parentElement
      while (ground && getComputedStyle(ground).backgroundColor === "rgba(0, 0, 0, 0)") ground = ground.parentElement
      const box = element.getBoundingClientRect()
      return {
        box: [box.x, box.y, box.width, box.height].map(Math.round),
        panel: getComputedStyle(element).backgroundColor,
        ground: ground ? getComputedStyle(ground).backgroundColor : "",
      }
    })
  await expect(page.locator('[aria-busy="true"]')).toBeVisible()
  let loading = await frame()
  await expect.poll(async () => (loading = await frame()).panel).not.toBe("")
  expect(loading.box[1]).toBe(8)
  await page.screenshot({ path: testInfo.outputPath("workspace-loading.png") })
  release()
  await expect(page.locator('[aria-busy="true"]')).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath("workspace-loaded.png") })
  expect(await frame()).toEqual(loading)
})

test("My Tasks leaves out a colleague's category, Everyone lists it, and work with no live owner is flagged for all", async ({ page }) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT || !databaseURL, "E2E fixture and E2E_DATABASE_URL required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e")) throw new Error("Disposable database required")
  const db = new Client({ connectionString: databaseURL })
  await db.connect()
  const roster = ["secondary@abita.test", "never-signed-in@abita.test"]
  const key = `owners-${Date.now()}`
  const colleagues = "+12025550231"
  const unowned = "+12025550232"
  try {
    await signInAs(page, "secondary@abita.test", "Synthetic Colleague")
    await expect(page.getByTestId("mounted-workspace")).toBeVisible()
    const { rows: [location] } = await db.query(
      "SELECT id, practice_id FROM access_locations WHERE provisioning_key = 'fixture-location-1'",
    )
    await db.query("INSERT INTO work_responsibility_locations VALUES ($1, $2) ON CONFLICT DO NOTHING", [location.practice_id, location.id])
    await db.query(
      `INSERT INTO work_responsibilities VALUES ($1, $2, $3, 'referrals', 'primary'), ($1, $2, $4, 'medication', 'primary')
       ON CONFLICT DO NOTHING`,
      [location.practice_id, location.id, ...roster],
    )
    const file = async (suffix: string, category: string, phone: string) => {
      const created = await page.request.post(`${portalURL}/v1/tasks`, { headers: { authorization: "Bearer synthetic-service-token" }, data: {
        callId: `${key}-${suffix}`, callerPhone: phone, category, idempotencyKey: `${key}-${suffix}`,
        officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency: "normal",
        summary: `${key} ${suffix}`, message: "Synthetic ownership work.",
      } })
      expect(created.ok(), await created.text()).toBeTruthy()
      return (await created.json()).taskId as string
    }
    await file("colleague", "referrals", colleagues)
    const unownedTask = await file("unowned", "medication", unowned)

    await signInAs(page, "selected@abita.test", "Synthetic Staff")
    const search = page.getByLabel("Search tasks, names, or phone")
    await search.fill(key)
    await search.press("Enter")
    const noOwner = railFilter(page, "unowned")
    const footer = page.getByTestId("patient-work-footer")
    await expect(railScope(page, "My Tasks")).toHaveAttribute("aria-pressed", "true")
    await expect(railFilter(page, "all")).toHaveText("All1")
    await expect(page.getByTestId("patient-row")).toHaveCount(1)
    await expect(patientRow(page, unowned).getByTestId("patient-row-reason")).toHaveText(`${key} unowned`)
    await expect(patientRow(page, colleagues)).toHaveCount(0)
    await expect(footer).toHaveText(/^\d+ open for \d+ patients · 1 more under Everyone$/)
    await expect(noOwner).toHaveText("No owner1")
    await expect(noOwner).toHaveClass(/(^|\s)text-destructive(\s|$)/)
    await noOwner.click()
    await expect(noOwner).toHaveAttribute("aria-pressed", "true")
    await expect(page.getByTestId("patient-row")).toHaveCount(1)
    await expect(patientRow(page, unowned)).toBeVisible()

    await railFilter(page, "all").click()
    await railScope(page, "Everyone").click()
    await expect(railFilter(page, "all")).toHaveText("All2")
    await expect(page.getByTestId("patient-row")).toHaveCount(2)
    await expect(patientRow(page, colleagues).getByTestId("patient-row-reason")).toHaveText(`${key} colleague`)
    await expect(noOwner).toHaveText("No owner1")
    await expect(footer).toHaveText(/^\d+ open for \d+ patients$/)
    await noOwner.click()
    await expect(patientRow(page, unowned)).toBeVisible()
    await expect(patientRow(page, colleagues)).toHaveCount(0)

    await railFilter(page, "all").click()
    await expect(page.getByTestId("patient-row")).toHaveCount(2)
    const { token } = await (await page.request.get("/api/auth/token")).json()
    const complete = async (taskID: string) => {
      const completed = await page.request.post(`${portalURL}/v1/tasks/${taskID}/complete`, {
        headers: { authorization: `Bearer ${token}` },
        data: { expectedVersion: 1, reason: "DONE" },
      })
      expect(completed.ok(), await completed.text()).toBeTruthy()
    }
    const firstRowTop = async () => (await page.getByTestId("patient-row").first().boundingBox())?.y
    await complete(unownedTask)
    await expect(noOwner).toHaveText("No owner0")
    await expect(noOwner).not.toHaveClass(/(^|\s)text-destructive(\s|$)/)
    await expect(page.getByTestId("patient-row")).toHaveCount(1)
    const top = await firstRowTop()
    const again = await file("unowned-again", "medication", unowned)
    await expect(noOwner).toHaveText("No owner1")
    await expect(noOwner).toHaveClass(/(^|\s)text-destructive(\s|$)/)
    expect(await firstRowTop()).toBe(top)
    await complete(again)
    await expect(noOwner).toHaveText("No owner0")
    expect(await firstRowTop()).toBe(top)
  } finally {
    await db.query("DELETE FROM work_responsibilities WHERE account_email = ANY($1::text[])", [roster])
    await db.end()
  }
})
