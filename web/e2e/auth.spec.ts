import { expect, test } from "@playwright/test"

import { signInAs } from "./support"

for (const path of ["/", "/method"]) {
  test(`${path} starts Google sign-in with one click`, async ({ page }) => {
    await page.context().route("**/api/auth/oauth-popup/start**", (route) =>
      route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Google sign-in</title>" }),
    )
    await page.goto(path)
    const popupPromise = page.waitForEvent("popup")
    await page.locator("header").getByRole("button", { name: "Sign in" }).click()
    const popup = await popupPromise
    const popupURL = new URL(popup.url())
    expect(popupURL.pathname).toBe("/api/auth/oauth-popup/start")
    expect(popupURL.searchParams.get("provider")).toBe("google")
    expect(popupURL.searchParams.get("callbackURL")).toBe("/workspace")
    await expect(page).toHaveURL(path)
    await popup.close()
    await expect(page.getByText("Google sign-in was closed. Try again.")).toBeVisible()
    await expect(page.getByRole("button", { name: "Continue with Google" })).toBeEnabled()
  })
}

test("footer sign-in also starts Google with one click", async ({ page }) => {
  await page.context().route("**/api/auth/oauth-popup/start**", (route) =>
    route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Google sign-in</title>" }),
  )
  await page.goto("/")
  const popupPromise = page.waitForEvent("popup")
  await page.locator("footer").getByRole("button", { name: "Sign in" }).click()
  await (await popupPromise).close()
})

test("existing session opens the workspace without contacting Google", async ({ page }) => {
  await signInAs(page, "admin@abita.test", "Fixture Admin")
  await expect(page.getByRole("button", { name: "admin@abita.test" })).toBeVisible()
  let popupStarts = 0
  page.on("popup", () => { popupStarts += 1 })
  await page.goto("/")
  await page.locator("header").getByRole("button", { name: "Sign in" }).click()
  await expect(page).toHaveURL(/\/workspace$/)
  await expect(page.getByRole("button", { name: "admin@abita.test" })).toBeVisible()
  await page.goto("/sign-in?next=%2Fworkspace")
  await expect(page).toHaveURL(/\/workspace$/)
  expect(popupStarts).toBe(0)
})

test("blocked popup stays recoverable", async ({ page }) => {
  await page.goto("/")
  await page.evaluate(() => { window.open = () => null })
  await page.locator("header").getByRole("button", { name: "Sign in" }).click()
  await expect(page.getByText("Allow pop-ups for Acuity Health, then try again.")).toBeVisible()
  await expect(page.getByRole("button", { name: "Continue with Google" })).toBeEnabled()
})

test("sign-in does not pass an external return destination to Google", async ({ page }) => {
  await page.context().route("**/api/auth/oauth-popup/start**", (route) =>
    route.fulfill({ contentType: "text/html", body: "<!doctype html><title>Google sign-in</title>" }),
  )
  await page.goto("/sign-in?next=%2F%2Fexample.com")
  const popupPromise = page.waitForEvent("popup")
  await page.getByRole("button", { name: "Continue with Google" }).click()
  const popup = await popupPromise
  expect(new URL(popup.url()).searchParams.get("callbackURL")).toBe("/workspace")
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
  let completeSessionRefresh!: () => void
  const sessionRefreshCompleted = new Promise<void>((resolve) => {
    completeSessionRefresh = resolve
  })
  await page.exposeFunction(
    "waitForSessionRefresh",
    () => sessionRefreshCompleted,
  )
  await page.context().route("**/api/auth/get-session**", async (route) => {
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
    sessionRefreshes += 1
    completeSessionRefresh()
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
      await (
        window as typeof window & {
          waitForSessionRefresh: () => Promise<void>
        }
      ).waitForSessionRefresh()
      await new Promise(requestAnimationFrame)
      await new Promise(requestAnimationFrame)
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
