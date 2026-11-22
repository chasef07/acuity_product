import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("diagnostic scorecards connect intent and outcome to failures without hiding unknown checks", async ({ page }, testInfo) => {
  const databaseURL = process.env.E2E_DATABASE_URL
  test.skip(!databaseURL, "E2E_DATABASE_URL is required")
  if (!new URL(databaseURL!).pathname.endsWith("_e2e")) throw new Error("Disposable E2E database required")
  const client = new Client({ connectionString: databaseURL })
  await client.connect()
  const id = randomUUID()
  const passingID = randomUUID()
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  try {
    const { rows: [scope] } = await client.query("SELECT l.id,l.practice_id FROM access_locations l JOIN access_practices p ON p.id=l.practice_id WHERE p.provisioning_key='abita-eye-group' AND l.provisioning_key='fixture-location-1'")
    const judged = ["office_question_answered", "call_ended_correctly", "transfer_policy_followed", "emergency_policy_followed"]
    const results: Record<string, unknown> = Object.fromEntries(judged.map((key) => [key, { verdict: key !== "transfer_policy_followed", probability: key === "transfer_policy_followed" ? 0.1 : 0.9, votes: { a: 0.9, b: 0.9, c: 0.9 } }]))
    results.knowledge_available = { status: "unknown", reason: "no_office_knowledge_result" }
    results.appointment_datetime_correct = { status: "not_applicable", reason: "no_appointment_action" }
    await client.query(
      `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
       VALUES($1,'scorecard-flow-e2e',$2,$3,$1::uuid::text,'+15555550887','+17275550101',now()-interval '1 hour',now()-interval '58 minutes','COMPLETED',3,$4,$5)`,
      [id, scope.practice_id, scope.id, { items: [{ type: "message", role: "user", content: ["Synthetic staff request."] }] }, { evaluation: { evaluatorVersion: "typesafe-scorecard-v8", evaluator: "jury", status: "complete", jurors: ["a", "b", "c"], results } }],
    )
    await client.query("UPDATE ai_interaction_classifications SET status='complete',intent='speak_to_staff',outcome='resolved' WHERE interaction_id=$1", [id])
    for (const key of judged) await client.query(
      `INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES($1,$2,$3,'judge',$4,'typesafe-scorecard-v8','a,b,c',now())`,
      [id, scope.practice_id, key, key !== "transfer_policy_followed"],
    )
    const passedResults = { ...results, knowledge_available: { status: "not_applicable" }, transfer_policy_followed: { verdict: true, probability: 0.9, votes: { a: 0.9, b: 0.9, c: 0.9 } } }
    await client.query(
      `INSERT INTO ai_interactions(id,service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
       VALUES($1,'scorecard-flow-e2e',$2,$3,$1::uuid::text,'+15555550888','+17275550101',now()-interval '6 minutes',now()-interval '4 minutes','COMPLETED',3,$4,$5)`,
      [passingID, scope.practice_id, scope.id, { items: [{ type: "message", role: "user", content: ["Synthetic office question."] }] }, { evaluation: { evaluatorVersion: "typesafe-scorecard-v8", evaluator: "jury", status: "complete", jurors: ["a", "b", "c"], results: passedResults } }],
    )
    await client.query("UPDATE ai_interaction_classifications SET status='complete',intent='appointment_information',outcome='resolved' WHERE interaction_id=$1", [passingID])
    for (const key of judged) await client.query(`INSERT INTO ai_interaction_scorecard_answers(interaction_id,practice_id,question,source,answer,scorecard_version,judge_model,computed_at) VALUES($1,$2,$3,'judge',true,'typesafe-scorecard-v8','a,b,c',now())`, [passingID, scope.practice_id, key])
    await signInAs(page, "founder@acuity.test", "Synthetic Operator")
    await page.getByRole("button", { name: "Command Center", exact: true }).click()
    await page.getByRole("button", { name: "Workspace selector" }).click()
    await page.getByRole("button", { name: "Fixture Location 1", exact: true }).click()
    const diagnostics = page.getByRole("region", { name: "Command Center" })
    await diagnostics.getByRole("combobox", { name: "Date range", exact: true }).click()
    await page.getByRole("option", { name: "Last 7 days", exact: true }).click()
    const navigation = page.getByRole("navigation", { name: "Command Center", exact: true })
    await expect(navigation.getByRole("button")).toHaveText(["Calls", "Summary", "Breakdowns", "Tools", "Cost", "Judges"])
    await expect(navigation.getByRole("button", { name: "Calls", exact: true })).toHaveAttribute("aria-current", "page")
    await expect(diagnostics.getByRole("combobox", { name: "Judge flags", exact: true })).toContainText("Any judge failure")
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(1)
    const filter = async (name: string, option: string) => {
      await diagnostics.getByRole("combobox", { name, exact: true }).click()
      await page.getByRole("option", { name: option, exact: true }).click()
    }
    await filter("Judge flags", "Emergency policy followed")
    await expect(diagnostics.getByText("No calls match these filters", { exact: true })).toBeVisible()
    await filter("Judge flags", "Transfer policy followed")
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(1)
    await filter("Intent", "Insurance question")
    await expect(diagnostics.getByText("No calls match these filters", { exact: true })).toBeVisible()
    await filter("Intent", "Speak to staff")
    await filter("Outcome", "Abandoned")
    await expect(diagnostics.getByText("No calls match these filters", { exact: true })).toBeVisible()
    await filter("Outcome", "Resolved")
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(1)
    await filter("Intent", "All intents")
    await filter("Judge flags", "All calls")
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(2)
    await diagnostics.getByRole("row").filter({ hasText: "(555) 555-0888" }).getByRole("button", { name: /Open analytics/ }).click()
    const passing = page.getByRole("dialog", { name: "AI call evidence" }).getByLabel("Intent to outcome scorecard")
    await expect(passing.getByLabel("Check result")).toContainText("Pass")
    await expect(passing.getByText("All applicable checks passed", { exact: true })).toBeVisible()
    await passing.screenshot({ path: testInfo.outputPath("passed-flow.png") })
    await page.setViewportSize({ width: 390, height: 844 })
    const longIntent = passing.getByText("Appointment confirmation/details", { exact: true })
    await expect(longIntent).toBeVisible()
    expect(await longIntent.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await passing.screenshot({ path: testInfo.outputPath("passed-flow-mobile.png") })
    await page.setViewportSize({ width: 1440, height: 1000 })
    await page.keyboard.press("Escape")
    await filter("Judge flags", "Any judge failure")
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(1)
    await page.screenshot({ path: testInfo.outputPath("control-center.png"), fullPage: true })
    await diagnostics.getByRole("row").filter({ hasText: "(555) 555-0887" }).getByRole("button", { name: /Open analytics/ }).click()
    const evidence = page.getByRole("dialog", { name: "AI call evidence" })
    const flow = evidence.getByLabel("Intent to outcome scorecard")
    await expect(flow.getByText("Speak to staff", { exact: true })).toBeVisible()
    await expect(flow.getByText("Resolved", { exact: true })).toBeVisible()
    await expect(flow.getByText("1 failed check", { exact: true })).toBeVisible()
    await expect(flow.locator("details")).not.toHaveAttribute("open")
    await expect(flow.getByLabel("Check result")).toContainText("Fail")
    await flow.screenshot({ path: testInfo.outputPath("diagnostic-flow.png") })
    await flow.getByLabel("Review judge checks", { exact: true }).click()
    await expect(flow.getByText("Unknown", { exact: true })).toBeVisible()
    await expect(flow.getByText("Not applicable", { exact: true })).toBeVisible()
    await flow.getByLabel("Review judge checks", { exact: true }).click()
    await page.setViewportSize({ width: 390, height: 844 })
    expect(await flow.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    await flow.screenshot({ path: testInfo.outputPath("diagnostic-flow-mobile.png") })
    await page.keyboard.press("Escape")
    await page.setViewportSize({ width: 1440, height: 1000 })
    await expect(diagnostics.getByRole("table").locator("tbody tr")).toHaveCount(1)
    const previousVersion = Number(await page.getByTestId("mounted-workspace").getAttribute("data-workspace-version"))
    await client.query("BEGIN")
    await client.query("UPDATE ai_interactions SET closeout_payload=jsonb_set(closeout_payload,'{evaluation,results}',$2::jsonb) WHERE id=$1", [id, JSON.stringify(passedResults)])
    await client.query("UPDATE ai_interaction_scorecard_answers SET answer=true WHERE interaction_id=$1", [id])
    await client.query("UPDATE ai_interaction_classifications SET status='complete',intent='speak_to_staff',outcome='resolved' WHERE interaction_id=$1", [id])
    await client.query("UPDATE access_practices SET workspace_version=workspace_version+1 WHERE id=$1", [scope.practice_id])
    await client.query("SELECT pg_notify('acuity_workspace_hints', json_build_object('practiceId',id,'version',workspace_version)::text) FROM access_practices WHERE id=$1", [scope.practice_id])
    await client.query("COMMIT")
    await expect.poll(async () => Number(await page.getByTestId("mounted-workspace").getAttribute("data-workspace-version"))).toBeGreaterThan(previousVersion)
    await expect(diagnostics.getByText("No calls match these filters", { exact: true })).toBeVisible()
    expect(errors).toEqual([])
  } finally {
    await client.query("ROLLBACK")
    await client.query("DELETE FROM ai_interactions WHERE id=ANY($1::uuid[])", [[id, passingID]])
    await client.end()
  }
})
