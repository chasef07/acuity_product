import { randomUUID } from "node:crypto"
import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("staff edit an office knowledge entry, publish it with a reason, and see the new text", async ({ page }, testInfo) => {
  const loaded = { id: randomUUID(), createdAt: "2026-09-30T15:00:00Z" }
  const published = { id: randomUUID(), createdAt: "2026-10-12T18:30:00Z" }
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
  await page.getByRole("button", { name: "Manage agent", exact: true }).click()
  await page.getByRole("button", { name: "Knowledge base", exact: true }).click()
  const hours = page.getByRole("listitem").filter({ hasText: "Office hours" })
  await hours.getByRole("button", { name: "Office hours", exact: true }).click()
  await hours.getByRole("button", { name: "Edit", exact: true }).click()
  await hours.getByLabel("Text", { exact: true }).fill("Monday to Friday, 8am to 5:30pm.")
  const publish = page.getByRole("button", { name: "Publish", exact: true })
  await expect(publish).toBeDisabled()
  await page.getByLabel("Why are you changing this?").fill("Office closes later on weekdays")
  await expect(publish).toBeEnabled()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-unpublished.png") })
  await publish.click()

  await expect(publish).toHaveCount(0)
  await page.getByRole("button", { name: "Office hours", exact: true }).click()
  await expect(page.getByText("Monday to Friday, 8am to 5:30pm.", { exact: true })).toBeVisible()
  await expect(page.locator(`time[datetime="${published.createdAt}"]`)).toBeVisible()
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("knowledge-published.png") })
  expect(publications).toEqual([
    expect.objectContaining({
      expectedRevisionId: loaded.id,
      reason: "Office closes later on weekdays",
      sections: [
        { id: "hours", title: "Office hours", text: "Monday to Friday, 8am to 5:30pm." },
        { id: "parking", title: "Parking", text: "Synthetic garage on site." },
      ],
    }),
  ])
  expect(errors).toEqual([])
})
