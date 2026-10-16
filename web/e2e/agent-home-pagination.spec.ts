import { randomUUID } from "node:crypto"
import { expect, test } from "@playwright/test"
import type { AgentCall } from "../src/lib/api/generated/types.gen"
import { signInAs } from "./support"

for (const navigation of ["list", "panel"] as const) {
  test(`Home exposes pagination failures and recovers through the ${navigation}`, async ({ page }, testInfo) => {
    const calls = ["Saturday opening hours", "Parking near the office"].map((query, index): AgentCall => ({
      id: randomUUID(),
      phone: `+1555555010${index}`,
      startedAt: new Date(Date.now() - (index + 1) * 60_000).toISOString(),
      durationSeconds: 60,
      appointmentActions: [],
      transferred: false,
      issueFlagged: false,
      knowledgeGaps: [query],
    }))
    let failNextPage = true
    const cursors: string[] = []
    await page.route("**/v1/agent-calls/query", async (route) => {
      const query = route.request().postDataJSON()
      expect(query.needsKnowledgeOnly).toBe(true)
      if (query.cursor) {
        cursors.push(query.cursor)
        if (failNextPage) {
          await route.fulfill({ status: 503, json: {} })
          return
        }
      }
      await route.fulfill({
        json: query.cursor
          ? { calls: [calls[1]], nextCursor: "" }
          : { calls: [calls[0]], nextCursor: "next-page" },
      })
    })
    for (const call of calls) {
      await page.route(`**/v1/agent-calls/${call.id}`, (route) => route.fulfill({
        json: {
          call,
          locationName: "Synthetic office",
          scorecard: [],
          messages: [{ speaker: "Caller", text: call.knowledgeGaps[0], occurredAt: call.startedAt }],
        },
      }))
    }
    await signInAs(page, "selected@abita.test", "Synthetic Staff", "/workspace")
    await expect(page.getByRole("heading", { level: 1, name: "Acuity" })).toBeVisible()
    await expect(page.getByText("“Saturday opening hours”", { exact: true })).toBeVisible()
    if (navigation === "panel") await page.getByRole("button", { name: "Open call", exact: true }).click()
    const surface = navigation === "panel" ? page.getByRole("dialog") : page.getByRole("region", { name: "Needs knowledge" })
    const next = surface.getByRole("button", { name: navigation === "panel" ? "Next call" : "Load more calls", exact: true })
    await next.click()
    await expect(surface.getByRole("alert")).toContainText("More calls could not be loaded. Try again.")
    await expect(next).toBeEnabled()
    await page.screenshot({ path: testInfo.outputPath(`home-${navigation}-failure.png`), fullPage: true, animations: "disabled" })
    failNextPage = false
    await next.click()
    await expect(surface.getByRole("alert")).toHaveCount(0)
    expect(cursors).toEqual(["next-page", "next-page"])
    if (navigation === "panel") {
      await expect(surface.getByText("Call 2 of 2", { exact: true })).toBeVisible()
      await expect(surface.getByText("Parking near the office", { exact: true })).toBeVisible()
      await expect(next).toBeDisabled()
    } else {
      await expect(surface.getByText("“Saturday opening hours”", { exact: true })).toBeVisible()
      await expect(surface.getByText("“Parking near the office”", { exact: true })).toBeVisible()
      await expect(next).toHaveCount(0)
    }
    await page.screenshot({ path: testInfo.outputPath(`home-${navigation}-recovered.png`), fullPage: true, animations: "disabled" })
  })
}
