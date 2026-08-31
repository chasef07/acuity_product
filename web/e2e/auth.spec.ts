import { expect, test } from "@playwright/test"

import { signInAs } from "./support"

test("homepage opens a Google-only sign-in dialog", async ({
  page,
}) => {
  await page.goto("/")

  await expect(page.getByTestId("sign-in-dialog")).toBeHidden()
  await page.getByRole("button", { name: "Sign in" }).click()
  await expect(page).toHaveURL("/")

  const card = page.getByTestId("sign-in-card")
  await expect(
    card.getByRole("heading", { name: "Sign in to Acuity Health" }),
  ).toBeVisible()
  await expect(
    card.getByRole("button", { name: "Continue with Google" }),
  ).toBeVisible()
  await expect(card.getByText("Secure Google sign-in")).toBeVisible()
  await expect(card.getByLabel("Email")).toBeHidden()
  await expect(card.getByLabel("Password")).toBeHidden()
})

test("interior marketing pages open the same sign-in dialog in place", async ({
  page,
}) => {
  await page.goto("/method")

  await page.getByRole("button", { name: "Sign in" }).click()

  await expect(page).toHaveURL(/\/method$/)
  await expect(
    page.getByRole("dialog", { name: "Sign in to Acuity Health" }),
  ).toBeVisible()
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
  await page.getByRole("button", { name: "Sign in" }).click()

  const popupPromise = page.waitForEvent("popup")
  await page.getByRole("button", { name: "Continue with Google" }).click()
  const popup = await popupPromise
  const popupURL = new URL(popup.url())

  expect(popupURL.pathname).toBe("/api/auth/oauth-popup/start")
  expect(popupURL.searchParams.get("provider")).toBe("google")
  expect(popupURL.searchParams.get("callbackURL")).toBe("/workspace")
  await expect(page).toHaveURL("/")
  await expect(
    page.getByRole("dialog", { name: "Sign in to Acuity Health" }),
  ).toBeVisible()
  await popup.close()
})

test("one Google popup switches the signed-out browser to the new session", async ({
  page,
}) => {
  await signInAs(page, "admin@abita.test", "Fixture Admin")
  await expect(
    page.getByRole("button", { name: "admin@abita.test" }),
  ).toBeVisible()
  await page.getByRole("button", { name: "admin@abita.test" }).click()
  await expect(page).toHaveURL(/\/sign-in(?:\?|$)/)

  let popupStarts = 0
  let sessionChecks = 0
  await page
    .context()
    .route("**/api/auth/get-session**", async (route) => {
      sessionChecks += 1
      await route.continue()
    })
  await page
    .context()
    .route("**/api/auth/oauth-popup/start**", async (route) => {
      popupStarts += 1
      const startURL = new URL(route.request().url())
      const nonce = startURL.searchParams.get("popupNonce")
      expect(nonce).toBeTruthy()
      await route.fulfill({
        contentType: "text/html",
        body: `<!doctype html>
        <script>
          void (async () => {
            const response = await fetch("/api/test/session", {
              method: "POST",
              headers: { "content-type": "application/json" },
              body: JSON.stringify({
                email: "selected@abita.test",
                name: "Fixture Selected Staff",
              }),
            })
            if (!response.ok) throw new Error("test session failed")
            setTimeout(() => {
              window.opener.postMessage({
                type: "better-auth:oauth-popup",
                nonce: ${JSON.stringify(nonce)},
                token: "fixture-popup-token",
              }, window.location.origin)
            }, 100)
          })()
        </script>`,
      })
    })

  await page.getByRole("button", { name: "Continue with Google" }).click()

  await expect(page).toHaveURL(/\/workspace$/)
  await expect(
    page.getByRole("button", { name: "selected@abita.test" }),
  ).toBeVisible()
  expect(popupStarts).toBe(1)
  expect(sessionChecks).toBe(2)
})

test("returning to the portal preserves the loaded workspace across session refresh", async ({
  page,
}) => {
  await signInAs(page, "admin@abita.test", "Fixture Admin")
  await expect(
    page.getByRole("button", { name: "admin@abita.test" }),
  ).toBeVisible()

  let sessionRefreshes = 0
  await page.context().route("**/api/auth/get-session**", async (route) => {
    sessionRefreshes += 1
    const response = await route.fetch()
    const session = await response.json()
    await route.fulfill({
      response,
      json: {
        ...session,
        session: {
          ...session.session,
          expiresAt: new Date(Date.now() + 60_000).toISOString(),
        },
      },
    })
  })

  const loadingObserved = await page.evaluate(async () => {
    const loading = () =>
      document.body.innerText.includes("Loading Acuity workspace")
    let observed = loading()
    const observer = new MutationObserver(() => {
      observed ||= loading()
    })
    observer.observe(document.body, { childList: true, subtree: true })
    const setVisibility = (hidden: boolean) => {
      Object.defineProperty(document, "hidden", {
        configurable: true,
        get: () => hidden,
      })
      Object.defineProperty(document, "visibilityState", {
        configurable: true,
        get: () => (hidden ? "hidden" : "visible"),
      })
      document.dispatchEvent(new Event("visibilitychange"))
    }

    try {
      setVisibility(true)
      setVisibility(false)
      await new Promise((resolve) => window.setTimeout(resolve, 750))
      return observed
    } finally {
      observer.disconnect()
      Reflect.deleteProperty(document, "hidden")
      Reflect.deleteProperty(document, "visibilityState")
    }
  })

  expect(sessionRefreshes).toBe(1)
  expect(loadingObserved).toBe(false)
  await expect(
    page.getByRole("button", { name: "admin@abita.test" }),
  ).toBeVisible()
})
