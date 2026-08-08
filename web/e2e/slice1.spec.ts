import { writeFile } from "node:fs/promises"
import { spawn } from "node:child_process"

import { expect, test, type Page } from "@playwright/test"

import { signInAs } from "./support"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"
const portalURL =
  process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const realtimeURL =
  process.env.E2E_REALTIME_URL ?? "http://127.0.0.1:18081"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT
const replacementRealtimePIDFile =
  process.env.E2E_REALTIME_REPLACEMENT_PID_FILE

test("Slice 1 authority, operator analytics, browser state, and reconnect", async ({
  browser,
  page,
}, testInfo) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")

  const customerContext = page.context()
  await abortFirstRealtimeRequest(page)
  await test.step("provisioned Admin receives an authenticated session", async () => {
    await signInAs(page, "admin@abita.test", "Fixture Admin")
    await expect(page.getByText("No open Tasks")).toBeVisible()
    const workspaceSelector = page.getByRole("button", {
      name: "Workspace selector",
    })
    await expect(workspaceSelector).toContainText("Abita Eye Group")
    await expect(workspaceSelector).toContainText("All offices")
    await expect(page.getByLabel("Live updates connected")).toBeVisible()
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
  await expect(secondCustomerPage.getByText("No open Tasks")).toBeVisible()
  await expect(
    secondCustomerPage.getByLabel("Live updates connected"),
  ).toBeVisible()
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
    await expect(page.getByLabel("Live updates delayed")).toBeVisible()
    await expect(
      secondCustomerPage.getByLabel("Live updates delayed"),
    ).toBeVisible()

    const replacementRealtime = spawn(runtimeBinary!, [], {
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
        REALTIME_STREAM_JITTER_SECONDS: "5",
        REALTIME_REVALIDATE_SECONDS: "2",
        REALTIME_RECONNECT_MIN_MS: "100",
        REALTIME_RECONNECT_MAX_SECONDS: "2",
      },
      stdio: "ignore",
    })
    expect(replacementRealtime.pid).toBeTruthy()
    expect(replacementRealtimePIDFile).toBeTruthy()
    await writeFile(
      replacementRealtimePIDFile!,
      String(replacementRealtime.pid),
      "utf8",
    )
    replacementRealtime.unref()

    await expect(page.getByLabel("Live updates connected")).toBeVisible()
    await expect(
      secondCustomerPage.getByLabel("Live updates connected"),
    ).toBeVisible()
    expect(firstBrowserRefetches).toBeGreaterThan(0)
    expect(secondBrowserRefetches).toBeGreaterThan(0)
  })

  const operatorContext = await browser.newContext()
  const operatorPage = await operatorContext.newPage()
  await test.step("Platform Operator writes directly and gets workspace analytics", async () => {
    await signInAs(operatorPage, "founder@acuity.test", "Fixture Founder")
    await expect(operatorPage.getByText("No open Tasks")).toBeVisible()
    await expect(
      operatorPage.getByRole("region", { name: "Call diagnostics" }),
    ).toHaveCount(0)

    const initialVersion = Number(
      await page.getByTestId("mounted-workspace").getAttribute(
        "data-workspace-version",
      ),
    )
    const operatorToken = await accessToken(operatorPage)
    const accessResponse = await operatorPage.request.get(
      `${portalURL}/v1/access`,
      { headers: { authorization: `Bearer ${operatorToken}` } },
    )
    expect(accessResponse.ok()).toBeTruthy()
    const access = (await accessResponse.json()) as {
      practices: Array<{ id: string; name: string }>
    }
    const operatorPractice = access.practices.find(
      (practice) => practice.name === "Abita Eye Group",
    )
    expect(operatorPractice?.id).toBeTruthy()
    const mutation = await operatorPage.request.post(
      `${portalURL}/v1/practices/${operatorPractice!.id}/locations`,
      {
        headers: {
          authorization: `Bearer ${operatorToken}`,
          origin: webURL,
        },
        data: {
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

    const analytics = operatorPage.getByRole("button", { name: "Analytics" })
    await expect(analytics).toBeVisible()
    await analytics.click()
    await expect(
      operatorPage.getByRole("region", { name: "Analytics" }),
    ).toContainText("Transcripts, latency, and call performance")
  })

  await test.step("persisted theme and explicit browser states", async () => {
    await page.goto("/workspace")
    await expect(page.getByText("No open Tasks")).toBeVisible()
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
    await expect(coarsePage.getByText("No open Tasks")).toBeVisible()
    await expect(
      coarsePage.getByRole("button", { name: "Workspace selector" }),
    ).toContainText("Abita Eye Group")
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
