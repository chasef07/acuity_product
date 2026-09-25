import { randomUUID } from "node:crypto"
import { expect, test, type Route } from "@playwright/test"
import { signInAs } from "./support"

test("changing call filters cancels old pages without unlocking the current page", async ({ page }) => {
  const call = (phone: string) => ({
    id: randomUUID(),
    phone,
    startedAt: new Date().toISOString(),
    appointmentActions: [],
    transferred: false,
    issueFlagged: false,
  })
  const first = call("+15555550101")
  const stale = call("+15555550102")
  const flagged = call("+15555550103")
  const nextFlagged = call("+15555550104")
  const fresh = call("+15555550105")
  const pending = new Map<string, Route>()
  const pageRequests: string[] = []
  let allLoads = 0
  await page.route("**/v1/agent-calls/query", async (route) => {
    const body = route.request().postDataJSON()
    if (body.cursor) {
      pageRequests.push(body.cursor)
      pending.set(body.cursor, route)
      return
    }
    if (!body.flaggedOnly) allLoads++
    await route.fulfill({ json: {
      calls: [body.flaggedOnly ? flagged : allLoads === 1 ? first : fresh],
      nextCursor: body.flaggedOnly ? "flagged-page" : `all-page-${allLoads}`,
    } })
  })
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  await page.getByRole("button", { name: "Manage agent", exact: true }).click()
  const more = page.getByRole("button", { name: "Load more calls", exact: true })
  const loading = page.getByRole("button", { name: /Loading…$/ })
  const filter = page.getByRole("button", { name: "Flagged calls", exact: true })
  await more.click()
  await expect.poll(() => pending.has("all-page-1")).toBe(true)
  await filter.click()
  await more.click()
  await expect.poll(() => pending.has("flagged-page")).toBe(true)
  await pending.get("all-page-1")!.fulfill({ json: { calls: [stale], nextCursor: "" } })
  // Give the late response a chance to incorrectly clear the other filter's loading state.
  await page.waitForTimeout(150)
  await expect(loading).toBeDisabled()
  await expect(more).toHaveCount(0)
  await pending.get("flagged-page")!.fulfill({ json: { calls: [nextFlagged], nextCursor: "" } })
  await expect(page.getByRole("button", { name: /Open call from/ })).toHaveCount(2)
  expect(pageRequests.filter(cursor => cursor === "flagged-page")).toHaveLength(1)

  // Repeat A -> B -> A while A's next page is pending; the repeated filter is a new snapshot.
  await filter.click()
  await more.click()
  await expect.poll(() => pending.has("all-page-2")).toBe(true)
  await filter.click()
  await expect(page.getByRole("button", { name: /Open call from \(555\) 555-0103/ })).toBeVisible()
  await filter.click()
  await expect(page.getByRole("button", { name: /Open call from \(555\) 555-0105/ })).toBeVisible()
  await expect(more).toBeEnabled()
  await pending.get("all-page-2")!.fulfill({ json: { calls: [stale], nextCursor: "" } })
  await page.waitForTimeout(150)
  await expect(page.getByRole("button", { name: /Open call from/ })).toHaveCount(1)
  await expect(more).toBeEnabled()
})
