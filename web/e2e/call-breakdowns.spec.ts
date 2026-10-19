import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { selectCommandCenterPage, signInAs } from "./support"

test("call breakdowns use the full selection and keep missing labels and judge coverage visible", async ({ page }, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e")) throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids: string[] = []
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const { rows: [scope] } = await client.query("SELECT l.id,l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'")
    await client.query("BEGIN")
    for (let index = 0; index < 70; index++) {
      const id = randomUUID()
      ids.push(id)
      await client.query(
        `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
         VALUES($1,'breakdowns-e2e',$2,$3,$1::uuid::text,'+15555550199','+17275550106',now()-interval '5 minutes'-$5*interval '1 day',now()-interval '3 minutes'-$5*interval '1 day','COMPLETED',3,$4,'{}')`,
        [id, scope.practice_id, scope.id, { items: [{ type: "message", role: "user", content: ["Synthetic appointment request."] }] }, index % 7],
      )
      if (index < 65) await client.query("UPDATE ai_interaction_classifications SET status='complete',intent=$2,outcome=$3 WHERE interaction_id=$1", [id, index < 35 ? "existing_patient_booking" : index < 55 ? "office_information" : "insurance_question", index < 20 || (index >= 35 && index < 50) ? "resolved" : index < 30 || (index >= 50 && index < 62) ? "transferred" : "abandoned"])
      if (index < 60) await client.query(
        `INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES($1,$2,'office_question_answered','judge',$3,'typesafe-scorecard-v8','synthetic',now())`,
        [id, scope.practice_id, index >= 8],
      )
      if (index < 10) await client.query(
        `INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES($1,$2,'call_ended_correctly','judge',$3,'typesafe-scorecard-v8','synthetic',now())`,
        [id, scope.practice_id, index >= 4],
      )
    }
    await client.query("COMMIT")
    await page.setViewportSize({ width: 1440, height: 1000 })
    await signInAs(page, "founder@acuity.test", "Synthetic Operator")
    await selectCommandCenterPage(page, "Breakdowns")
    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 6", exact: true }).click()
    await page.getByRole("combobox", { name: "Date range", exact: true }).click()
    await page.getByRole("option", { name: "Last 7 days", exact: true }).click()
    const command = page.getByRole("region", { name: "Command Center", exact: true })
    const flow = command.getByRole("region", { name: "Call flow", exact: true })
    const chips = command.getByRole("group", { name: "Selection", exact: true })
    const gaps = command.getByRole("group", { name: "Biggest gaps", exact: true })
    const ledger = command.locator("section[aria-labelledby=call-ledger-title]")
    await expect(command.getByRole("combobox", { name: "Intent", exact: true })).toHaveCount(0)
    await expect(command.getByRole("button", { name: "Judge failures", exact: true })).toHaveCount(0)
    await expect(chips).toContainText("ShowingAll calls")
    await expect(flow.getByText("All calls 70", { exact: true })).toBeVisible()
    await expect(gaps.getByRole("button")).toHaveText([
      "Existing patient booking · Failed: Office question answered correctly · 23%",
      "Existing patient booking · Abandoned · 14%",
      "Existing patient booking · Failed: Call ended correctly · 11%",
    ])
    await expect(ledger).toContainText("50 shown")
    await page.screenshot({ path: testInfo.outputPath("breakdowns-all.png"), fullPage: true, animations: "disabled" })
    await flow.screenshot({ path: testInfo.outputPath("breakdowns-flow.png"), animations: "disabled" })

    await gaps.getByRole("button").first().click()
    await expect(chips).toContainText("Intent: Existing patient booking")
    await expect(chips).toContainText("Failed: Office question answered correctly")
    await expect(chips).toContainText("8 calls")
    await expect(ledger).toContainText("8 shown")
    await flow.screenshot({ path: testInfo.outputPath("breakdowns-flow-selected.png"), animations: "disabled" })
    await page.screenshot({ path: testInfo.outputPath("breakdowns-gap-calls.png"), fullPage: true, animations: "disabled" })

    await flow.getByRole("button", { name: "Select Existing patient booking to Abandoned" }).click()
    await expect(chips).toContainText("Intent: Existing patient booking")
    await expect(chips).toContainText("Outcome: Abandoned")
    await expect(ledger).toContainText("5 shown")
    await flow.getByRole("button", { name: "Select Insurance question", exact: true }).click()
    await expect(chips).toContainText("Intent: Insurance question")
    await expect(chips).not.toContainText("Outcome:")
    await expect(ledger).toContainText("10 shown")

    await flow.getByRole("radio", { name: "List" }).or(flow.getByRole("button", { name: "List", exact: true })).click()
    await expect(flow.locator('section[aria-label="Outcomes"]')).toContainText("Transferred7 calls70%")
    await flow.locator('section[aria-label="Outcomes"]').getByRole("button", { name: "Select Abandoned" }).click()
    await expect(chips).toContainText("Outcome: Abandoned")
    await expect(ledger).toContainText("3 shown")
    await page.screenshot({ path: testInfo.outputPath("breakdowns-list.png"), fullPage: true, animations: "disabled" })
    await chips.getByRole("button", { name: "Remove Outcome: Abandoned" }).click()
    await flow.locator('section[aria-label="Judge results"]').getByRole("button", { name: "Select Pass" }).click()
    await expect(chips).toContainText("Judge: Pass")
    await expect(ledger).toContainText("5 shown")
    await chips.getByRole("button", { name: "Clear", exact: true }).click()
    await expect(chips).toContainText("ShowingAll calls")

    await page.setViewportSize({ width: 390, height: 1150 })
    await expect(flow.getByText("All calls 70", { exact: true })).toBeHidden()
    await expect(flow.locator('section[aria-label="Intents"]')).toBeVisible()
    expect(await command.locator("main").evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await expect(flow.locator('section[aria-label="Intents"]')).toContainText("35 calls")
    await page.screenshot({ path: testInfo.outputPath("breakdowns-mobile.png"), fullPage: true, animations: "disabled" })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await expect(page.getByRole("navigation", { name: "Current location" })).toHaveText("Command CenterBreakdowns")

    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 5", exact: true }).click()
    await expect(command.getByText(/No calls in this date range/)).toBeVisible()
    expect(errors).toEqual([])
  } finally {
    await client.query("ROLLBACK")
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [ids])
    await client.end()
  }
})
