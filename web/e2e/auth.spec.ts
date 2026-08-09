import { expect, test } from "@playwright/test"

test("homepage opens a Google-only sign-in dialog", async ({
  page,
}) => {
  await page.goto("/")

  await expect(page.getByTestId("sign-in-dialog")).toBeHidden()
  await page.getByRole("button", { name: "Portal" }).click()

  const card = page.getByTestId("sign-in-card")
  await expect(
    card.getByRole("heading", { name: "Sign in to Acuity" }),
  ).toBeVisible()
  await expect(
    card.getByRole("button", { name: "Continue with Google" }),
  ).toBeVisible()
  await expect(card.getByText("Secure Google sign-in")).toBeVisible()
  await expect(card.getByLabel("Email")).toBeHidden()
  await expect(card.getByLabel("Password")).toBeHidden()
})

test("Google sign-in opens in a popup and leaves the portal in place", async ({
  page,
}) => {
  await page
    .context()
    .route("**/api/auth/oauth-popup/start**", async (route) => {
      await route.fulfill({
        contentType: "text/html",
        body: "<!doctype html><title>Google sign-in</title>",
      })
    })
  await page.goto("/")
  await page.getByRole("button", { name: "Portal" }).click()

  const popupPromise = page.waitForEvent("popup")
  await page.getByRole("button", { name: "Continue with Google" }).click()
  const popup = await popupPromise
  const popupURL = new URL(popup.url())

  expect(popupURL.pathname).toBe("/api/auth/oauth-popup/start")
  expect(popupURL.searchParams.get("provider")).toBe("google")
  expect(popupURL.searchParams.get("callbackURL")).toBe("/workspace")
  await expect(page).toHaveURL("/")
  await expect(
    page.getByRole("dialog", { name: "Sign in to Acuity" }),
  ).toBeVisible()
  await popup.close()
})
