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

const operatorAnalyticsFixture = {
  summary: {
    totalCalls: 42,
    p50TotalLatencyMs: 1240,
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
  summary: operatorAnalyticsFixture.summary,
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
    await expect(page.getByRole("button", { name: "Analytics" })).toHaveCount(0)
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
          timeZone: "America/New_York",
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

    const analytics = operatorPage.getByRole("button", { name: "Analytics" })
    await expect(analytics).toBeVisible()
    await analytics.click()
    const analyticsRegion = operatorPage.getByRole("region", {
      name: "AI call analytics",
    })
    await expect(analyticsRegion.getByText("Total calls")).toBeVisible()
    await expect(analyticsRegion.getByText("42", { exact: true })).toBeVisible()
    await expect(
      analyticsRegion.getByText("1.24 s", { exact: true }).first(),
    ).toBeVisible()
    await expect(
      analyticsRegion.getByText("(985) 555-0142").first(),
    ).toBeVisible()
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

    await operatorPage.getByRole("button", { name: "Last 30 days" }).click()
    await expect
      .poll(() => analyticsRequests.at(-1)?.range)
      .toBe("30d")

    const selectedLocation = operatorPractice!.locations[0]!
    await operatorPage.getByRole("button", { name: "Workspace selector" }).click()
    await operatorPage
      .getByRole("button", { name: selectedLocation.name, exact: true })
      .click()
    await expect(operatorPage.getByText("No open Tasks")).toBeVisible()
    await operatorPage.getByRole("button", { name: "Analytics" }).click()
    await expect
      .poll(() => analyticsRequests.at(-1)?.locationId)
      .toBe(selectedLocation.id)

    await operatorPage
      .getByRole("button", { name: /Open analytics for call from/ })
      .first()
      .click()
    const callDialog = operatorPage.getByRole("dialog", {
      name: "AI call evidence",
    })
    await expect(callDialog).toContainText(
      "The caller rescheduled an existing eye exam",
    )
    await expect(callDialog.getByLabel("Caller message")).toContainText(
      "move my appointment",
    )
    const toolCall = callDialog
      .locator("details")
      .filter({ hasText: "lookup_appointments" })
      .first()
    await toolCall.locator("summary").click()
    await expect(toolCall.getByText("patient-redacted")).toBeVisible()
    await expect(callDialog).toContainText("Receipt backed")
    await operatorPage.screenshot({
      path: testInfo.outputPath("operator-analytics-detail.png"),
      fullPage: true,
    })
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
