import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("AI diagnostics connect measured distributions and tool failures to exact call evidence", async ({
  page,
}, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids: string[] = []
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const {
      rows: [scope],
    } = await client.query(
      "SELECT l.id, l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'",
    )
    const names = [
      "get_availability",
      "lookup_patient",
      "book_appointment",
      "check_insurance",
      "transfer_call",
      "create_task",
    ]
    for (let call = 0; call < 70; call++) {
      const id = randomUUID()
      ids.push(id)
      const start = new Date(
        Date.now() - (call % 7) * 86_400_000 - (call + 1) * 60_000,
      )
      const items: Record<string, unknown>[] = []
      for (let turn = 0; turn < 10; turn++) {
        const responseMs =
          (call + turn) % 37 === 0
            ? 10800
            : (call + turn) % 13 === 0
              ? 4300
              : 650 + turn * 105 + (call % 7) * 40
        items.push({
          type: "message",
          role: "user",
          id: `caller-${turn}`,
          created_at: start.getTime() / 1000 + turn * 12,
          content: ["Synthetic caller request"],
          metrics: { sttMs: 120 + turn * 20 },
        })
        items.push({
          type: "message",
          role: "assistant",
          id: `reply-${turn}`,
          created_at: start.getTime() / 1000 + turn * 12 + responseMs / 1000,
          content: ["Synthetic assistant response"],
          metrics: {
            ttftMs: 250 + turn * 45 + (call % 5) * 30,
            ttsTtfbMs: 90 + turn * 12,
            totalLatencyMs: responseMs,
          },
        })
      }
      for (let tool = 0; tool < names.length; tool++) {
        const duration = [
          900 + (call % 11) * 190,
          180 + (call % 8) * 35,
          550 + (call % 7) * 80,
          600,
          360 + (call % 5) * 90,
          130 + (call % 5) * 25,
        ][tool]
        const created = start.getTime() / 1000 + tool * 15
        items.push({
          type: "function_call",
          id: `request-${tool}`,
          call_id: `tool-${tool}`,
          name: names[tool],
          ...(tool !== 3 ? { created_at: created } : {}),
          arguments: "{}",
        })
        if (!(tool === 4 && call % 17 === 0))
          items.push({
            type: "function_call_output",
            call_id: `tool-${tool}`,
            name: names[tool],
            ...(tool !== 3 ? { created_at: created + duration / 1000 } : {}),
            is_error: call % (19 + tool) === 0,
            output:
              call % (19 + tool) === 0
                ? "Synthetic provider error"
                : "Synthetic tool result",
          })
      }
      await client.query(
        `INSERT INTO ai_interactions (id, service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage, appointment_outcome, transcript, closeout_payload)
        VALUES ($1, 'diagnostics-e2e', $2, $3, $1::uuid::text, '+15555550199', '+17275550106', $4, $5, $7, 3, 'INDETERMINATE', $6, '{"domainOutcomes":[]}'::jsonb)`,
        [
          id,
          scope.practice_id,
          scope.id,
          start,
          new Date(start.getTime() + 120_000),
          { items },
          call % 2 === 0 ? "ESCALATED" : "COMPLETED",
        ],
      )
    }
    await page.setViewportSize({ width: 1440, height: 1320 })
    await signInAs(page, "founder@acuity.test", "Fixture Founder")
    await page
      .getByRole("button", { name: "AI diagnostics", exact: true })
      .click()
    const diagnostics = page.getByRole("region", { name: "AI call analytics" })
    await diagnostics
      .getByRole("combobox", { name: "Office", exact: true })
      .click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()
    const volume = diagnostics.getByRole("region", {
      name: "Call volume over time",
      exact: true,
    })
    const transfers = diagnostics.getByRole("region", {
      name: "Transfer rate over time",
      exact: true,
    })
    await expect(volume.getByRole("strong")).toHaveText("70")
    await expect(transfers.getByRole("strong")).toHaveText("50.0%")
    await expect(
      transfers.getByText("35 of 70 calls transferred"),
    ).toBeVisible()
    await expect(volume.locator(".recharts-bar-rectangle")).not.toHaveCount(0)
    await expect(transfers.locator(".recharts-line-curve")).toHaveCount(1)
    await transfers.getByText("View daily data", { exact: true }).click()
    const dailyRows = transfers.getByRole("table").locator("tbody tr")
    const dailyCounts = await dailyRows.evaluateAll((rows) =>
      rows.reduce(
        (sum, row) => sum + Number(row.querySelector("td")?.textContent),
        0,
      ),
    )
    expect(dailyCounts).toBe(70)
    await transfers.getByText("View daily data", { exact: true }).click()
    await page.screenshot({
      path: testInfo.outputPath("call-trends.png"),
      fullPage: true,
      animations: "disabled",
    })
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(volume).toBeVisible()
    expect(
      await diagnostics.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
    ).toBe(true)
    await page.screenshot({
      path: testInfo.outputPath("call-trends-mobile.png"),
      fullPage: true,
      animations: "disabled",
    })
    await page.setViewportSize({ width: 1280, height: 800 })
    await diagnostics
      .getByRole("button", { name: "Calls", exact: true })
      .click()
    await diagnostics
      .getByRole("button", { name: /Open analytics for call from/ })
      .first()
      .click()
    const callSheet = page.getByRole("dialog", { name: "AI call evidence" })
    await expect(
      callSheet.getByRole("button", { name: "Turn timing" }),
    ).toHaveAttribute("aria-pressed", "true")
    await expect(callSheet.getByText("P50 STT", { exact: true })).toBeVisible()
    await expect(callSheet.getByLabel("Caller message").first()).toContainText("STT final")
    await expect(callSheet.getByLabel("Agent message").first()).toContainText("TTFT")
    await expect(
      callSheet.getByRole("heading", {
        name: "Appointment and receipt evidence",
      }),
    ).toHaveCount(0)
    await expect(
      callSheet.getByLabel("Caller message").first(),
    ).toBeInViewport()
    await expect(
      callSheet.getByLabel("Caller message").first(),
    ).toHaveAttribute("data-align", "end")
    await expect(callSheet.getByLabel("Agent message").first()).toHaveAttribute(
      "data-align",
      "start",
    )
    await expect(
      callSheet
        .getByLabel("Caller message")
        .first()
        .locator('[data-slot="bubble"]'),
    ).toHaveCount(1)
    await expect(
      callSheet
        .getByLabel("Agent message")
        .first()
        .locator('[data-slot="bubble"]'),
    ).toHaveCount(1)
    const transcript = callSheet.getByLabel("Scrollable call evidence")
    await transcript.evaluate((element) => {
      element.scrollTop = 500
    })
    await expect(
      callSheet.getByText("P50 STT", { exact: true }),
    ).toBeInViewport()
    await expect(
      callSheet.getByText("P50 E2E", { exact: true }),
    ).toBeInViewport()
    await transcript.evaluate((element) => {
      element.scrollTop = 0
    })
    await page.screenshot({
      path: testInfo.outputPath("conversation.png"),
      fullPage: true,
      animations: "disabled",
    })
    await callSheet.getByRole("button", { name: "Turn timing" }).click()
    await expect(callSheet.getByText("P50 STT", { exact: true })).toBeVisible()
    await callSheet.getByRole("button", { name: "Turn timing" }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    expect(
      await callSheet.evaluate(
        (element) => element.scrollWidth <= element.clientWidth,
      ),
    ).toBe(true)
    await page.screenshot({
      path: testInfo.outputPath("conversation-mobile.png"),
      fullPage: true,
      animations: "disabled",
    })
    await page.getByRole("button", { name: "Close", exact: true }).click()
    await page.setViewportSize({ width: 1440, height: 1320 })
    await diagnostics
      .getByRole("button", { name: "Performance", exact: true })
      .click()
    await expect(
      diagnostics.getByText("700 samples · 70 of 70 calls measured"),
    ).toBeVisible()
    await expect(
      diagnostics
        .getByRole("region", { name: "Response performance" })
        .locator(".recharts-line-curve"),
    ).toHaveCount(2)
    await page.screenshot({
      path: testInfo.outputPath("performance.png"),
      fullPage: true,
      animations: "disabled",
    })
    await diagnostics.getByRole("button", { name: /10s\+:.*samples/ }).click()
    const samples = diagnostics.getByRole("region", { name: "Samples · 10s+" })
    await expect(samples.getByRole("button")).toHaveCount(5)
    await samples.getByRole("button").first().click()
    await expect(page.locator("[data-diagnostic-selected=true]")).toContainText(
      "Synthetic assistant response",
    )
    await expect(page.locator("[data-diagnostic-selected=true]")).toContainText(
      "Response 10.80 s",
    )
    await page.getByRole("button", { name: "Close", exact: true }).click()
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await diagnostics.getByRole("button", { name: /^STT/ }).click()
    await expect(
      diagnostics.getByRole("heading", { name: "STT distribution" }),
    ).toBeVisible()
    await diagnostics
      .getByRole("button", { name: "Tools", exact: true })
      .click()
    await expect(
      diagnostics.getByText("345 of 420 executions timed"),
    ).toBeVisible()
    const tools = diagnostics.getByRole("region", { name: "Tool breakdown" })
    const availability = tools
      .getByRole("row")
      .filter({ hasText: "get_availability" })
    await expect(availability).toContainText("70 / 70")
    const unknown = tools
      .getByRole("row")
      .filter({ hasText: "check_insurance" })
    await expect(unknown).toContainText("Not measured")
    await expect(unknown).toContainText("0 / 70")
    await page.screenshot({
      path: testInfo.outputPath("tools.png"),
      fullPage: true,
      animations: "disabled",
    })
    await availability.getByRole("button", { name: /%/ }).click()
    await diagnostics
      .getByRole("region", { name: "Failed executions · get availability" })
      .getByRole("button")
      .first()
      .click()
    const selected = page.locator("[data-diagnostic-selected=true]")
    await expect(selected).toContainText("get_availability")
    await expect(selected).toContainText("execution")
    await expect(selected.locator("details")).toHaveAttribute("open", "")
    await page.screenshot({
      path: testInfo.outputPath("tool-evidence.png"),
      fullPage: true,
      animations: "disabled",
    })
    await page.getByRole("button", { name: "Close", exact: true }).click()
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await tools
      .getByRole("combobox", { name: "Sort tools" })
      .selectOption("volume")
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(
      diagnostics.getByRole("region", { name: "Tool execution summary" }),
    ).toBeVisible()
    await page.screenshot({
      path: testInfo.outputPath("tools-mobile.png"),
      fullPage: true,
      animations: "disabled",
    })
    // New scope clears selected tools and never reuses the prior Location's examples.
    await diagnostics
      .getByRole("combobox", { name: "Office", exact: true })
      .click()
    await page
      .getByRole("option", { name: "Fixture Location 5", exact: true })
      .click()
    await expect(
      diagnostics.getByText("No tool executions in this range."),
    ).toBeVisible()
    await expect(
      diagnostics.getByRole("region", { name: /Failed executions/ }),
    ).toHaveCount(0)
    expect(errors).toEqual([])
  } finally {
    await client.query(
      "DELETE FROM ai_interactions WHERE id = ANY($1::uuid[])",
      [ids],
    )
    await client.end()
  }
})

test("historical tool failures reveal and focus their technical evidence", async ({
  page,
}) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const id = randomUUID()
  try {
    const {
      rows: [scope],
    } = await client.query(
      "SELECT l.id, l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'",
    )
    const start = new Date(Date.now() - 60_000)
    const items = Array.from({ length: 20 }, (_, index) => ({
      id: `historical-message-${index}`,
      type: "message",
      role: index % 2 ? "assistant" : "user",
      content: ["Synthetic historical conversation"],
      created_at: start.getTime() / 1000 + index,
    }))
    await client.query(
      `INSERT INTO ai_interactions (id, service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage, appointment_outcome, transcript, closeout_payload)
       VALUES ($1, 'diagnostics-e2e', $2, $3, $1::uuid::text, '+15555550198', '+17275550106', $4, $5, 'COMPLETED', 3, 'INDETERMINATE', $6, $7)`,
      [
        id,
        scope.practice_id,
        scope.id,
        start,
        new Date(start.getTime() + 30_000),
        { items },
        {
          toolExecutions: [
            {
              callId: "historical-failure",
              toolName: "historical_lookup",
              status: "ERROR",
              createdAt: start.toISOString(),
            },
          ],
        },
      ],
    )
    await page.setViewportSize({ width: 1280, height: 800 })
    await signInAs(page, "founder@acuity.test", "Fixture Founder")
    await page
      .getByRole("button", { name: "AI diagnostics", exact: true })
      .click()
    const diagnostics = page.getByRole("region", { name: "AI call analytics" })
    await diagnostics
      .getByRole("combobox", { name: "Office", exact: true })
      .click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()
    await diagnostics
      .getByRole("button", { name: "Tools", exact: true })
      .click()
    const tool = diagnostics
      .getByRole("row")
      .filter({ hasText: "historical_lookup" })
    await tool.getByRole("button", { name: /100.0%/ }).click()
    await diagnostics
      .getByRole("region", { name: "Failed executions · historical lookup" })
      .getByRole("button")
      .click()
    const selected = page.locator("[data-diagnostic-selected=true]")
    await expect(selected).toHaveCount(1)
    await expect(selected).toContainText("historical-failure")
    await expect(selected).toContainText("execution: ERROR")
    await expect(selected).toBeInViewport()
    await expect(
      page
        .locator("details")
        .filter({ has: page.getByText("Technical evidence", { exact: true }) }),
    ).toHaveAttribute("open", "")
    await expect
      .poll(() =>
        page
          .getByLabel("Scrollable call evidence")
          .evaluate((element) => element.scrollTop),
      )
      .toBeGreaterThan(0)
  } finally {
    await client.query("DELETE FROM ai_interactions WHERE id=$1", [id])
    await client.end()
  }
})
