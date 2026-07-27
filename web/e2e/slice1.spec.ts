import { readFile } from "node:fs/promises"
import { spawn, type ChildProcess } from "node:child_process"

import { expect, test, type Page } from "@playwright/test"

import { latestEmail } from "./support"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"
const portalURL =
  process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const realtimeURL =
  process.env.E2E_REALTIME_URL ?? "http://127.0.0.1:18081"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT
let replacementRealtime: ChildProcess | undefined

test.afterEach(() => {
  replacementRealtime?.kill("SIGKILL")
  replacementRealtime = undefined
})

test("Slice 1 invite, authority, Support Mode, recovery, and reconnect", async ({
  browser,
  page,
}, testInfo) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  const provisioned = JSON.parse(
    await readFile(provisioningOutput!, "utf8"),
  ) as {
    invitations: Array<{ email: string; token: string }>
  }
  const adminInvite = provisioned.invitations.find(
    (invitation) => invitation.email === "admin@abita.test",
  )
  expect(adminInvite?.token).toBeTruthy()

  await test.step("public sign-up is visibly and behaviorally rejected", async () => {
    await page.goto("/sign-up")
    await expect(
      page.getByRole("heading", { name: "Public sign-up is unavailable" }),
    ).toBeVisible()
    const response = await page.request.post(`${webURL}/api/auth/sign-up/email`, {
      headers: { origin: webURL },
      data: {
        name: "Uninvited User",
        email: "uninvited@example.test",
        password: "not-a-real-password-123",
      },
    })
    expect(response.status()).toBe(403)
  })

  const customerContext = page.context()
  await abortFirstRealtimeRequest(page)
  await test.step("invited Admin creates and verifies a private account", async () => {
    await page.goto(`/invite#${adminInvite!.token}`)
    await expect(page).toHaveURL(`${webURL}/invite`)
    await expect(
      page.getByRole("heading", { name: "Create your account" }),
    ).toBeVisible()
    await expect(page.getByText("Abita Eye Group")).toBeVisible()
    await expect(page.getByText("All current and future Locations")).toBeVisible()
    await page.getByLabel("Your name").fill("Fixture Admin")
    await page.getByLabel("Create password").fill("fixture-password-1234")
    await page.getByLabel("Confirm password").fill("fixture-password-1234")
    await page.getByRole("button", { name: "Create private account" }).click()
    await expect(page.getByText("Check your email")).toBeVisible()

    const verificationURL = await latestEmail(
      page,
      "admin@abita.test",
      "verification",
    )
    expect(verificationURL).toContain("/verify-email#")
    await page.goto(verificationURL)
    await expect(page.getByRole("heading", { name: "Welcome back" })).toBeVisible()
    await page.getByLabel("Email").fill("admin@abita.test")
    await page.getByLabel("Password").fill("fixture-password-1234")
    await page.getByRole("button", { name: "Sign in" }).click()
    await expect(page).toHaveURL(/\/workspace$/)
    await expect(page.getByText("No tasks yet")).toBeVisible()
    await expect(page.getByText("Abita Eye Group · Fixture Location 1")).toBeVisible()
    await expect(page.getByText("Live")).toBeVisible()
  })

  await test.step("cross-Location expansion is denied without protected data", async () => {
    const token = await accessToken(page)
    const response = await page.request.get(
      `${portalURL}/v1/workspace?practiceId=00000000-0000-0000-0000-000000000001&locationId=00000000-0000-0000-0000-000000000002`,
      {
        headers: {
          authorization: `Bearer ${token}`,
          origin: webURL,
        },
      },
    )
    expect(response.status()).toBe(403)
    const body = await response.json()
    expect(body).toEqual({
      error: expect.objectContaining({
        code: "ACCESS_DENIED",
        retryable: false,
      }),
    })
    expect(JSON.stringify(body)).not.toContain("Abita Eye Group")
  })

  const customerState = await customerContext.storageState()
  const secondCustomerContext = await browser.newContext({
    storageState: customerState,
  })
  const secondCustomerPage = await secondCustomerContext.newPage()
  await abortFirstRealtimeRequest(secondCustomerPage)
  await secondCustomerPage.goto("/workspace")
  await expect(secondCustomerPage.getByText("No tasks yet")).toBeVisible()
  await expect(secondCustomerPage.getByText("Live")).toBeVisible()
  const initialVersion = Number(
    await page.getByTestId("mounted-workspace").getAttribute(
      "data-workspace-version",
    ),
  )

  await test.step("both established browsers recover from realtime instance death", async () => {
    const realtimePID = Number(process.env.E2E_REALTIME_PID)
    const runtimeBinary = process.env.E2E_RUNTIME_BINARY
    expect(Number.isInteger(realtimePID)).toBeTruthy()
    expect(runtimeBinary).toBeTruthy()
    let firstBrowserRefetches = 0
    let secondBrowserRefetches = 0
    page.on("request", (request) => {
      if (request.url().startsWith(`${portalURL}/v1/workspace`)) {
        firstBrowserRefetches += 1
      }
    })
    secondCustomerPage.on("request", (request) => {
      if (request.url().startsWith(`${portalURL}/v1/workspace`)) {
        secondBrowserRefetches += 1
      }
    })

    process.kill(realtimePID, "SIGKILL")
    await expect(page.getByText("Disconnected")).toBeVisible()
    await expect(secondCustomerPage.getByText("Disconnected")).toBeVisible()

    replacementRealtime = spawn(runtimeBinary!, [], {
      env: {
        ...process.env,
        ACUITY_RUNTIME_ROLE: "realtime",
        DATABASE_URL: process.env.E2E_DATABASE_URL,
        DATABASE_POOL_MAX: "3",
        DATABASE_ACQUIRE_TIMEOUT_MS: "1500",
        HTTP_PORT: "18081",
        BROWSER_ORIGIN: webURL,
        BETTER_AUTH_JWKS_URL: `${webURL}/api/auth/jwks`,
        BETTER_AUTH_ISSUER: webURL,
        PORTAL_API_AUDIENCE: portalURL,
        REALTIME_HEARTBEAT_SECONDS: "2",
        REALTIME_STREAM_SECONDS: "30",
        REALTIME_REVALIDATE_SECONDS: "2",
        REALTIME_RECONNECT_MIN_MS: "100",
        REALTIME_RECONNECT_MAX_SECONDS: "2",
      },
      stdio: "ignore",
    })

    await expect(page.getByText("Live")).toBeVisible()
    await expect(secondCustomerPage.getByText("Live")).toBeVisible()
    expect(firstBrowserRefetches).toBeGreaterThan(0)
    expect(secondBrowserRefetches).toBeGreaterThan(0)
  })

  const operatorContext = await browser.newContext()
  const operatorPage = await operatorContext.newPage()
  await test.step("Platform Operator needs visible Practice-scoped Support Mode", async () => {
    await operatorPage.goto("/operator-access")
    await operatorPage.getByLabel("Your name").fill("Fixture Founder")
    await operatorPage
      .getByLabel("Provisioned operator email")
      .fill("founder@acuity.test")
    await operatorPage.getByLabel("Create password").fill("operator-password-1234")
    await operatorPage
      .getByLabel("Confirm password")
      .fill("operator-password-1234")
    await operatorPage
      .getByRole("button", { name: "Create operator account" })
      .click()
    const verificationURL = await latestEmail(
      operatorPage,
      "founder@acuity.test",
      "verification",
    )
    await operatorPage.goto(verificationURL)
    await operatorPage.getByLabel("Email").fill("founder@acuity.test")
    await operatorPage.getByLabel("Password").fill("operator-password-1234")
    await operatorPage.getByRole("button", { name: "Sign in" }).click()
    await expect(operatorPage.getByText("No tasks yet")).toBeVisible()

    await operatorPage
      .getByRole("button", { name: "Enter Support Mode" })
      .click()
    const dialog = operatorPage.getByRole("dialog")
    await dialog.getByLabel("Reason").fill("Validate Slice 1 browser workflow")
    await dialog.getByLabel("Duration").selectOption("30")
    await dialog
      .getByRole("button", { name: "Enter Support Mode" })
      .click()
    await expect(operatorPage.getByText("Support Mode active")).toBeVisible()
    await expect(
      operatorPage.getByText("Validate Slice 1 browser workflow"),
    ).toBeVisible()

    const operatorToken = await accessToken(operatorPage)
    const snapshotResponse = await operatorPage.request.get(
      `${portalURL}/v1/workspace`,
      {
        headers: { authorization: `Bearer ${operatorToken}` },
        params: {
          practiceId: await selectedValue(operatorPage, "Practice"),
          locationId: await selectedValue(operatorPage, "Location"),
        },
      },
    )
    expect(snapshotResponse.ok()).toBeTruthy()
    const snapshot = await snapshotResponse.json()
    const mutation = await operatorPage.request.post(
      `${portalURL}/v1/practices/${snapshot.practice.id}/locations`,
      {
        headers: {
          authorization: `Bearer ${operatorToken}`,
          origin: webURL,
        },
        data: {
          supportSessionId: snapshot.supportMode.id,
          key: "fixture-location-7",
          name: "Fixture Location 7",
        },
      },
    )
    expect(mutation.status()).toBe(201)

    await expect
      .poll(async () =>
        Number(
          await page.getByTestId("mounted-workspace").getAttribute(
            "data-workspace-version",
          ),
        ),
      )
      .toBeGreaterThan(initialVersion)
    await expect
      .poll(async () =>
        Number(
          await secondCustomerPage
            .getByTestId("mounted-workspace")
            .getAttribute("data-workspace-version"),
        ),
      )
      .toBeGreaterThan(initialVersion)

    await operatorPage.getByRole("button", { name: "Exit" }).click()
    await expect(operatorPage.getByText("Support Mode active")).toBeHidden()
  })

  await test.step("recovery, persisted theme, and explicit browser states", async () => {
    await page.goto("/forgot-password")
    await page.getByLabel("Verified email").fill("admin@abita.test")
    await page.getByRole("button", { name: "Send recovery link" }).click()
    const recoveryURL = await latestEmail(
      page,
      "admin@abita.test",
      "password-reset",
    )
    expect(recoveryURL).toContain("/reset-password#")
    await page.goto(recoveryURL)
    await expect(page).toHaveURL(`${webURL}/reset-password`)
    await page
      .getByLabel("New password", { exact: true })
      .fill("updated-password-1234")
    await page.getByLabel("Confirm new password").fill("updated-password-1234")
    await page.getByRole("button", { name: "Update password" }).click()
    await expect(page.getByText("Password updated")).toBeVisible()

    await page.goto("/sign-in")
    await page.getByLabel("Email").fill("admin@abita.test")
    await page.getByLabel("Password").fill("updated-password-1234")
    await page.getByRole("button", { name: "Sign in" }).click()
    await expect(page.getByText("No tasks yet")).toBeVisible()
    await page.screenshot({
      path: testInfo.outputPath("workspace-light.png"),
      fullPage: true,
    })
    await page.getByRole("button", { name: "Dark mode" }).click()
    await expect(page.locator("html")).toHaveClass(/dark/)
    await page.screenshot({
      path: testInfo.outputPath("workspace-dark.png"),
      fullPage: true,
    })
    await page.reload()
    await expect(page.locator("html")).toHaveClass(/dark/)

    const authenticatedState = await page.context().storageState()
    const coarseContext = await browser.newContext({
      storageState: authenticatedState,
      hasTouch: true,
      viewport: { width: 1280, height: 800 },
    })
    const coarsePage = await coarseContext.newPage()
    await coarsePage.goto("/workspace")
    await expect(coarsePage.getByText("No tasks yet")).toBeVisible()
    await expect(coarsePage.getByLabel("Practice")).toBeVisible()
    await coarsePage.screenshot({
      path: testInfo.outputPath("workspace-coarse-pointer.png"),
      fullPage: true,
    })
    await coarseContext.close()

    const unavailableContext = await browser.newContext({
      storageState: authenticatedState,
    })
    const unavailablePage = await unavailableContext.newPage()
    await unavailablePage.route(`${portalURL}/v1/access`, (route) =>
      route.abort(),
    )
    await unavailablePage.goto("/workspace")
    await expect(
      unavailablePage.getByText("Workspace temporarily disconnected"),
    ).toBeVisible()
    await unavailablePage.screenshot({
      path: testInfo.outputPath("workspace-unavailable.png"),
      fullPage: true,
    })
    await unavailableContext.close()

    const deniedContext = await browser.newContext({
      storageState: authenticatedState,
    })
    const deniedPage = await deniedContext.newPage()
    await deniedPage.route(`${portalURL}/v1/access`, (route) =>
      route.fulfill({
        status: 403,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "ACCESS_DENIED",
            message: "The requested access is not available.",
            correlationId: "browser-evidence",
            retryable: false,
          },
        }),
      }),
    )
    await deniedPage.goto("/workspace")
    await expect(deniedPage.getByText("Workspace access unavailable")).toBeVisible()
    await deniedContext.close()
  })

  await operatorContext.close()
  await secondCustomerContext.close()
})

async function accessToken(page: Page): Promise<string> {
  const response = await page.request.get(`${webURL}/api/auth/token`)
  expect(response.ok()).toBeTruthy()
  return ((await response.json()) as { token: string }).token
}

async function abortFirstRealtimeRequest(page: Page) {
  let aborted = false
  await page.route(`${realtimeURL}/v1/events**`, async (route) => {
    if (!aborted) {
      aborted = true
      await route.abort()
      return
    }
    await route.continue()
  })
}

async function selectedValue(page: Page, label: string): Promise<string> {
  return page.getByLabel(label).inputValue()
}
