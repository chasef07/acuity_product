import { randomUUID } from "node:crypto"
import { expect, test } from "@playwright/test"
import { openAgentPage, signInAs } from "./support"

test("staff edit two office knowledge entries, publish both at once from beside the search bar, and see the new text", async ({ page }, testInfo) => {
  const loaded = { id: randomUUID(), version: 1, createdAt: "2026-09-30T15:00:00Z" }
  const published = { id: randomUUID(), version: 2, createdAt: "2026-10-12T18:30:00Z" }
  let knowledge = {
    revision: loaded,
    sections: [
      { id: "hours", title: "Office hours", text: "Monday to Friday, 8am to 5pm." },
      { id: "parking", title: "Parking", text: "Synthetic garage on site." },
    ],
  }
  const publications: Record<string, unknown>[] = []
  await page.route("**/v1/knowledge/query", (route) =>
    route.fulfill({ json: { locationId: route.request().postDataJSON().locationId, ...knowledge } }),
  )
  await page.route("**/v1/knowledge/revisions", (route) => {
    const publication = route.request().postDataJSON()
    publications.push(publication)
    knowledge = { revision: published, sections: publication.sections }
    return route.fulfill({ json: published })
  })
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))

  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Knowledge base")
  const hours = page.getByRole("listitem").filter({ hasText: "Office hours" })
  await hours.getByRole("button", { name: "Office hours", exact: true }).click()
  await hours.getByRole("button", { name: "Edit", exact: true }).click()
  await hours.getByLabel("Text", { exact: true }).fill("Monday to Friday, 8am to 5:30pm.")
  await hours.getByRole("button", { name: "Done", exact: true }).click()
  const parking = page.getByRole("listitem").filter({ hasText: "Parking" })
  await parking.getByRole("button", { name: "Parking", exact: true }).click()
  await parking.getByRole("button", { name: "Edit", exact: true }).click()
  await parking.getByLabel("Text", { exact: true }).fill("Synthetic valet only.")
  await expect(page.getByText("2 unpublished changes")).toBeVisible()
  const publish = page.getByRole("button", { name: "Publish", exact: true })
  await expect(publish).toBeInViewport()
  await expect(publish).toBeDisabled()
  await page.getByLabel("Why are you changing this?").fill("Later weekday hours and valet parking")
  await expect(publish).toBeEnabled()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-unpublished.png") })
  await publish.click()

  await expect(publish).toHaveCount(0)
  await page.getByRole("button", { name: "Office hours", exact: true }).click()
  await expect(page.getByText("Monday to Friday, 8am to 5:30pm.", { exact: true })).toBeVisible()
  await expect(page.locator(`time[datetime="${published.createdAt}"]`)).toBeVisible()
  await expect(page.getByText("· Version 2")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-published.png") })
  expect(publications).toEqual([
    expect.objectContaining({
      expectedRevisionId: loaded.id,
      reason: "Later weekday hours and valet parking",
      sections: [
        { id: "hours", title: "Office hours", text: "Monday to Friday, 8am to 5:30pm." },
        { id: "parking", title: "Parking", text: "Synthetic valet only." },
      ],
    }),
  ])
  expect(errors).toEqual([])
})

test("staff open History, restore an earlier version with a reason, and see it active", async ({ page }, testInfo) => {
  type Revision = { id: string; version: number; createdAt: string }
  const first: Revision = { id: randomUUID(), version: 1, createdAt: "2026-09-01T15:00:00Z" }
  const second: Revision = { id: randomUUID(), version: 2, createdAt: "2026-09-30T15:00:00Z" }
  const entries: Record<string, { id: string; title: string; text: string }[]> = {
    [first.id]: [
      { id: "hours", title: "Office hours", text: "Monday to Friday, 8am to 5pm." },
      { id: "fax", title: "Fax", text: "Synthetic fax 555-0100." },
    ],
    [second.id]: [{ id: "hours", title: "Office hours", text: "Monday to Friday, 8am to 5:30pm." }],
  }
  let active = second
  let history = [
    { ...second, author: "staff@abita.test", provenance: "portal", reason: "Office closes later", previousRevisionId: first.id, active: true, agentSearchCount: 0 },
    { ...first, author: "Unknown user", provenance: "git:abc123", reason: "initial import", active: false, lastAgentSearchAt: "2026-09-20T16:00:00Z", agentSearchCount: 2 },
  ]
  const publications: Record<string, unknown>[] = []
  await page.route("**/v1/knowledge/query", (route) =>
    route.fulfill({ json: { locationId: route.request().postDataJSON().locationId, revision: active, sections: entries[active.id] } }),
  )
  await page.route("**/v1/knowledge/revisions/query", (route) =>
    route.fulfill({ json: { locationId: route.request().postDataJSON().locationId, revisions: history } }),
  )
  await page.route("**/v1/knowledge/revisions/read", (route) => {
    const { locationId, revisionId } = route.request().postDataJSON()
    const revision = history.find((row) => row.id === revisionId)!
    return route.fulfill({ json: { locationId, revision: { id: revision.id, version: revision.version, createdAt: revision.createdAt }, sections: entries[revisionId] } })
  })
  await page.route("**/v1/knowledge/revisions", (route) => {
    const publication = route.request().postDataJSON()
    publications.push(publication)
    const restored: Revision = { id: publication.revisionId, version: 3, createdAt: "2026-10-12T20:00:00Z" }
    entries[restored.id] = publication.sections
    history = [
      { ...restored, author: "staff@abita.test", provenance: "portal", reason: publication.reason, previousRevisionId: active.id, active: true, agentSearchCount: 0 },
      ...history.map((row) => ({ ...row, active: false })),
    ]
    active = restored
    return route.fulfill({ json: restored })
  })
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))

  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await openAgentPage(page, "Knowledge base")
  await expect(page.getByText("· Version 2")).toBeVisible()
  await page.getByRole("button", { name: "History", exact: true }).click()
  const panel = page.getByRole("dialog", { name: "History" })
  await expect(panel.getByText("Not used by the agent yet")).toBeVisible()
  await panel.getByRole("button", { name: /^Version 1/ }).click()
  await expect(panel.getByText("First version")).toBeVisible()
  await panel.getByRole("button", { name: "Restore this version", exact: true }).click()
  await panel.getByLabel("Why restore this version?").fill("Bring back the fax entry")
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-restore.png") })
  await panel.getByRole("button", { name: "Restore", exact: true }).click()

  await expect(panel.getByRole("button", { name: /^Version 3 Active/ })).toBeVisible()
  await expect(panel.getByText("Bring back the fax entry")).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-restored.png") })
  await page.keyboard.press("Escape")
  await expect(page.getByText("· Version 3")).toBeVisible()
  await expect(page.getByRole("button", { name: "Fax", exact: true })).toBeVisible()
  expect(publications).toEqual([
    expect.objectContaining({ expectedRevisionId: second.id, reason: "Bring back the fax entry", sections: entries[first.id] }),
  ])
  expect(errors).toEqual([])
})
