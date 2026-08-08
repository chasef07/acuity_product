import { expect, test } from "@playwright/test"

test("Portal opens a Google-first sign-in dialog with in-card email", async ({
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
  await expect(card.getByText("Invite-only access")).toBeVisible()
  await expect(card.getByLabel("Email")).toBeHidden()

  const emailAction = card.getByRole("button", { name: "Use email instead" })
  await expect(emailAction).toHaveAttribute("aria-expanded", "false")
  await emailAction.click()

  await expect(emailAction).toHaveAttribute("aria-expanded", "true")
  await expect(card.getByLabel("Email")).toBeVisible()
  await expect(card.getByLabel("Password")).toBeVisible()
  await expect(card.getByRole("link", { name: "Forgot password?" })).toBeVisible()
  await expect(card.getByRole("button", { name: "Sign in" })).toBeVisible()
})

test("Google sign-in opens in a popup and leaves the portal in place", async ({
  page,
}) => {
  await page.context().route("**/api/auth/oauth-popup/start**", async (route) => {
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

test("sign-in deep links open the dialog with verified invitation state", async ({
  page,
}) => {
  await page.goto("/sign-in?verified=1&next=%2Faccept-invitation")

  const card = page.getByTestId("sign-in-card")
  await expect(card.getByText("Email verified")).toBeVisible()
  await expect(
    card.getByText("Sign in to activate your Acuity invitation."),
  ).toBeVisible()
  await expect(
    card.getByRole("button", { name: "Continue with Google" }),
  ).toBeVisible()
})
