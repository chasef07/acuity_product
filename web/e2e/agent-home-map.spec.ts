import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

test("Home connects the animated Acuity mark to three keyboard-accessible areas", async ({ page }, testInfo) => {
  await signInAs(page, "selected@abita.test", "Synthetic Staff", "/workspace")
  const connections = page.getByRole("group", { name: "Agent connections" })
  const mark = connections.getByRole("img", { name: "Acuity", exact: true })
  await expect(connections.getByRole("button")).toHaveCount(3)
  await expect(mark.locator("circle")).toHaveCount(7)
  await expect(page.getByText(/Transfer policy|Emergency policy/)).toHaveCount(0)
  const firstFrame = await mark.innerHTML()
  await expect.poll(() => mark.innerHTML()).not.toBe(firstFrame)
  await page.screenshot({ path: testInfo.outputPath("agent-home-desktop.png"), fullPage: true })

  const tools = connections.getByRole("button", { name: "Tools", exact: true })
  await tools.focus()
  await page.keyboard.press("Enter")
  await expect(tools).toHaveAttribute("aria-expanded", "true")
  await expect(page.getByRole("list", { name: "Agent tools" })).toBeVisible()
  await page.getByRole("button", { name: "Close details", exact: true }).click()
  await expect(tools).toBeFocused()
  await expect(tools).toHaveAttribute("aria-expanded", "false")

  await connections.getByRole("button", { name: "Knowledge base", exact: true }).click()
  await page.getByRole("button", { name: "Open knowledge base", exact: true }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Knowledge base" })).toBeVisible()
  await page.getByRole("navigation", { name: "Manage agent" }).getByRole("button", { name: "Home", exact: true }).click()
  await connections.getByRole("button", { name: "Insurance", exact: true }).click()
  await page.getByRole("button", { name: "Open insurance list", exact: true }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Insurance list" })).toBeVisible()
})

test("Home fits narrow screens and honors reduced motion", async ({ page }, testInfo) => {
  await page.emulateMedia({ reducedMotion: "reduce" })
  await page.setViewportSize({ width: 390, height: 844 })
  await signInAs(page, "selected@abita.test", "Synthetic Staff", "/workspace")
  const connections = page.getByRole("group", { name: "Agent connections" })
  const mark = connections.getByRole("img", { name: "Acuity", exact: true })
  await expect(mark).toBeVisible()
  const frame = await mark.innerHTML()
  await connections.getByRole("button", { name: "Tools", exact: true }).click()
  await expect(page.getByRole("list", { name: "Agent tools" })).toBeVisible()
  await expect(mark).toHaveJSProperty("innerHTML", frame)
  await page.getByRole("button", { name: "Close details", exact: true }).click()
  await page.getByRole("heading", { level: 1, name: "Your agent" }).scrollIntoViewIfNeeded()
  for (const width of [390, 768, 1024, 1280]) {
    await page.setViewportSize({ width, height: 900 })
    await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    const cards = await connections.getByRole("button").all()
    for (const card of cards) {
      await expect(card).toBeVisible()
      expect(await card.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true)
    }
    await page.screenshot({ path: testInfo.outputPath(`agent-home-${width}.png`), fullPage: true })
  }
})
