import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("staff review calls and persist an issue for Acuity", async ({
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
        { domainOutcomes: [{ outcome: "rescheduled", status: "success" }] },
      ],
    )
    await signInAs(page, "selected@abita.test", "Synthetic Staff")
    await page
      .getByRole("button", { name: "Manage my agent", exact: true })
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
    await panel
      .getByRole("textbox", { name: "What went wrong?" })
      .fill("Please review the appointment instructions.")
    await panel.screenshot({
      animations: "disabled",
      path: testInfo.outputPath("manage-agent-flag-form.png"),
    })
    // A failed save keeps the note and makes retry explicit.
    await page.route(`**/v1/agent-calls/${id}/issue`, (route) =>
      route.fulfill({
        status: 503,
        contentType: "application/json",
        body: "{}",
      }),
    )
    await panel.getByRole("button", { name: "Flag issue", exact: true }).click()
    await expect(panel.getByRole("alert")).toContainText("could not be saved")
    await expect(
      panel.getByRole("textbox", { name: "What went wrong?" }),
    ).toHaveValue("Please review the appointment instructions.")
    await page.unroute(`**/v1/agent-calls/${id}/issue`)
    await panel.getByRole("button", { name: "Flag issue", exact: true }).click()
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
      .getByRole("button", { name: "Manage my agent", exact: true })
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
        .getByText("Please review the appointment instructions.", {
          exact: true,
        }),
    ).toBeVisible()
    const { rows: reports } = await client.query(
      "SELECT note,reported_by FROM ai_interaction_issues WHERE interaction_id=$1",
      [id],
    )
    expect(reports).toHaveLength(1)
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
      .getByRole("button", { name: "Manage my agent", exact: true })
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
        .getByText("Please review the appointment instructions.", {
          exact: true,
        }),
    ).toBeVisible()
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

test("call navigation preserves drafts and crosses page boundaries with retry", async ({
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
    .getByRole("button", { name: "Manage my agent", exact: true })
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
  const transcript = panel.locator('[data-slot="message-scroller-viewport"]')
  await expect(transcript).toBeVisible()
  await transcript.evaluate(element => { element.scrollTop = element.scrollHeight })
  await expect(panel.getByRole("button", { name: "Flag an issue", exact: true })).toBeInViewport()
  await panel
    .getByRole("button", { name: "Flag an issue", exact: true })
    .click()
  await panel
    .getByRole("textbox", { name: "What went wrong?" })
    .fill("Keep this unfinished note for the first call.")
  await next.click()
  await expect(
    panel.getByRole("heading", { name: "(555) 555-0101", exact: true }),
  ).toBeVisible()
  await expect(
    panel.getByRole("textbox", { name: "What went wrong?" }),
  ).toHaveCount(0)
  await previous.click()
  await expect(
    panel.getByRole("textbox", { name: "What went wrong?" }),
  ).toHaveValue("Keep this unfinished note for the first call.")
  const scroll = panel.locator('[data-slot="message-scroller-viewport"]')
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
          request.range === "7d" &&
          request.cursor === "next-page",
      ),
  ).toBe(true)
  await previous.click()
  await previous.click()
  await expect(
    panel.getByRole("textbox", { name: "What went wrong?" }),
  ).toHaveValue("Keep this unfinished note for the first call.")
  await panel.screenshot({
    animations: "disabled",
    path: testInfo.outputPath("call-navigation.png"),
  })
})
