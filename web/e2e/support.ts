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
  await page.getByRole("navigation", { name: "Workspace" }).getByRole("button", { name: "Home", exact: true }).click()
  await page.getByRole("button", { name, exact: true }).click()
}
