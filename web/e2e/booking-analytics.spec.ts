import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("Practice Admin booking analytics uses real scoped aggregates and clear copy", async ({
  page,
  browser,
}, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids: string[] = []
  try {
    const scope = await client.query(
      "SELECT l.id, l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'",
    )
    for (let i = 0; i < 6; i++) {
      const id = randomUUID()
      ids.push(id)
      const start = new Date(Date.now() - 3 * 86400000 + i * 1000)
      const booked = i < 4
      await client.query(
        `INSERT INTO ai_interactions
        (id, service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage, appointment_outcome, new_appointment_id, booking_result, appointment_occurred_at, transcript, closeout_payload)
        VALUES ($1::uuid, 'booking-design-e2e', $2, $3, $1::uuid::text, '+15555550199', '+17275550106', $4, $5, 'COMPLETED', 3, $6, $7, $8, $9, $10, $11)`,
        [
          id,
          scope.rows[0].practice_id,
          scope.rows[0].id,
          start,
          new Date(start.getTime() + (300 + i * 60) * 1000),
          booked ? "BOOKING" : "INDETERMINATE",
          booked ? `test-appointment-${id}` : null,
          booked ? { status: "booked" } : null,
          booked ? new Date(start.getTime() + (200 + i * 30) * 1000) : null,
          i === 5
            ? null
            : {
                items: [
                  {
                    type: "function_call",
                    name: "get_availability",
                    call_id: "first",
                  },
                  {
                    type: "function_call",
                    name: "get_availability",
                    call_id: "second",
                  },
                ],
              },
          {
            domainOutcomes:
              i === 2 || i === 5
                ? []
                : [
                    {
                      outcome: i % 2 ? "patient_verified" : "patient_new",
                      status: "success",
                    },
                  ],
          },
        ],
      )
    }
    await signInAs(page, "admin@abita.test", "Fixture Admin")
    await page.getByRole("button", { name: "Analytics", exact: true }).click()
    await expect(
      page.getByRole("heading", { name: "Confirmed bookings", exact: true }),
    ).toBeVisible()
    await expect(
      page.getByRole("button", { name: "AI diagnostics", exact: true }),
    ).toHaveCount(0)
    await page.getByRole("button", { name: "7 days", exact: true }).click()
    await page.getByRole("combobox", { name: "Office", exact: true }).click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()
    const performance = page.getByRole("region", {
      name: "Booking performance",
      exact: true,
    })
    await expect(
      performance.getByRole("status", {
        name: "Confirmed bookings",
        exact: true,
      }),
    ).toHaveText("4")
    await expect(
      page.getByText("Availability calls", { exact: true }),
    ).toHaveCount(0)
    await expect(page.getByText("Sample data", { exact: true })).toHaveCount(0)
    await page.getByRole("button", { name: "Conversion", exact: true }).click()
    await expect(
      performance.getByText(
        "80% of calls with a recorded availability check booked.",
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      performance.getByText("4 of 5 calls booked.", { exact: true }),
    ).toBeVisible()
    await expect(
      performance.getByText("Availability history recorded for 5 of 6 calls.", {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page.getByText(
        "New/existing breakdown covers 4 of 6 calls. Totals include all calls.",
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      page.getByRole("cell", { name: "Unknown", exact: true }),
    ).toHaveCount(0)
    // Each series has just one observation: it must still render a visible mark.
    await expect(performance.locator(".recharts-line-dots circle")).toHaveCount(3)
    for (const dot of await performance.locator(".recharts-line-dots circle").all()) {
      await expect(dot).toBeVisible()
      await expect(dot).not.toHaveCSS("fill", "rgb(255, 255, 255)")
    }
    await page.screenshot({
      path: testInfo.outputPath("admin-booking-conversion.png"),
      fullPage: true,
    })
    await page.getByRole("button", { name: "Duration", exact: true }).click()
    await expect(performance.getByText("6m 30s", { exact: true })).toBeVisible()
    await expect(
      page.getByRole("button", { name: "Duration", exact: true }),
    ).toHaveAttribute("aria-pressed", "true")
    await expect(performance.locator(".recharts-line-dots circle")).toHaveCount(3)
    for (const dot of await performance.locator(".recharts-line-dots circle").all()) {
      await expect(dot).toBeVisible()
      await expect(dot).not.toHaveCSS("fill", "rgb(255, 255, 255)")
    }
    await page.screenshot({
      path: testInfo.outputPath("admin-booking-duration.png"),
      fullPage: true,
    })

    const endpoint = `${process.env.E2E_PORTAL_API_URL}/v1/analytics/bookings/query`
    await page.route(endpoint, (route) => route.abort(), { times: 1 })
    await page.getByRole("button", { name: "30 days", exact: true }).click()
    await expect(
      page.getByText("Analytics couldn’t load", { exact: true }),
    ).toBeVisible()
    await page.getByRole("button", { name: "Retry", exact: true }).click()
    await expect(
      page.getByRole("region", { name: "Booking performance", exact: true }),
    ).toBeVisible()

    const staffContext = await browser.newContext()
    const staff = await staffContext.newPage()
    await signInAs(staff, "selected@abita.test", "Fixture Staff")
    await expect(staff.getByTestId("mounted-workspace")).toBeVisible()
    await expect(
      staff.getByRole("button", { name: "Analytics", exact: true }),
    ).toHaveCount(0)
    await staffContext.close()
  } finally {
    await client.query(
      "DELETE FROM ai_interactions WHERE id = ANY($1::uuid[])",
      [ids],
    )
    await client.end()
  }
})
