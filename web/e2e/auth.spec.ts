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
