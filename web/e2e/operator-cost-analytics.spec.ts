import { randomUUID } from "node:crypto"
import { Client } from "pg"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

for (const stack of ["legacy", "live"] as const) {
test(`Platform Operator sees ${stack} costs with reconciling item shares`, async ({
  page,
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
    const usage = stack === "live" ? [
 { type: "llm_usage", provider: "api.openai.com", model: "gpt-live-1", session_duration: 600 },
 { type: "llm_usage", provider: "api.openai.com", model: "gpt-6-luna", input_tokens: 100000, input_cached_tokens: 25000, input_cache_creation_tokens: 10000, output_tokens: 10000 },
 ] : [
      {
        type: "llm_usage",
        provider: "livekit",
        model: "google/gemma-4-31b-it",
        input_tokens: 1_000_000,
        input_cached_tokens: 250_000,
        output_tokens: 100_000,
      },
      {
        type: "stt_usage",
        provider: "livekit",
        model: "assemblyai/universal-3-5-pro",
        audio_duration: 600,
      },
      {
        type: "tts_usage",
        provider: "rime",
        model: "coda",
        characters_count: 10_000,
      },
    ]
    for (let index = 0; index < 2; index++) {
      const id = randomUUID()
      ids.push(id)
      const start = new Date(Date.now() - (index + 1) * 86_400_000)
      await client.query(
        `INSERT INTO ai_interactions
          (id, service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage, appointment_outcome, transcript, closeout_payload)
         VALUES ($1::uuid, 'cost-e2e', $2, $3, $1::uuid::text, '+15555550199', '+17275550106', $4, $5, 'COMPLETED', 3, 'INDETERMINATE', $6, '{}'::jsonb)`,
        [
          id,
          scope.rows[0].practice_id,
          scope.rows[0].id,
          start,
          new Date(start.getTime() + 600_000),
          { usage },
        ],
      )
    }
    await signInAs(page, "founder@acuity.test", "Fixture Founder")
    await page
      .getByRole("button", { name: "AI diagnostics", exact: true })
      .click()
    const diagnostics = page.getByRole("region", { name: "AI call analytics" })
    await diagnostics.getByRole("button", { name: "Cost", exact: true }).click()
    await diagnostics
      .getByRole("combobox", { name: "Office", exact: true })
      .click()
    await page
      .getByRole("option", { name: "Fixture Location 6", exact: true })
      .click()

    const overview = diagnostics.getByRole("region", {
      name: "AI cost overview",
    })
    await expect(
      overview.getByRole("status", { name: "Estimated cost" }),
    ).toHaveText(stack === "live" ? "$1.30" : "$2.36")
    await expect(overview.getByText(stack === "live" ? "$0.6480" : "$1.18", { exact: true })).toBeVisible()
    await expect(overview.getByText(stack === "live" ? "$0.0648" : "$0.1180", { exact: true })).toBeVisible()
    await expect(
      overview.getByText("Cache hit rate · 25.0%", {
        exact: true,
      }),
    ).toBeVisible()
    await expect(overview.locator(".recharts-bar")).toBeVisible()

    const breakdown = diagnostics.getByRole("region", {
      name: "Cost breakdown",
    })
    const expected = stack === "live" ? [
 ["GPT Live 1 · voice", "$1.00", "77.2%"],
 ["GPT-6 Luna · uncached input", "$0.0130", "1.0%"],
 ["GPT-6 Luna · cached input", "$0.0005", "0.0%"],
 ["GPT-6 Luna · cache writes", "$0.0025", "0.2%"],
 ["GPT-6 Luna · output", "$0.0100", "0.8%"],
 ["LiveKit · media", "$0.2000", "15.4%"],
 ["Telnyx · inbound SIP", "$0.0700", "5.4%"],
 ] : [
      ["Gemma 4 31B IT · uncached input", "$0.6000", "25.4%"],
      ["Gemma 4 31B IT · cached input", "$0.1000", "4.2%"],
      ["Gemma 4 31B IT · output", "$0.2400", "10.2%"],
      ["AssemblyAI · Universal 3.5 Pro", "$0.1500", "6.4%"],
      ["Rime · Coda", "$1.00", "42.4%"],
      ["LiveKit · media", "$0.2000", "8.5%"],
      ["Telnyx · inbound SIP", "$0.0700", "3.0%"],
    ]
    for (const [label, cost, share] of expected) {
      const row = breakdown.getByRole("row").filter({ hasText: label })
      await expect(row).toContainText(cost)
      await expect(row).toContainText(share)
    }
    const total = breakdown.getByRole("row").filter({ hasText: /^Total/ })
    await expect(total).toContainText(stack === "live" ? "$1.30" : "$2.36")
    if (stack === "live") {
      await expect(breakdown.getByRole("row").filter({ hasText: "cache writes" }).first()).toContainText("$0.125 / 1M tokens")
      await expect(breakdown.getByRole("row").filter({ hasText: "Gemma" })).toHaveCount(0)
    }
    await expect(total).toContainText("100.0%")
    await page.screenshot({
      path: testInfo.outputPath("operator-cost-analytics.png"),
      fullPage: true,
    })
    if (stack === "live") {
      await client.query(
        "UPDATE ai_interactions SET transcript=jsonb_build_object('usage', $2::jsonb) WHERE id = ANY($1::uuid[])",
        [ids, JSON.stringify([usage[0]])],
      )
      await page.getByRole("button", { name: "Last 30 days", exact: true }).click()
      await expect(overview.getByText("Recorded estimated cost", { exact: true })).toBeVisible()
      await expect(overview.getByRole("status", { name: "Estimated cost" })).toHaveText("$1.27")
      await expect(overview.getByText(/Averages use the 0 of 2 calls/)).toBeVisible()
      await expect(breakdown.getByRole("row").filter({ hasText: "GPT-6 Luna · output" })).toContainText("—")
      await breakdown.getByRole("row").filter({ hasText: "GPT-6 Luna · cache writes" }).scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath("operator-cost-missing-usage.png") })
    }
  } finally {
    await client.query(
      "DELETE FROM ai_interactions WHERE id = ANY($1::uuid[])",
      [ids],
    )
    await client.end()
  }
})

}
