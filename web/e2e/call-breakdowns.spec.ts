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
    const intents = command.getByRole("region", { name: "Intents", exact: true })
    const outcomes = command.getByRole("region", { name: "Outcomes", exact: true })
    const failures = command.getByRole("region", { name: "Judge failures", exact: true })
    const row = (region: typeof intents, label: string) => region.locator("dl > div").filter({ has: page.getByText(label, { exact: true }) })
    await expect(intents).toContainText("70 calls · share of selected calls")
    await expect(row(intents, "Existing patient booking")).toContainText("35 calls50%")
    await expect(row(intents, "Unlabeled")).toContainText("5 calls7.1%")
    await expect(command.getByRole("img", { name: "Intents over time" })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath("breakdowns-intents.png"), fullPage: true, animations: "disabled" })
    await command.getByRole("button", { name: "Outcomes", exact: true }).click()
    await expect(row(outcomes, "Resolved")).toContainText("35 calls50%")
    await expect(row(outcomes, "Unlabeled")).toContainText("5 calls7.1%")
    await expect(command.getByRole("img", { name: "Outcomes over time" })).toBeVisible()
    await page.screenshot({ path: testInfo.outputPath("breakdowns-outcomes.png"), fullPage: true, animations: "disabled" })
    await command.getByRole("button", { name: "Judge failures", exact: true }).click()
    await expect(failures).toContainText("12 flags across 8 calls")
    await expect(failures).toContainText("60 of 70 calls evaluated · 10 unevaluated")
    await expect(row(failures, "Office question answered correctly")).toContainText("8 calls66.7%")
    await expect(page.getByRole("navigation", { name: "Current location" })).toHaveText("Command CenterBreakdowns")
    await expect(command.getByRole("group", { name: "Show version changes" })).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath("breakdowns-desktop.png"), fullPage: true, animations: "disabled" })
    await page.setViewportSize({ width: 390, height: 1150 })
    await expect(failures).toBeVisible()
    expect(await command.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath("breakdowns-mobile.png"), fullPage: true, animations: "disabled" })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await command.getByRole("button", { name: "Intents", exact: true }).click()
    await intents.getByRole("button", { name: "Explore Existing patient booking" }).click()
    await expect(outcomes).toContainText("35 calls · share of selected calls")
    await expect(row(outcomes, "Resolved")).toContainText("20 calls57.1%")
    await expect(row(outcomes, "Transferred")).toContainText("10 calls28.6%")
    await expect(row(outcomes, "Abandoned")).toContainText("5 calls14.3%")
    await page.screenshot({ path: testInfo.outputPath("intent-outcome-drilldown.png"), fullPage: true, animations: "disabled" })
    await outcomes.getByRole("button", { name: "Explore Resolved" }).click()
    await expect(failures).toContainText("12 flags across 8 calls")
    await expect(failures).toContainText("20 of 20 calls evaluated · 0 unevaluated")
    await expect(command.getByRole("button", { name: "Judge failures", exact: true })).toHaveAttribute("aria-pressed", "true")
    await page.screenshot({ path: testInfo.outputPath("intent-outcome-failures.png"), fullPage: true, animations: "disabled" })
    await command.getByRole("button", { name: "Outcomes", exact: true }).click()
    await expect(outcomes).toContainText("35 calls · share of selected calls")
    await outcomes.getByRole("button", { name: "Explore Transferred" }).click()
    await expect(failures).toContainText("No judge failures recorded")
    await expect(failures).toContainText("10 of 10 calls evaluated")
    await command.getByRole("combobox", { name: "Intent", exact: true }).click()
    await page.getByRole("option", { name: "Office information", exact: true }).click()
    await command.getByRole("combobox", { name: "Outcome", exact: true }).click()
    await page.getByRole("option", { name: "Abandoned", exact: true }).click()
    await expect(command.getByText(/No calls in this selection/)).toBeVisible()
    await command.getByRole("button", { name: "Reset filters", exact: true }).click()
    await expect(intents).toContainText("70 calls · share of selected calls")
    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 5", exact: true }).click()
    await expect(command.getByText(/No calls in this selection/)).toBeVisible()
    await expect(intents).toHaveCount(0)
    expect(errors).toEqual([])
  } finally {
    await client.query("ROLLBACK")
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [ids])
    await client.end()
  }
})
