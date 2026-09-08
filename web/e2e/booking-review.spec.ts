import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("operator reviews exact non-conversions, persists buckets, and exports synthetic scenario drafts", async ({
  page,
}) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids = Array.from({ length: 5 }, () => randomUUID())
  try {
    const scope = (
      await client.query(
        "SELECT l.id,l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'",
      )
    ).rows[0]
    for (let i = 0; i < ids.length; i++) {
      const start = new Date(Date.now() - 3 * 86400000 + i * 1000)
      const booked = i === 0
      const searched = i !== 3
      const finished = i !== 4
      const items = [
        {
          type: "message",
          role: "user",
          content: [
            "I am a fictional caller looking for a morning appointment.",
          ],
        },
        ...(searched
          ? [
              {
                type: "function_call",
                name: "get_availability",
                call_id: "search",
              },
              {
                type: "function_call_output",
                name: "get_availability",
                call_id: "search",
                output: '{"slots":[]}',
                is_error: false,
              },
            ]
          : []),
        {
          type: "message",
          role: "assistant",
          content: [
            booked
              ? "Your synthetic appointment is booked."
              : "There are no matching morning slots. I can check another day.",
          ],
        },
      ]
      await client.query(
        `INSERT INTO ai_interactions (id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,appointment_outcome,new_appointment_id,booking_result,transcript,closeout_payload)
        VALUES ($1::uuid,'review-e2e',$2,$3,$1::uuid::text,$13,'+17275550106',$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
        [
          ids[i],
          scope.practice_id,
          scope.id,
          start,
          finished ? new Date(start.getTime() + 180000) : null,
          finished ? "COMPLETED" : "IN_PROGRESS",
          finished ? 3 : 1,
          booked ? "BOOKING" : "INDETERMINATE",
          booked ? `synthetic-${ids[i]}` : null,
          booked
            ? { status: "booked", appointmentId: `synthetic-${ids[i]}` }
            : null,
          { items },
          {
            domainOutcomes: [
              {
                outcome: i === 2 ? "patient_verified" : "patient_new",
                status: "success",
              },
            ],
          },
          `+155555501${String(i).padStart(2, "0")}`,
        ],
      )
    }
    await signInAs(page, "founder@acuity.test", "Fixture Operator")
    await page.goto(`/workspace/non-conversions?practice=${scope.practice_id}`)
    await expect(
      page.getByRole("heading", { name: "Non-converting calls", exact: true }),
    ).toBeVisible()
    const callList = page.getByRole("region", {
      name: "Non-converting call list",
    })
    await expect(
      callList.getByRole("button", { name: /Review call/ }),
    ).toHaveCount(2)
    await callList
      .getByRole("button", { name: "Review call 1", exact: true })
      .click()
    await expect(
      page.getByRole("heading", { name: "AI call evidence", exact: true }),
    ).toBeVisible()
    await expect(
      page.getByText(
        "I am a fictional caller looking for a morning appointment.",
        { exact: true },
      ),
    ).toBeVisible()
    await expect(
      page.getByLabel("Caller phone number", { exact: true }),
    ).toHaveText("(555) 555-0102")
    await page
      .getByLabel("New or existing failure tag")
      .fill("get_availability")
    await page
      .getByRole("button", { name: "Create & tag", exact: true })
      .click()
    await expect(
      page.getByRole("button", {
        name: "Remove tag get_availability",
        exact: true,
      }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(
      page.getByRole("heading", { name: "AI call evidence", exact: true }),
    ).toBeHidden()
    await callList
      .getByRole("button", { name: "Review call 2", exact: true })
      .click()
    await expect(
      page.getByRole("heading", { name: "AI call evidence", exact: true }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    await callList
      .getByRole("button", { name: "Review call 2", exact: true })
      .click()
    await page
      .getByRole("button", { name: "Apply tag get_availability", exact: true })
      .click()
    await expect(
      page.getByRole("button", {
        name: "Remove tag get_availability",
        exact: true,
      }),
    ).toBeVisible()
    await page
      .getByRole("button", { name: "Remove tag get_availability", exact: true })
      .click()
    await expect(
      page.getByRole("button", {
        name: "Apply tag get_availability",
        exact: true,
      }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    await page.reload()
    await page
      .getByRole("combobox", { name: "Filter by failure tag", exact: true })
      .click()
    await page
      .getByRole("option", { name: "get_availability", exact: true })
      .click()
    await expect(
      callList.getByRole("button", { name: /Review call/ }),
    ).toHaveCount(1)
    await callList
      .getByRole("button", { name: "Review call 1", exact: true })
      .click()
    await expect(
      page.getByRole("button", {
        name: "Remove tag get_availability",
        exact: true,
      }),
    ).toBeVisible()
    await page.keyboard.press("Escape")
    const savedTagsBefore = await page.evaluate(() =>
      Object.entries(localStorage).filter(([key]) =>
        key.startsWith("acuity:call-tags:v1:"),
      ),
    )
    await expect(
      page.getByRole("combobox", {
        name: "Patient status for call 1",
        exact: true,
      }),
    ).toContainText("Existing")
    await page
      .getByRole("combobox", { name: "Patient status for call 1", exact: true })
      .click()
    await page.getByRole("option", { name: "New", exact: true }).click()
    await expect(
      page.getByRole("heading", { name: "AI call evidence", exact: true }),
    ).toBeHidden()
    await expect(
      page.getByText("Original: Existing · local correction", { exact: true }),
    ).toBeVisible()
    await page.reload()
    await expect(
      page.getByRole("combobox", {
        name: "Patient status for call 1",
        exact: true,
      }),
    ).toContainText("New")
    expect(
      await page.evaluate(() =>
        Object.entries(localStorage).filter(([key]) =>
          key.startsWith("acuity:call-tags:v1:"),
        ),
      ),
    ).toEqual(savedTagsBefore)
    await callList
      .getByRole("button", { name: "Review call 1", exact: true })
      .click()
    const statusCorrection = page.getByRole("region", {
      name: "Patient status correction",
    })
    await expect(
      statusCorrection.getByRole("combobox", {
        name: "Patient status",
        exact: true,
      }),
    ).toContainText("New")
    await expect(
      page.getByRole("button", {
        name: "Remove tag get_availability",
        exact: true,
      }),
    ).toBeVisible()
    await statusCorrection.getByRole("combobox").click()
    await page.getByRole("option", { name: "Unknown", exact: true }).click()
    await expect(statusCorrection.getByRole("combobox")).toContainText(
      "Unknown",
    )
    await statusCorrection
      .getByRole("button", { name: "Use original", exact: true })
      .click()
    await expect(statusCorrection.getByRole("combobox")).toContainText(
      "Existing",
    )
    await page.keyboard.press("Escape")
    await expect(
      page.getByRole("combobox", {
        name: "Patient status for call 1",
        exact: true,
      }),
    ).toContainText("Existing")
    expect(
      await page.evaluate(() =>
        Object.entries(localStorage).filter(([key]) =>
          key.startsWith("acuity:call-tags:v1:"),
        ),
      ),
    ).toEqual(savedTagsBefore)
    await page.goto("/workspace")
    await page.getByRole("button", { name: "Analytics", exact: true }).click()
    await page.getByRole("button", { name: "7 days", exact: true }).click()
    await page.getByRole("combobox", { name: "Office", exact: true }).click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()
    await page
      .getByRole("button", {
        name: "Review non-converting calls (2)",
        exact: true,
      })
      .click()
    await expect(
      page.getByText("2 calls to review", { exact: true }),
    ).toBeVisible()
    await expect(
      page.getByText("1 converted / 3 availability calls · 33.3%", {
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      page
        .getByRole("navigation", { name: "Calls to review" })
        .getByRole("button"),
    ).toHaveCount(2)
    await expect(
      page.getByText(
        "I am a fictional caller looking for a morning appointment.",
        { exact: true },
      ),
    ).toBeVisible()
    await page.getByRole("combobox", { name: "Failure bucket" }).click()
    await page
      .getByRole("option", { name: "No suitable availability", exact: true })
      .click()
    await page
      .getByLabel("Evidence / pattern notes")
      .fill("PRIVATE SYNTHETIC EVIDENCE excluded from export")
    await page.getByText("Draft a LiveKit scenario", { exact: true }).click()
    await page
      .getByRole("button", { name: "Use bucket starter", exact: true })
      .click()
    await page
      .getByRole("button", { name: "Save review locally", exact: true })
      .click()
    await expect(
      page.getByText("Review saved in this browser.", { exact: true }),
    ).toBeVisible()
    await expect(
      page.getByText("1 / 2 reviewed", { exact: true }),
    ).toBeVisible()
    const downloadPromise = page.waitForEvent("download")
    await page
      .getByRole("button", { name: "Export scenario drafts (1)", exact: true })
      .click()
    const download = await downloadPromise
    const stream = await download.createReadStream()
    const chunks = []
    for await (const chunk of stream!) chunks.push(chunk)
    const content = Buffer.concat(chunks).toString()
    expect(content).toContain("A new caller wants an appointment this week")
    expect(content).not.toContain("PRIVATE SYNTHETIC EVIDENCE")
    expect(content).not.toContain("fictional caller looking for a morning")
    for (const id of ids) expect(content).not.toContain(id)
    await page
      .getByRole("button", { name: "Back to report", exact: true })
      .click()
    await page
      .getByRole("button", {
        name: "Review non-converting calls (2)",
        exact: true,
      })
      .click()
    await expect(
      page.getByText("1 / 2 reviewed", { exact: true }),
    ).toBeVisible()
    await expect(page.getByLabel("Evidence / pattern notes")).toHaveValue(
      "PRIVATE SYNTHETIC EVIDENCE excluded from export",
    )
    await page
      .getByRole("button", { name: "Unreviewed 1", exact: true })
      .click()
    await expect(
      page
        .getByRole("navigation", { name: "Calls to review" })
        .getByRole("button"),
    ).toHaveCount(1)
    await page
      .getByLabel("Evidence / pattern notes")
      .fill("Unsaved synthetic note")
    page.once("dialog", (dialog) => dialog.dismiss())
    await page
      .getByRole("button", { name: "Back to report", exact: true })
      .click()
    await expect(
      page.getByRole("region", { name: "Non-conversion review" }),
    ).toBeVisible()
  } finally {
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [
      ids,
    ])
    await client.end()
  }
})
