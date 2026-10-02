import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("staff flag a call and Acuity reviews it", async ({
  page,
}, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const id = randomUUID()
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const {
      rows: [scope],
    } = await client.query(
      "SELECT l.id, l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-1'",
    )
    await client.query(
      `INSERT INTO ai_interactions (id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
      VALUES ($1,'manage-agent-e2e',$2,$3,$1::uuid::text,'+15555550876','+17275550101',now()-interval '5 minutes',now()-interval '2 minutes','ESCALATED',3,$4,$5)`,
      [
        id,
        scope.practice_id,
        scope.id,
        {
          items: [
            {
              type: "message",
              role: "system",
              content: ["Internal prompt must stay private"],
            },
            {
              type: "message",
              role: "user",
              content: ["Can I move my appointment to Friday?"],
            },
            {
              type: "function_call",
              name: "reschedule_appointment",
              arguments: "internal tool payload",
            },
            {
              type: "message",
              role: "assistant",
              content: ["Your appointment has been moved to Friday."],
            },
          ],
        },
        {
          domainOutcomes: [{
            outcome: "rescheduled",
            status: "success",
            evidence: { appointmentId: "synthetic-new", cancelledAppointmentId: "synthetic-old" },
          }],
        },
      ],
    )
    await signInAs(page, "selected@abita.test", "Synthetic Staff")
    await page
      .getByRole("button", { name: "Manage agent", exact: true })
      .click()
    await expect(page.getByRole("columnheader")).toHaveText([
      "Date / time",
      "Phone number",
      "Appointment action",
      "Duration",
      "Transferred",
    ])
    await page
      .getByRole("textbox", { name: "Search phone number" })
      .fill("5555550876")
    const row = page.getByRole("row").filter({ hasText: "(555) 555-0876" })
    await expect(row).toContainText("Rescheduled")
    await expect(row).toContainText("3:00")
    await expect(row.getByLabel("Transferred: Yes")).toBeVisible()
    await row.getByRole("button", { name: /Open call/ }).click()
    const panel = page.getByRole("dialog")
    await expect(panel).toBeVisible()
    await expect(async () => {
      const bounds = await panel.boundingBox()
      expect(bounds!.width).toBeGreaterThan(600)
    }).toPass()
    await expect(
      panel.getByText("Can I move my appointment to Friday?", { exact: true }),
    ).toBeVisible()
    await expect(
      panel.getByText("Internal prompt must stay private"),
    ).toHaveCount(0)
    await expect(panel.getByText("internal tool payload")).toHaveCount(0)
    await panel.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-call-open.png"),
    })
    await panel
      .getByRole("button", { name: "Flag an issue", exact: true })
      .click()
    const reason = panel.getByRole("combobox", {
      name: "What went wrong?",
      exact: true,
    })
    await expect(
      panel.getByRole("button", { name: "Flag issue", exact: true }),
    ).toBeDisabled()
    await reason.click()
    await page
      .getByRole("option", { name: "Wrong Appointment Type", exact: true })
      .click()
    await expect(reason).toContainText("Wrong Appointment Type")
    await panel.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-flag-form.png"),
    })
    await page.route(`**/v1/agent-calls/${id}/issue`, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: "{}",
      }),
    )
    await panel.getByRole("button", { name: "Flag issue", exact: true }).click()
    await expect(panel.getByRole("alert")).toContainText("could not be saved")
    await expect(reason).toContainText("Wrong Appointment Type")
    await page.unroute(`**/v1/agent-calls/${id}/issue`)
    let releaseSave!: () => void
    const saveGate = new Promise<void>((resolve) => { releaseSave = resolve })
    await page.route(`**/v1/agent-calls/${id}/issue`, async (route) => {
      await saveGate
      await route.continue()
    })
    await panel.getByRole("button", { name: "Flag issue", exact: true }).click()
    await expect(panel.getByRole("button", { name: /Saving…/ })).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(panel).toBeVisible()
    await panel.getByRole("button", { name: "Close", exact: true }).click()
    await expect(panel).toBeVisible()
    releaseSave()
    await expect(
      panel.getByText("Issue flagged", { exact: true }),
    ).toBeVisible()
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-transcript.png"),
      fullPage: true,
    })
    await page.keyboard.press("Escape")
    await expect(
      page.getByRole("textbox", { name: "Search phone number" }),
    ).toHaveValue("5555550876")
    await page
      .getByRole("button", { name: "Flagged calls", exact: true })
      .click()
    await expect(row).toBeVisible()
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-calls.png"),
      fullPage: true,
    })
    await page.reload()
    await page
      .getByRole("button", { name: "Manage agent", exact: true })
      .click()
    await page
      .getByRole("textbox", { name: "Search phone number" })
      .fill("5555550876")
    await page
      .getByRole("row")
      .filter({ hasText: "(555) 555-0876" })
      .getByRole("button", { name: /Open call/ })
      .click()
    await expect(
      page
        .getByRole("dialog")
        .getByText("Wrong Appointment Type", { exact: true }),
    ).toBeVisible()
    const { rows: reports } = await client.query(
      "SELECT reason,reported_by FROM ai_interaction_issues WHERE interaction_id=$1",
      [id],
    )
    expect(reports).toHaveLength(1)
    expect(reports[0].reason).toBe("WRONG_APPOINTMENT_TYPE")
    expect(reports[0].reported_by).toBeTruthy()
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(async () => {
      const bounds = await page.getByRole("dialog").boundingBox()
      expect(bounds!.width).toBe(390)
    }).toPass()
    await expect(page.getByRole("dialog")).toHaveCSS("opacity", "1")
    await page.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-mobile.png"),
      fullPage: true,
    })
    await page.keyboard.press("Escape")
    await page.setViewportSize({ width: 1280, height: 720 })
    await signInAs(page, "founder@acuity.test", "Synthetic Acuity Operator")
    await page
      .getByRole("button", { name: "Manage agent", exact: true })
      .click()
    await page
      .getByRole("textbox", { name: "Search phone number" })
      .fill("5555550876")
    await page
      .getByRole("button", { name: "Flagged calls", exact: true })
      .click()
    await page
      .getByRole("row")
      .filter({ hasText: "(555) 555-0876" })
      .getByRole("button", { name: /Open call/ })
      .click()
    await expect(
      page
        .getByRole("dialog")
        .getByText("Wrong Appointment Type", { exact: true }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    await page
      .getByRole("button", { name: "AI diagnostics", exact: true })
      .click()
    const diagnostics = page.getByRole("region", { name: "AI call analytics" })
    const pending = diagnostics
      .getByRole("status")
      .filter({ hasText: /flagged by staff needs? review/ })
    await expect(pending).toContainText("(555) 555-0876")
    await expect(pending).toContainText("Wrong Appointment Type")
    await expect(pending).toContainText("Flagged by selected@abita.test")
    await pending
      .getByRole("button", { name: "Review call from (555) 555-0876" })
      .click()
    const flag = page
      .getByRole("dialog")
      .getByRole("region", { name: "Staff flag" })
    await expect(flag).toContainText("Flagged by selected@abita.test")
    const realIssue = flag.getByRole("button", { name: "Real issue", exact: true })
    await expect(realIssue).toHaveAttribute("aria-pressed", "false")
    await realIssue.click()
    await expect(realIssue).toHaveAttribute("aria-pressed", "true")
    await expect(flag).toContainText("Reviewed by founder@acuity.test")
    await flag.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("diagnostics-flag-review.png"),
    })
    const { rows: reviews } = await client.query(
      "SELECT review_outcome FROM ai_interaction_issues WHERE interaction_id=$1",
      [id],
    )
    expect(reviews[0].review_outcome).toBe("CONFIRMED")
    await page.keyboard.press("Escape")
    await expect(
      diagnostics.getByRole("button", { name: "Review call from (555) 555-0876" }),
    ).toHaveCount(0)
    expect(errors).toEqual([])
  } finally {
    await client.query(
      "DELETE FROM ai_interaction_issues WHERE interaction_id=$1",
      [id],
    )
    await client.query("DELETE FROM ai_interactions WHERE id=$1", [id])
    await client.end()
  }
})

test("call navigation crosses page boundaries with retry and shows a concurrent report", async ({
  page,
}, testInfo) => {
  const ids = [randomUUID(), randomUUID(), randomUUID()]
  const calls = ids.map((id, index) => ({
    id,
    phone: `+1555555010${index}`,
    startedAt: new Date(Date.now() - index * 60_000).toISOString(),
    durationSeconds: 60,
    appointmentActions: [],
    transferred: false,
    issueFlagged: false,
  }))
  let failNextPage = true
  let firstCallReported = false
  const requests: Array<Record<string, unknown>> = []
  await page.route("**/v1/agent-calls/query", async (route) => {
    const body = route.request().postDataJSON()
    requests.push(body)
    if (body.cursor && failNextPage) {
      await route.fulfill({ status: 503, json: {} })
      return
    }
    await route.fulfill({
      json: body.cursor
        ? { calls: [calls[2]], nextCursor: "" }
        : { calls: calls.slice(0, 2), nextCursor: "next-page" },
    })
  })
  for (const [index, id] of ids.entries()) {
    await page.route(`**/v1/agent-calls/${id}`, (route) =>
      route.fulfill({
        json: {
          call: calls[index],
          locationName: "Synthetic office",
          issue: index === 0 && firstCallReported ? {
            reason: "INSURANCE_ISSUE",
            createdAt: calls[index].startedAt,
          } : undefined,
          messages: Array.from({ length: 30 }, (_, turn) => ({
            speaker: turn % 2 ? "Agent" : "Caller",
            text: `Call ${index + 1}, message ${turn + 1}. Synthetic conversation.`,
            occurredAt: calls[index].startedAt,
          })),
        },
      }),
    )
  }
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await page
    .getByRole("button", { name: "Manage agent", exact: true })
    .click()
  await page
    .getByRole("textbox", { name: "Search phone number" })
    .fill("555555010")
  await expect.poll(() => requests.at(-1)?.phone).toBe("555555010")
  await page
    .getByRole("button", { name: /Open call from \(555\) 555-0100/ })
    .click()
  const panel = page.getByRole("dialog")
  const previous = panel.getByRole("button", {
    name: "Previous call",
    exact: true,
  })
  const next = panel.getByRole("button", { name: "Next call", exact: true })
  await expect(previous).toBeDisabled()
  await expect(panel.getByText("Call 1 of 2+", { exact: true })).toBeVisible()
  const transcript = panel.getByLabel("Call transcript", { exact: true })
  await expect(transcript).toBeVisible()
  await transcript.evaluate(element => { element.scrollTop = element.scrollHeight })
  await expect(panel.getByRole("button", { name: "Flag an issue", exact: true })).toBeInViewport()
  const scroll = panel.getByLabel("Call transcript", { exact: true })
  await scroll.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  await expect
    .poll(() => scroll.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(100)
  await next.click()
  await expect(
    panel.getByRole("heading", { name: "(555) 555-0101", exact: true }),
  ).toBeVisible()
  await expect
    .poll(() => scroll.evaluate((element) => element.scrollTop))
    .toBe(0)
  await next.click()
  await expect(panel.getByRole("alert")).toContainText(
    "More calls could not be loaded",
  )
  await expect(panel.getByText("Call 2 of 2+", { exact: true })).toBeVisible()
  failNextPage = false
  await next.click()
  await expect(
    panel.getByRole("heading", { name: "(555) 555-0102", exact: true }),
  ).toBeVisible()
  await expect(panel.getByText("Call 3 of 3", { exact: true })).toBeVisible()
  await expect(next).toBeDisabled()
  await expect(panel.getByRole("alert")).toHaveCount(0)
  expect(
    requests
      .filter((request) => request.cursor)
      .every(
        (request) =>
          request.phone === "555555010" &&
          request.range === "24h" &&
          request.cursor === "next-page",
      ),
  ).toBe(true)
  await previous.click()
  await previous.click()
  await panel.screenshot({
    animations: "disabled",
    path: testInfo.outputPath("call-navigation.png"),
  })
  await panel
    .getByRole("button", { name: "Flag an issue", exact: true })
    .click()
  const reason = panel.getByRole("combobox", {
    name: "What went wrong?",
    exact: true,
  })
  await reason.click()
  await page
    .getByRole("option", { name: "Wrong Appointment Type", exact: true })
    .click()
  await page.route(`**/v1/agent-calls/${ids[0]}/issue`, (route) => {
    firstCallReported = true
    return route.fulfill({ status: 409, json: {} })
  })
  await panel.getByRole("button", { name: "Flag issue", exact: true }).click()
  await expect(panel.getByText("Issue flagged", { exact: true })).toBeVisible()
  await expect(panel.getByText("Insurance Issue", { exact: true })).toBeVisible()
  await expect(reason).toHaveCount(0)
  await expect(
    panel.getByRole("button", { name: "Flag issue", exact: true }),
  ).toHaveCount(0)
  await page.keyboard.press("Escape")
  await expect(
    page
      .getByRole("row")
      .filter({ hasText: "(555) 555-0100" })
      .getByLabel("Issue flagged"),
  ).toBeVisible()
})
