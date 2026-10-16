import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { openAgentPage, signInAs } from "./support"

test("intent and outcome labels filter customer transcripts and AI diagnostics", async ({ page }, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e"))
    throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids = [randomUUID(), randomUUID(), randomUUID()]
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const { rows: [scope] } = await client.query(
      "SELECT l.id,l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-1'",
    )
    await client.query("BEGIN")
    for (const [index, id] of ids.entries()) {
      await client.query(
        `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
         VALUES($1,'call-labels-e2e',$2,$3,$1::uuid::text,$4,'+17275550101',now()-interval '5 minutes',now()-interval '3 minutes','COMPLETED',3,$5,'{}')`,
        [id, scope.practice_id, scope.id, `+1555555098${index}`, { items: [
          { type: "message", role: "user", content: ["I am an existing patient and would like to book an appointment."] },
          { type: "message", role: "assistant", content: ["Your appointment is booked for Friday."] },
        ] }],
      )
      await client.query(
        `UPDATE ai_interaction_classifications SET status='complete',intent=$2,outcome=$3 WHERE interaction_id=$1`,
        [id, index === 2 ? "insurance_question" : "existing_patient_booking", index === 1 ? "abandoned" : "resolved"],
      )
    }
    await client.query("COMMIT")
    await signInAs(page, "selected@abita.test", "Synthetic Staff")
    await openAgentPage(page, "Transcripts")
    await page.getByRole("textbox", { name: "Search phone number" }).fill("555555098")
    const rows = page.getByRole("table").locator("tbody tr")
    await expect(rows).toHaveCount(3)
    await page.getByRole("combobox", { name: "Intent", exact: true }).click()
    await page.getByRole("option", { name: "Existing patient booking", exact: true }).click()
    await expect(rows).toHaveCount(2)
    await page.getByRole("combobox", { name: "Outcome", exact: true }).click()
    await page.getByRole("option", { name: "Resolved", exact: true }).click()
    await expect(rows).toHaveCount(1)
    await expect(rows).toContainText("(555) 555-0980")
    await expect(async () => {
      await expect(page.getByRole("table")).toBeVisible()
      await page.screenshot({ path: testInfo.outputPath("customer-label-filters.png"), fullPage: true })
      await expect(page.getByRole("table")).toBeVisible()
    }).toPass()
    await rows.getByRole("button", { name: /Open call/ }).click()
    const transcript = page.getByRole("dialog")
    await expect(transcript.getByText("Existing patient booking", { exact: true })).toBeVisible()
    await expect(transcript.getByText("Resolved", { exact: true })).toBeVisible()
    await expect(transcript.getByText("Your appointment is booked for Friday.", { exact: true })).toBeVisible()
    await page.keyboard.press("Escape")

    await signInAs(page, "founder@acuity.test", "Synthetic Acuity Operator")
    await page.getByRole("button", { name: "AI diagnostics", exact: true }).click()
    const diagnostics = page.getByRole("region", { name: "AI call analytics" })
    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 1", exact: true }).click()
    await diagnostics.getByRole("combobox", { name: "Intent", exact: true }).click()
    await page.getByRole("option", { name: "Existing patient booking", exact: true }).click()
    const diagnosticRows = diagnostics.getByRole("table").locator("tbody tr")
    await expect(diagnosticRows).toHaveCount(2)
    await diagnostics.getByRole("combobox", { name: "Outcome", exact: true }).click()
    await page.getByRole("option", { name: "Abandoned", exact: true }).click()
    await expect(diagnosticRows).toHaveCount(1)
    await expect(diagnosticRows).toContainText("(555) 555-0981")
    await page.screenshot({ path: testInfo.outputPath("diagnostics-label-filters.png"), fullPage: true })
    await diagnosticRows.getByRole("button", { name: /Open analytics for call/ }).click()
    const evidence = page.getByRole("dialog", { name: "AI call evidence" })
    await expect(evidence.getByText("Existing patient booking", { exact: true })).toBeVisible()
    await expect(evidence.getByText("Abandoned", { exact: true })).toBeVisible()
    await page.keyboard.press("Escape")
    await page.setViewportSize({ width: 390, height: 844 })
    await expect(diagnostics.getByRole("combobox", { name: "Intent", exact: true })).toBeInViewport()
    await expect(diagnostics.getByRole("combobox", { name: "Outcome", exact: true })).toBeInViewport()
    await expect(diagnostics.getByText("(555) 555-0981", { exact: true }).last()).toBeVisible()
    await expect(diagnostics.getByText("Abandoned", { exact: true }).last()).toBeVisible()
    await diagnostics.getByRole("combobox", { name: "Outcome", exact: true }).click()
    await page.getByRole("option", { name: "Resolved", exact: true }).click()
    await expect(diagnostics.getByText("(555) 555-0980", { exact: true }).last()).toBeVisible()
    expect(await diagnostics.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath("diagnostics-labels-mobile.png"), fullPage: true })
    expect(errors).toEqual([])
  } finally {
    await client.query("ROLLBACK")
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [ids])
    await client.end()
  }
})
