import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { selectCommandCenterPage, signInAs } from "./support"

test("Judges compares model pairs using shared votes and exposes criteria and quorum", async ({ page }, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e")) throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const ids: string[] = []
  const models = ["typesafe/jev-1.14", "perplexity/pplx-decider-v1.2", "liquid/d1-ultra"]
  const keys = ["knowledge_available", "office_question_answered", "appointment_datetime_correct", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"]
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const { rows: [scope] } = await client.query("SELECT l.id,l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-6'")
    for (let index = 0; index < 14; index++) {
      const id = randomUUID()
      ids.push(id)
      const values = [0.9, index % 3 === 0 ? 0.1 : 0.9, index % 2 === 0 ? 0.1 : 0.9]
      const votes = Object.fromEntries(models.map((model, i) => [model, values[i]]))
      const results = Object.fromEntries(keys.map((key) => [key, { verdict: values.filter((value) => value > 0.4).length >= 2, probability: values.reduce((sum, value) => sum + value, 0) / 3, votes }]))
      const evaluationErrors: Record<string, unknown> = {}
      if (index === 10) {
        delete results.office_question_answered
        evaluationErrors.office_question_answered = { cause: "no_quorum", votes: { [models[0]]: 0.9 }, errors: { [models[1]]: "TimeoutError", [models[2]]: "TimeoutError" } }
      }
      const evaluation = index === 13 ? {} : { evaluator: "jury", evaluatorVersion: index === 12 ? "typesafe-scorecard-v10" : "typesafe-scorecard-v11", jurors: models, status: index === 10 ? "incomplete" : "complete", results: { ...results, ...(index === 11 ? { appointment_datetime_correct: { status: "unknown", reason: "no_confirmed_appointment_action" } } : {}) }, errors: evaluationErrors }
      await client.query(`INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload) VALUES($1,'judges-e2e',$2,$3,$1::uuid::text,'+15555550199','+17275550106',now()-interval '5 minutes',now()-interval '3 minutes','COMPLETED',3,$4,$5)`, [id, scope.practice_id, scope.id, { items: [] }, { evaluation }])
    }
    await page.setViewportSize({ width: 1440, height: 1200 })
    await signInAs(page, "founder@acuity.test", "Synthetic Operator")
    await selectCommandCenterPage(page, "Judges")
    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 6", exact: true }).click()
    const command = page.getByRole("region", { name: "Command Center", exact: true })
    await expect(command.getByRole("region", { name: "Observed jury" })).toContainText("12 of 14 calls")
    await expect(command.getByRole("region", { name: "Observed jury" })).toContainText("2 of 3 valid votes required")
    await command.getByRole("combobox", { name: "Judge", exact: true }).click()
    await page.getByRole("option", { name: "Office question answered correctly", exact: true }).click()
    const agreement = command.getByRole("region", { name: "Model agreement" })
    const pair = agreement.getByRole("row").filter({ hasText: models[0] }).filter({ hasText: models[1] })
    await expect(pair.getByRole("cell").nth(2)).toHaveText("11")
    await expect(pair.getByRole("cell").nth(3)).toHaveText("7 · 63.6%")
    await expect(pair.getByRole("cell").nth(4)).toHaveText("4 · 36.4%")
    await expect(command.getByRole("region", { name: "Judge criteria" })).toContainText("An office question went unanswered")
    await expect(command.getByRole("region", { name: "Model vote coverage" })).toContainText(models[0])
    await page.screenshot({ path: testInfo.outputPath("judges-desktop.png"), fullPage: true, animations: "disabled" })
    await page.setViewportSize({ width: 390, height: 1000 })
    expect(await command.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await page.screenshot({ path: testInfo.outputPath("judges-mobile.png"), fullPage: true, animations: "disabled" })
    expect(errors).toEqual([])
  } finally {
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [ids])
    await client.end()
  }
})
