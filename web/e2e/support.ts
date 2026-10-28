import { expect, type Page } from "@playwright/test"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"

export async function signInAs(page: Page, email: string, name: string, path = "/workspace?view=work") {
  const response = await page.request.post(`${webURL}/api/test/session`, {
    data: { email, name },
  })
  expect(response.ok()).toBeTruthy()
  await page.goto(path)
}

export async function openAgentPage(page: Page, name: string) {
  const home = page.getByRole("navigation", { name: "Workspace" }).getByRole("button", { name: "Home", exact: true })
  const destination = page.getByRole("navigation", { name: "Manage agent" }).getByRole("button", { name, exact: true })
  await expect(async () => {
    if (await home.getAttribute("aria-expanded") !== "true") await home.click()
    await expect(destination).toBeInViewport({ ratio: 1, timeout: 500 })
    await destination.click({ timeout: 1000 })
  }).toPass({ timeout: 10_000 })
}

export async function selectCommandCenterPage(page: Page, name: "Calls" | "Summary" | "Breakdowns" | "Tools" | "Cost" | "Review" | "Judges") {
  const commandCenter = page.getByRole("navigation", { name: "Workspace", exact: true }).getByRole("button", { name: "Command Center", exact: true })
  const destination = page.getByRole("navigation", { name: "Command Center", exact: true }).getByRole("button", { name, exact: true })
  await expect(async () => {
    if (await commandCenter.getAttribute("aria-expanded") !== "true") await commandCenter.click()
    await expect(destination).toBeInViewport({ ratio: 1, timeout: 500 })
    await destination.click({ timeout: 1000 })
  }).toPass({ timeout: 10_000 })
}

export async function selectAnalyticsPage(page: Page, name: "Bookings" | "Call volume" | "Duration" | "Staff") {
  const analytics = page.getByRole("navigation", { name: "Workspace", exact: true }).getByRole("button", { name: "Analytics", exact: true })
  const destination = page.getByRole("navigation", { name: "Analytics", exact: true }).getByRole("button", { name, exact: true })
  await expect(async () => {
    if (await analytics.getAttribute("aria-expanded") !== "true") await analytics.click()
    await expect(destination).toBeInViewport({ ratio: 1, timeout: 500 })
    await destination.click({ timeout: 1000 })
  }).toPass({ timeout: 10_000 })
}
