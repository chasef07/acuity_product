import { spawn } from "node:child_process"
import { writeFile } from "node:fs/promises"
import { createServer } from "node:net"

import { expect, test, type BrowserContext, type Page } from "@playwright/test"

import { signInAs } from "./support"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"
const portalURL =
  process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const realtimeURL =
  process.env.E2E_REALTIME_URL ?? "http://127.0.0.1:18081"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT
const replacementRealtimePIDFile =
  process.env.E2E_REALTIME_REPLACEMENT_PID_FILE
const browserReconnectMaximumMilliseconds = 30_000
const browserReconnectAssertionMilliseconds =
  browserReconnectMaximumMilliseconds + 5_000

const operatorAnalyticsFixture = {
  summary: {
    diagnostics: {
      stages: [
        { stage: "e2e", p50Ms: 1240, p95Ms: 2600, p99Ms: 4200 },
        { stage: "stt", p50Ms: 185, p95Ms: 500, p99Ms: 780 },
        { stage: "llm", p50Ms: 410, p95Ms: 1100, p99Ms: 1600 },
        { stage: "tts", p50Ms: 265, p95Ms: 600, p99Ms: 900 },
      ].map((stage) => ({
        ...stage,
        sampleCount: 100,
        measuredCalls: 42,
        buckets: [{ fromMs: 0, count: 100, examples: [] }],
        trend: [{
          date: "2026-08-10",
          sampleCount: 100,
          p50Ms: stage.p50Ms,
          p95Ms: stage.p95Ms,
        }],
      })),
      tools: [{
        name: "book_appointment",
        executionCount: 31,
        errorCount: 2,
        incompleteCount: 0,
        sampleCount: 0,
        examples: [],
        errors: [],
      }],
    },
    daily: [{ date: "2026-08-10", totalCalls: 42, transferCount: 5, transferRate: 5 / 42 }],
    totalCalls: 42,
    bookingCount: 8,
    cancellationCount: 3,
    rescheduleCount: 4,
    p50SttMs: 185,
    p90SttMs: 420,
    p99SttMs: 780,
    p50TtftMs: 410,
    p90TtftMs: 900,
    p99TtftMs: 1600,
    p50TtsTtfbMs: 265,
    p90TtsTtfbMs: 540,
    p99TtsTtfbMs: 900,
    p50TotalLatencyMs: 1240,
    p90TotalLatencyMs: 2200,
    p99TotalLatencyMs: 4200,
    transferCount: 5,
    transferRate: 0.119,
    toolCallCount: 31,
    toolErrorCount: 2,
    toolFailureRate: 0.065,
  },
  calls: [
    {
      id: "10000000-0000-0000-0000-000000000101",
      locationId: "00000000-0000-0000-0000-000000000001",
      locationName: "Abita Springs",
      sourceCallId: "livekit-call-1",
      phone: "+19855550142",
      startedAt: "2026-08-10T08:15:00Z",
      endedAt: "2026-08-10T08:18:42Z",
      status: "COMPLETED",
      durationSeconds: 222,
      p50SttMs: 185,
      p50TtftMs: 410,
      p50TtsTtfbMs: 265,
      p50TotalLatencyMs: 1240,
      toolCallCount: 3,
      toolErrorCount: 0,
      toolActions: ["lookup_appointments", "reschedule_appointment"],
      transferred: false,
      transcriptAvailable: true,
    },
  ],
  nextCursor: "page-2",
}

const operatorAnalyticsNextPageFixture = {
  calls: [
    {
      ...operatorAnalyticsFixture.calls[0],
      id: "10000000-0000-0000-0000-000000000102",
      sourceCallId: "livekit-call-2",
      phone: "+19855550143",
      startedAt: "2026-08-10T07:45:00Z",
      endedAt: "2026-08-10T07:48:00Z",
    },
  ],
  nextCursor: "",
}

const operatorAnalyticsDetailFixture = {
  id: "10000000-0000-0000-0000-000000000101",
  practiceId: "00000000-0000-0000-0000-000000000001",
  sourceCallId: "livekit-call-1",
  locationId: "00000000-0000-0000-0000-000000000001",
  locationName: "Abita Springs",
  phone: "+19855550142",
  officePhone: "+19855550100",
  startedAt: "2026-08-10T08:15:00Z",
  endedAt: "2026-08-10T08:18:42Z",
  status: "COMPLETED",
  summary: "The caller rescheduled an existing eye exam for next Tuesday.",
  appointmentOutcome: "RESCHEDULE",
  appointment: {
    appointmentId: "appointment-new",
    patientName: "Patient redacted",
    providerName: "Dr. Example",
    locationName: "Abita Springs",
    appointmentTypeName: "Eye exam",
    startDatetime: "2026-08-18T14:30:00Z",
  },
  previousAppointment: {
    appointmentId: "appointment-old",
    providerName: "Dr. Example",
    locationName: "Abita Springs",
    appointmentTypeName: "Eye exam",
    startDatetime: "2026-08-12T14:30:00Z",
  },
  appointmentOccurredAt: "2026-08-10T08:16:30Z",
  oldAppointmentId: "appointment-old",
  newAppointmentId: "appointment-new",
  bookingResult: {
    status: "ACCEPTED",
    referenceId: "receipt-reschedule-1",
  },
  createdAt: "2026-08-10T08:18:43Z",
  updatedAt: "2026-08-10T08:18:43Z",
  p50SttMs: 185,
  p50TtftMs: 410,
  p50TtsTtfbMs: 265,
  p50TotalLatencyMs: 1240,
  timeline: [
    {
      kind: "CALLER_MESSAGE",
      occurredAt: "2026-08-10T08:15:07Z",
      text: "I need to move my appointment to next Tuesday.",
    },
    {
      kind: "AGENT_MESSAGE",
      occurredAt: "2026-08-10T08:15:09Z",
      text: "I can help you find the current appointment.",
    },
    {
      kind: "TOOL_CALL",
      occurredAt: "2026-08-10T08:15:11Z",
      name: "lookup_appointments",
      callId: "tool-call-1",
      payload: { patientId: "patient-redacted" },
    },
    {
      kind: "TOOL_RESULT",
      occurredAt: "2026-08-10T08:15:12Z",
      name: "lookup_appointments",
      callId: "tool-call-1",
      payload: { appointmentsFound: 1 },
      totalLatencyMs: 284,
    },
  ],
  toolExecutions: [
    {
      callId: "tool-call-1",
      name: "lookup_appointments",
      occurredAt: "2026-08-10T08:15:11Z",
      status: "SUCCESS",
      outputClass: "appointments_found",
    },
  ],
}

test("workspace authority, operator analytics, browser state, and reconnect", async ({
  browser,
  page,
}, testInfo) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")

  await installControlledReconnectBackoff(page)
  const customerContext = page.context()
  await abortFirstRealtimeRequest(page)
  await test.step("provisioned Admin receives an authenticated session", async () => {
    await signInAs(page, "admin@abita.test", "Fixture Admin")
    const tasksSection = page.getByRole("button", { name: /^Tasks/ })
    await expect(tasksSection).toHaveAttribute("aria-expanded", "false")
    await expectNoOpenTasks(page)
    const workspaceSelector = page.getByRole("button", {
      name: "Workspace selector",
    })
    await expect(workspaceSelector).toContainText("Abita Eye Group")
    await expect(workspaceSelector).toContainText("All offices")
    await expect(page.getByLabel("Live updates connected")).toBeVisible()
    await expect(page.getByRole("button", { name: "Analytics", exact: true })).toBeVisible()
    await expect(page.getByRole("button", { name: "AI diagnostics", exact: true })).toHaveCount(0)
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
  await installControlledReconnectBackoff(secondCustomerContext)
  const secondCustomerPage = await secondCustomerContext.newPage()
  await abortFirstRealtimeRequest(secondCustomerPage)
  await secondCustomerPage.goto("/workspace")
  await expectNoOpenTasks(secondCustomerPage)
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

    await Promise.all(
      [page, secondCustomerPage].map(enableDelayedReconnectBackoff),
    )

    process.kill(realtimePID, "SIGKILL")
    await expect(page.getByLabel("Live updates delayed")).toBeVisible()
    await expect(
      secondCustomerPage.getByLabel("Live updates delayed"),
    ).toBeVisible()

    const realtimeEndpoint = new URL(realtimeURL)
    const realtimePort = Number(realtimeEndpoint.port)
    expect(Number.isInteger(realtimePort)).toBeTruthy()
    await expect
      .poll(() => canListen(realtimeEndpoint.hostname, realtimePort), {
        message: `realtime port ${realtimeEndpoint.hostname}:${realtimePort} was not released`,
        timeout: 5_000,
      })
      .toBe(true)

    const replacementRealtime = spawn(runtimeBinary!, [], {
      env: {
        ...process.env,
        ACUITY_RUNTIME_ROLE: "realtime",
        DATABASE_URL: process.env.E2E_DATABASE_URL,
        DATABASE_POOL_MAX: "3",
        DATABASE_ACQUIRE_TIMEOUT_MS: "1500",
        HTTP_PORT: String(realtimePort),
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
      stdio: ["ignore", "inherit", "inherit"],
    })
    expect(replacementRealtime.pid).toBeTruthy()
    expect(replacementRealtimePIDFile).toBeTruthy()
    await writeFile(
      replacementRealtimePIDFile!,
      String(replacementRealtime.pid),
      "utf8",
    )
    replacementRealtime.unref()
    const readinessURL = new URL("/health/ready", realtimeEndpoint).toString()
    await expect
      .poll(
        async () => {
          if (
            replacementRealtime.exitCode !== null ||
            replacementRealtime.signalCode !== null
          ) {
            return `exited: ${replacementRealtime.exitCode ?? replacementRealtime.signalCode}`
          }
          try {
            const response = await fetch(readinessURL)
            return response.ok ? "ready" : `HTTP ${response.status}`
          } catch (error) {
            return error instanceof Error ? error.message : String(error)
          }
        },
        {
          message: "replacement realtime runtime did not become ready",
          timeout: 10_000,
        },
      )
      .toBe("ready")

    await Promise.all([
      expect(page.getByLabel("Live updates connected")).toBeVisible({
        timeout: browserReconnectAssertionMilliseconds,
      }),
      expect(
        secondCustomerPage.getByLabel("Live updates connected"),
      ).toBeVisible({ timeout: browserReconnectAssertionMilliseconds }),
    ])
    expect(firstBrowserRefetches).toBeGreaterThan(0)
    expect(secondBrowserRefetches).toBeGreaterThan(0)
  })

  const operatorContext = await browser.newContext()
  const operatorPage = await operatorContext.newPage()
  await test.step("Platform Operator writes directly and gets workspace analytics", async () => {
    await signInAs(operatorPage, "founder@acuity.test", "Fixture Founder")
    await expectNoOpenTasks(operatorPage)
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
      practices: Array<{
        id: string
        name: string
        locations: Array<{ id: string; name: string }>
      }>
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

    const analyticsRequests: Array<{
      practiceId: string
      locationId?: string
      range: string
      cursor?: string
    }> = []
    await operatorPage.route(
      `${portalURL}/v1/operator/ai-analytics/query`,
      async (route) => {
        const body = route.request().postDataJSON()
        analyticsRequests.push(body)
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(
            body.cursor
              ? operatorAnalyticsNextPageFixture
              : operatorAnalyticsFixture,
          ),
        })
      },
    )
    await operatorPage.route(
      `${portalURL}/v1/operator/ai-interactions/*/analytics`,
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(operatorAnalyticsDetailFixture),
        }),
    )

    const analytics = operatorPage.getByRole("button", { name: "AI diagnostics" })
    await expect(analytics).toBeVisible()
    await analytics.click()
    const analyticsRegion = operatorPage.getByRole("region", {
      name: "AI call analytics",
    })
    await expect(analyticsRegion.getByText("Total calls")).toBeVisible()
    await expect(analyticsRegion.getByRole("region", { name: "Call volume over time", exact: true }).getByRole("strong")).toHaveText("42")
    await expect(analyticsRegion.getByText("Booked", { exact: true })).toBeVisible()
    await expect(analyticsRegion.getByText("8", { exact: true })).toBeVisible()
    await expect(analyticsRegion.getByText("Cancelled", { exact: true })).toBeVisible()
    await expect(analyticsRegion.getByText("3", { exact: true })).toBeVisible()
    await expect(
      analyticsRegion.getByText("Rescheduled", { exact: true }),
    ).toBeVisible()
    await expect(analyticsRegion.getByText("4", { exact: true })).toBeVisible()
    await expect
      .poll(() =>
        operatorPage.evaluate(
          () => getComputedStyle(document.documentElement).fontFamily,
        ),
      )
      .toContain("-apple-system")
    await operatorPage.screenshot({
      path: testInfo.outputPath("operator-analytics-overview.png"),
      fullPage: true,
    })
    await analyticsRegion
      .getByRole("button", { name: "Performance", exact: true })
      .click()
    const latencyPipeline = analyticsRegion.getByRole("region", { name: "Pipeline stages" })
    await expect(latencyPipeline.getByRole("button", { name: /^STT/ })).toContainText("P50 185 ms")
    await expect(latencyPipeline.getByRole("button", { name: /^LLM/ })).toContainText("P50 410 ms")
    await expect(latencyPipeline.getByRole("button", { name: /^TTS/ })).toContainText("P50 265 ms")
    const performance = analyticsRegion.getByRole("region", { name: "Response performance" })
    await expect(performance.getByText("2.60 s", { exact: true }).first()).toBeVisible()
    await expect(performance.getByText("1.24 s", { exact: true }).first()).toBeVisible()
    await expect(performance.getByText("4.20 s", { exact: true }).first()).toBeVisible()
    await expect(performance.getByText("100 samples · 42 of 42 calls measured")).toBeVisible()
    await expect(analyticsRegion.getByRole("region", { name: "Latency distribution" })).toBeVisible()
    await operatorPage.screenshot({
      path: testInfo.outputPath("operator-analytics-performance.png"),
      fullPage: true,
    })

    await analyticsRegion
      .getByRole("button", { name: "Tools", exact: true })
      .click()
    await expect(analyticsRegion.getByText("Tool executions", { exact: true })).toBeVisible()
    await expect(analyticsRegion.getByRole("region", { name: "Tool execution summary" }).getByText("31", { exact: true })).toBeVisible()

    await analyticsRegion
      .getByRole("button", { name: "Calls", exact: true })
      .click()
    const callerPhone = analyticsRegion.getByText("(985) 555-0142").first()
    await expect(callerPhone).toBeVisible()
    await expect
      .poll(() =>
        callerPhone.evaluate((element) => getComputedStyle(element).fontFamily),
      )
      .toContain("-apple-system")
    await expect
      .poll(() => analyticsRequests.at(-1))
      .toMatchObject({
        practiceId: operatorPractice!.id,
        range: "7d",
      })
    expect(analyticsRequests.at(-1)?.locationId).toBeUndefined()

    await analyticsRegion
      .getByRole("button", { name: "Load more calls" })
      .click()
    await expect
      .poll(() => analyticsRequests.at(-1)?.cursor)
      .toBe("page-2")
    await expect(
      analyticsRegion.getByText("(985) 555-0143").first(),
    ).toBeVisible()
    await expect(
      analyticsRegion.getByRole("button", { name: "Load more calls" }),
    ).toHaveCount(0)

    // Cursor pages omit summary; navigating back must retain the initial metrics.
    await analyticsRegion.getByRole("button", { name: "Tools", exact: true }).click()
    await expect(analyticsRegion.getByRole("region", { name: "Tool execution summary" }).getByText("31", { exact: true })).toBeVisible()
    await analyticsRegion.getByRole("button", { name: "Calls", exact: true }).click()

    await operatorPage.getByRole("button", { name: "Last 30 days" }).click()
    await expect
      .poll(() => analyticsRequests.at(-1)?.range)
      .toBe("30d")

    const selectedLocation = operatorPractice!.locations[0]!
    await operatorPage.getByRole("button", { name: "Workspace selector" }).click()
    await operatorPage
      .getByRole("button", { name: selectedLocation.name, exact: true })
      .click()
    await expectNoOpenTasks(operatorPage)
    await operatorPage.getByRole("button", { name: "AI diagnostics" }).click()
    await expect
      .poll(() => analyticsRequests.at(-1)?.locationId)
      .toBe(selectedLocation.id)
    await analyticsRegion
      .getByRole("button", { name: "Calls", exact: true })
      .click()

    await operatorPage
      .getByRole("button", { name: /Open analytics for call from/ })
      .first()
      .click()
    const callSheet = operatorPage.getByRole("dialog", {
      name: "AI call evidence",
    })
    await expect(callSheet).toContainText(
      "The caller rescheduled an existing eye exam",
    )
    await expect(callSheet.getByLabel("Call conversation")).toBeVisible()
    await expect(callSheet.getByLabel("Caller message")).toContainText(
      "move my appointment",
    )
    const scrollBody = callSheet.getByLabel("Scrollable call evidence")
    await expect
      .poll(() =>
        scrollBody.evaluate(
          (element) => element.scrollHeight > element.clientHeight,
        ),
      )
      .toBe(true)
    const toolCall = callSheet
      .locator("details")
      .filter({ hasText: "lookup_appointments" })
      .first()
    await toolCall.locator("summary").click()
    await expect(toolCall.getByText("Request")).toBeVisible()
    await expect(toolCall.getByText("patient-redacted")).toBeVisible()
    await expect(callSheet).toContainText("Receipt backed")
    await operatorPage.screenshot({
      path: testInfo.outputPath("operator-analytics-detail.png"),
      fullPage: true,
    })
  })

  await test.step("persisted theme and explicit browser states", async () => {
    await page.goto("/workspace")
    await expectNoOpenTasks(page)
    const appearanceButton = page.getByRole("button", { name: "Appearance" })
    const systemThemeOption = page.getByRole("menuitemradio", {
      name: "System",
    })
    await appearanceButton.click()
    await expect(systemThemeOption).toBeVisible()
    await page.getByRole("menuitemradio", { name: "Light" }).click()
    await expect(systemThemeOption).toBeHidden()
    await expect(page.locator("html")).not.toHaveClass(/dark/)
    await expect
      .poll(() =>
        page.evaluate(() => {
          const styles = getComputedStyle(document.documentElement)
          return {
            background: styles.getPropertyValue("--background").trim(),
            foreground: styles.getPropertyValue("--foreground").trim(),
            primary: styles.getPropertyValue("--primary").trim(),
            sidebar: styles.getPropertyValue("--sidebar").trim(),
          }
        }),
      )
      .toEqual({
        background: "#fff",
        foreground: "#0d0d0d",
        primary: "#0d0d0d",
        sidebar: "lab(98.26% 0 0)",
      })
    await page.screenshot({
      path: testInfo.outputPath("workspace-light.png"),
      fullPage: true,
    })
    await appearanceButton.click()
    const darkThemeOption = page.getByRole("menuitemradio", { name: "Dark" })
    await expect(darkThemeOption).toBeVisible()
    await darkThemeOption.click()
    await expect(darkThemeOption).toBeHidden()
    await expect(page.locator("html")).toHaveClass(/dark/)
    await expect
      .poll(() =>
        page.evaluate(() => {
          const styles = getComputedStyle(document.documentElement)
          return {
            background: styles.getPropertyValue("--background").trim(),
            foreground: styles.getPropertyValue("--foreground").trim(),
            primary: styles.getPropertyValue("--primary").trim(),
            sidebar: styles.getPropertyValue("--sidebar").trim(),
          }
        }),
      )
      .toEqual({
        background: "#0f0f0f",
        foreground: "#f5f5f3",
        primary: "#f5f5f3",
        sidebar: "#000",
      })
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
    await expectNoOpenTasks(coarsePage)
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

async function expectNoOpenTasks(page: Page) {
  const tasksSection = page.getByRole("button", { name: /^Tasks/ })
  if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
    await tasksSection.click()
  }
  await expect(tasksSection).toHaveAttribute("aria-expanded", "true")
  await expect(page.getByText("No open Tasks")).toBeVisible()
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

async function installControlledReconnectBackoff(
  target: Page | BrowserContext,
) {
  await target.addInitScript(() => {
    const state = globalThis as typeof globalThis & {
      __acuityReconnectBackoff?: { enabled: boolean }
    }
    const originalRandom = Math.random
    let reconnectRandomCalls = 0
    state.__acuityReconnectBackoff = { enabled: false }
    // Exhaust the short jitter windows, then hold one retry in the 16-second
    // window that exposed the former 10-second assertion race.
    Math.random = () =>
      state.__acuityReconnectBackoff?.enabled
        ? reconnectRandomCalls++ < 5
          ? 0
          : 0.999
        : originalRandom()
  })
}

async function enableDelayedReconnectBackoff(page: Page) {
  await page.evaluate(() => {
    const state = globalThis as typeof globalThis & {
      __acuityReconnectBackoff?: { enabled: boolean }
    }
    if (!state.__acuityReconnectBackoff) {
      throw new Error("reconnect backoff control is unavailable")
    }
    state.__acuityReconnectBackoff.enabled = true
  })
}

function canListen(host: string, port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const server = createServer()
    server.once("error", () => resolve(false))
    server.listen({ host, port, exclusive: true }, () => {
      server.close((error) => resolve(!error))
    })
  })
}
