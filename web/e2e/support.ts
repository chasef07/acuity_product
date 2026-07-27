import { expect, type Page } from "@playwright/test"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"

export async function latestEmail(
  page: Page,
  email: string,
  kind: "verification" | "password-reset",
): Promise<string> {
  let url = ""
  await expect
    .poll(async () => {
      const response = await page.request.get(`${webURL}/api/test/email`, {
        params: { email, kind },
      })
      if (response.ok()) {
        url = ((await response.json()) as { url: string }).url
      }
      return url
    })
    .not.toBe("")
  return url
}
