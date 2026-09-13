import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { expectConnectedZeroBaseline, signInAs } from "./support"

test("Staff analytics measures connected phone time and the 48-hour task goal", async ({
  page,
}, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  const url = process.env.E2E_DATABASE_URL
  test.skip(!url, "E2E_DATABASE_URL required")
  if (!new URL(url!).pathname.endsWith("_e2e"))
    throw new Error("Disposable database required")
  const db = new Client({ connectionString: url })
  await db.connect()
  let analyticsRequests = 0
  page.on("request", (request) => {
    if (request.url().includes("/v1/analytics/staff/query")) analyticsRequests++
  })
  const calls: string[] = [],
    tasks: string[] = []
  const threadID = randomUUID()
  try {
    await signInAs(page, "selected@abita.test", "Fixture Staff")
    await expect(page.getByTestId("mounted-workspace")).toBeVisible()
    await signInAs(page, "admin@abita.test", "Fixture Admin")
    const {
      rows: [location],
    } = await db.query(
      "SELECT id,practice_id FROM access_locations WHERE provisioning_key='fixture-location-6'",
    )
    const {
      rows: [staff],
    } = await db.query(
      "SELECT user_subject,email FROM access_memberships WHERE practice_id=$1 AND email='selected@abita.test'",
      [location.practice_id],
    )
    const day = new Date(Date.now() - 3 * 86400000)
    for (const [direction, seconds] of [
      ["INBOUND", 120],
      ["INBOUND", 60],
      ["OUTBOUND", 300],
    ] as const) {
      const id = randomUUID()
      calls.push(id)
      const end = new Date(+day + seconds * 1000)
      await db.query(
        "INSERT INTO human_calling_calls(id,practice_id,location_id,direction,entry_point,terminal_outcome,created_at,ended_at,outbound_idempotency_key) VALUES($1::uuid,$2,$3,$4,'STANDALONE','RESOLVED',$5,$6,'staff-e2e-'||$1::uuid::text)",
        [id, location.practice_id, location.id, direction, day, end],
      )
      await db.query(
        "INSERT INTO human_calling_call_legs(call_id,role,sequence,staff_subject,state,answered_at,bridge_pending_at,bridged_at,ended_at) VALUES($1,'STAFF',1,$2,'ENDED',$3,$3,$3,$4)",
        [id, staff.user_subject, day, end],
      )
    }
    await db.query(
      "INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone) VALUES($1,$2,$3,'+15555550198','+15555550199')",
      [threadID, location.practice_id, location.id],
    )
    for (const state of ["SENT", "DELIVERED", "DELIVERED", "FAILED"]) {
      await db.query(
        "INSERT INTO messaging_messages(thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_kind,created_by_subject,created_at) VALUES($1,$2,$3,'OUTBOUND','Synthetic staff text','+15555550198','+15555550199',$4,'HUMAN',$5,$6)",
        [
          threadID,
          location.practice_id,
          location.id,
          state,
          staff.user_subject,
          day,
        ],
      )
    }
    for (const [index, completed] of [true, true, false].entries()) {
      const id = randomUUID()
      tasks.push(id)
      const createdAt = new Date(+day + (index % 2) * 86400000)
      await db.query(
        `INSERT INTO work_tasks(id,practice_id,location_id,phone,title,state,created_by_kind,created_by_subject,created_at,updated_at,origin,source_call_id,source_message,category,ai_idempotency_key,ai_input_fingerprint,completed_by_kind,completed_by_subject,completed_by_email,completed_at) VALUES($1::uuid,$2,$3,'+15555550199','Synthetic staff task',$4,'SERVICE','staff-e2e',$5,$5,'ABITA_AI',$1::uuid::text,'Synthetic staff evidence','other',$1::uuid::text,decode(repeat('00',32),'hex'),$6,$7,$8,$9)`,
        [
          id,
          location.practice_id,
          location.id,
          completed ? "COMPLETED" : "OPEN",
          createdAt,
          completed ? "HUMAN" : null,
          completed ? staff.user_subject : null,
          completed ? staff.email : null,
          completed ? new Date(+createdAt + 86400000) : null,
        ],
      )
    }
    await page.getByRole("button", { name: "Analytics", exact: true }).click()
    await page.getByRole("button", { name: "Staff", exact: true }).click()
    await page.getByRole("button", { name: "7 days", exact: true }).click()
    await page.getByRole("combobox", { name: "Office", exact: true }).click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()
    await expect(
      page.getByRole("status", {
        name: "Median task completion time",
        exact: true,
      }),
    ).toHaveText("24h 00m")
    const performance = page.getByRole("region", {
      name: "Staff performance",
      exact: true,
    })
    await expect(performance.getByText("66.7%", { exact: true })).toBeVisible()
    const accounts = page.getByRole("region", {
      name: "Staff accounts",
      exact: true,
    })
    await expect(
      accounts
        .getByRole("row")
        .filter({ hasText: "selected@abita.test" })
        .getByRole("cell"),
    ).toHaveText([
      "selected@abita.testStaff", "21m 30s avg/call", "15m 00s avg/call",
      "3m", "5m", "3", "2",
    ])
    await expect(
      accounts
        .getByRole("row")
        .filter({ hasText: "admin@abita.test" })
        .getByRole("cell"),
    ).toHaveText([
      "admin@abita.testAdmin", "0— avg/call", "0— avg/call",
      "0m", "0m", "0", "0",
    ])
    await expect(accounts.getByRole("row").last().getByRole("cell")).toHaveText([
      "Total", "21m 30s avg/call", "15m 00s avg/call", "3m", "5m", "3", "2",
    ])
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "selected@abita.test",
    )
    await expect(
      accounts
        .getByRole("columnheader")
        .filter({ hasText: "Inbound time" }),
    ).toHaveAttribute("aria-sort", "descending")
    await expect(performance.locator(".recharts-line")).toHaveCount(1)
    await expect(performance.locator(".recharts-area")).toHaveCount(1)
    await expect(performance.locator(".recharts-line-dots circle")).toHaveCount(0)
    await expectConnectedZeroBaseline(performance, 1)
    await expect(
      page.getByRole("button", { name: "Staff", exact: true }),
    ).toHaveAttribute("aria-pressed", "true")
    const requestsBeforeSort = analyticsRequests
    await accounts
      .getByRole("button", { name: "Sort by inbound calls", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "selected@abita.test",
    )
    await accounts
      .getByRole("button", { name: "Sort by inbound calls", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "admin@abita.test",
    )
    await accounts
      .getByRole("button", { name: "Sort by outbound time", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "selected@abita.test",
    )
    await accounts
      .getByRole("button", { name: "Sort by texts sent", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "selected@abita.test",
    )
    await accounts
      .getByRole("button", { name: "Sort by texts sent", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "admin@abita.test",
    )
    await accounts
      .getByRole("button", { name: "Sort by tasks completed", exact: true })
      .click()
    await expect(accounts.getByRole("row").nth(1)).toContainText(
      "selected@abita.test",
    )
    await expect(accounts.getByRole("row").last()).toContainText("Total")
    expect(analyticsRequests).toBe(requestsBeforeSort)
    // Tick text must fit inside the SVG viewport, including its bottom row.
    await expect(async () => {
      const clippedLabels = await performance
        .locator('[data-slot="chart"]')
        .evaluate((chart) => {
          const bounds = chart.getBoundingClientRect()
          const labels = [...chart.querySelectorAll("svg text")]
          if (labels.length < 3) {
            return [{ error: `Expected chart labels, found ${labels.length}` }]
          }
          return labels.flatMap((label) => {
            const box = label.getBoundingClientRect()
            const clipped =
              box.left < bounds.left - 0.25 ||
              box.right > bounds.right + 0.25 ||
              box.top < bounds.top - 0.25 ||
              box.bottom > bounds.bottom + 0.25
            return clipped
              ? [{
                  text: label.textContent,
                  label: box.toJSON(),
                  chart: bounds.toJSON(),
                }]
              : []
          })
        })
      expect(clippedLabels).toEqual([])
    }).toPass()
    await accounts.scrollIntoViewIfNeeded()
    await accounts.screenshot({
      path: testInfo.outputPath("staff-texts-call-averages.png"),
    })
    await page.screenshot({
      path: testInfo.outputPath("admin-staff-analytics.png"),
      fullPage: true,
    })
    // Incomplete timing must not appear as a shorter average, and must not
    // affect the other direction or the count of successfully sent texts.
    await db.query(
      "UPDATE human_calling_call_legs SET state='ENDING',ended_at=NULL WHERE call_id=$1",
      [calls[0]],
    )
    await page.getByRole("button", { name: "30 days", exact: true }).click()
    await expect(
      accounts
        .getByRole("row")
        .filter({ hasText: "selected@abita.test" })
        .getByRole("cell"),
    ).toHaveText([
      "selected@abita.testStaff", "2— avg/call", "15m 00s avg/call",
      "—", "5m", "3", "2",
    ])
    await page.getByRole("button", { name: "Bookings", exact: true }).click()
    await expect(
      page.getByRole("region", { name: "Booking performance", exact: true }),
    ).toBeVisible()
    await expect(
      page.getByRole("region", { name: "Staff performance", exact: true }),
    ).toHaveCount(0)
  } finally {
    await db.query("DELETE FROM messaging_messages WHERE thread_id=$1", [
      threadID,
    ])
    await db.query("DELETE FROM messaging_threads WHERE id=$1", [threadID])
    await db.query(
      "DELETE FROM work_task_acknowledgements WHERE task_id=ANY($1::uuid[])",
      [tasks],
    )
    await db.query("DELETE FROM work_tasks WHERE id=ANY($1::uuid[])", [tasks])
    await db.query("DELETE FROM human_calling_calls WHERE id=ANY($1::uuid[])", [
      calls,
    ])
    await db.end()
  }
})
