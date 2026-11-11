import { expect, test, type Page, type Route } from "@playwright/test"

import { patientRow, railFilter, railScope, showPatientTask, signInAs } from "./support"

const telnyxFixtureURL =
  process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"
const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT

test("mobile phone search keeps Call visible across multiple offices", async ({
  page,
}) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await page.setViewportSize({ width: 390, height: 844 })
  await signInAs(page, "admin@abita.test", "Fixture Admin")
  await page.getByRole("button", { name: "Work", exact: true }).click()

  let releaseTimeline = () => {}
  const timelineGate = new Promise<void>((resolve) => {
    releaseTimeline = resolve
  })
  const delayedTimeline = async (route: Route) => {
    await timelineGate
    await route.continue()
  }
  await page.route(/\/v1\/engagements\/[^/]+\/timeline/, delayedTimeline)

  const searchInput = page.getByLabel("Search tasks, names, or phone")
  const submitButton = page.getByRole("button", { name: "Search" })
  await expect(submitButton).toBeVisible()
  await searchInput.fill("7275550188")
  await submitButton.click()
  await page.keyboard.press("Escape")

  await expect(page.getByRole("heading", { name: "(727) 555-0188" })).toBeVisible()
  await expect(
    page.getByRole("status", { name: "Loading conversation" }),
  ).toBeVisible()
  releaseTimeline()
  await expect(page.getByText("No activity yet", { exact: true })).toBeVisible()
  await page.unroute(/\/v1\/engagements\/[^/]+\/timeline/, delayedTimeline)
  await expect(page.getByLabel("Sender office")).toBeVisible()
  const callButton = page.getByRole("button", { name: "Call", exact: true })
  await expect(callButton).toBeInViewport({ ratio: 1 })
  await expect(callButton).toHaveText("Call")

  await page.setViewportSize({ width: 1280, height: 720 })
  await expect(page.locator('button[aria-label="Search"]')).toBeHidden()
})

test("appointment verification stays open until shared completion and can be reopened", async ({ page, browser }) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  const sourceCallId = "shared-appointment-verification"
  await createAIAppointmentReview(page, sourceCallId, new Date(Date.now() - 8 * 24 * 60 * 60 * 1000))
  const { token } = await (await page.request.get("/api/auth/token")).json()
  let task!: { id: string; title: string }
  await expect.poll(async () => {
    const response = await page.request.post(`${portalURL}/v1/tasks/query`, {
      headers: { authorization: `Bearer ${token}` },
      data: { practiceId: (await (await page.request.get(`${portalURL}/v1/access`, { headers: { authorization: `Bearer ${token}` } })).json()).practices[0].id, state: "OPEN", responsibility: "all", limit: 50 },
    })
    const data = await response.json()
    task = data.items?.find((item: { sourceCallId?: string }) => item.sourceCallId === sourceCallId)
    return Boolean(task)
  }).toBe(true)
  await page.reload()
  const row = page.locator(`[data-task-id="${task.id}"]`)
  await expect(row).toBeVisible()
  await showPatientTask(page, "+17275550188")
  await expect(page.getByRole("button", { name: "Verify & next", exact: true })).toBeVisible()
  await railFilter(page, "appointments").click()
  await expect(railFilter(page, "appointments")).toHaveAttribute("aria-pressed", "true")
  await expect(row).toBeVisible()
  const secondContext = await browser.newContext()
  try {
    const second = await secondContext.newPage()
    await signInAs(second, "selected@abita.test", "Second Synthetic Staff")
    const secondRow = second.locator(`[data-task-id="${task.id}"]`)
    await expect(secondRow).toBeVisible()
    await showPatientTask(second, "+17275550188")
    await railFilter(second, "appointments").click()
    await expect(secondRow).toBeVisible()
    await page.getByRole("button", { name: "Verify & next", exact: true }).click()
    await expect(row).toHaveCount(0)
    await expect(secondRow).toHaveCount(0)
    await expect(second.getByText("Completed for everyone with access.")).toBeVisible()
    const completedContext = page.getByRole("complementary", { name: "Task context" })
    await expect(completedContext).toBeHidden()
    await expect(page.getByTestId("patient-row")).toHaveCount(0)
    await page.getByRole("button", { name: "Recently completed", exact: true }).click()
    await expect(row).toBeVisible()
    await row.getByRole("button").first().click()
    await expect(completedContext).toBeVisible()
    await page.getByRole("button", { name: "Reopen", exact: true }).click()
    await expect(secondRow).toBeVisible()
    await expect(page.getByRole("button", { name: /^Appointments 7d|^Texts 7d|^Mark all/ })).toHaveCount(0)
  } finally {
    await secondContext.close()
  }
})

test("an AI call without appointment actions stays call-first", async ({
  page,
}, testInfo) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()
  await createAINoAppointmentCall(page)
  const followUp = await page.request.post(`${portalURL}/v1/tasks`, {
    headers: { authorization: "Bearer synthetic-service-token" },
    data: {
      callId: "messaging-no-appointment-call", callerPhone: "+17275550187",
      category: "documentation", idempotencyKey: "history-paperwork",
      message: "Caller also requested paperwork.", summary: "Confirm office paperwork",
      officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency: "normal",
    },
  })
  expect([200, 201]).toContain(followUp.status())

  const searchInput = page.getByLabel("Search tasks, names, or phone")
  await searchInput.fill("7275550187")
  await searchInput.press("Enter")

  const outboundText = "We’re open until 4:30 PM today."
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(outboundText)
  await page.getByRole("button", { name: "Send message" }).click()
  const outboundMessage = page
    .getByRole("article")
    .filter({ hasText: outboundText })
  await expect(outboundMessage.getByText("Sent", { exact: true })).toBeVisible()
  await expect(outboundMessage.locator('[data-slot="message-sender"]')).toContainText(/^messaging@abita\.test · \d/)

  const inboundText = "Perfect, thank you!"
  await sendInbound(page, "messaging-no-appointment-inbound", inboundText, "+17275550187")
  const inboundMessage = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inboundMessage).toBeVisible()
  await expect(inboundMessage.locator('[data-slot="bubble"]')).toHaveAttribute(
    "data-variant",
    "ghost",
  )

  const activity = page.getByRole("region", { name: "Conversation activity" })
  const callRow = activity.locator("button[aria-expanded]")
  const callItem = callRow.locator("xpath=..")
  const linkedTask = page.getByRole("button", { name: "View task: Confirm office paperwork" })
  await expect(callRow).toHaveCount(1)
  await expect(callRow).toContainText("Called in · Caller asked about office hours")
  await expect(callRow).toContainText("Message taken")
  await expect(callRow).not.toContainText("No appointment actions")
  await expect(linkedTask).toHaveCount(0)
  await callRow.click()
  await expect(callRow).toHaveAttribute("aria-expanded", "true")
  await expect(linkedTask).toBeVisible()
  await expect(callItem).toContainText('AI took a message: "Confirm office paperwork"')
  await expect(callItem).not.toContainText("transferred")
  await expect(callItem).toContainText("Confirm office paperwork · Open")
  await activity.getByRole("button", { name: "View AI call", exact: true }).click()
  await expect(callRow).toHaveAttribute("aria-current", "true")
  await expect(callItem).toHaveAttribute("data-selected", "true")
  await expect
    .poll(() =>
      callItem.evaluate((element) => ({
        background: getComputedStyle(element).backgroundColor,
        marker: getComputedStyle(element, "::before").backgroundColor,
      })),
    )
    .toMatchObject({
      background: "rgba(0, 0, 0, 0)",
    })
  expect(
    await callItem.evaluate(
      (element) => getComputedStyle(element, "::before").backgroundColor,
    ),
  ).not.toBe("rgba(0, 0, 0, 0)")

  const callContext = page.getByRole("complementary", {
    name: "AI call context",
  })
  await expect(callContext).toBeVisible()
  await expect(
    callContext.getByRole("heading", { name: "AI call", exact: true }),
  ).toBeVisible()
  await expect(callContext).not.toContainText("AI call details")
  await expect(callContext).toContainText("Caller asked about office hours.")
  await expect(callContext.getByText("Details", { exact: true })).toBeVisible()
  await expect(callContext.getByText("Evidence", { exact: true })).toBeVisible()
  await expect(callContext).not.toContainText("No appointment actions")
  await expect(callContext).not.toContainText("Patient")
  await page.screenshot({
    path: testInfo.outputPath("shared-call-history.png"),
    fullPage: true,
  })

  await linkedTask.click()
  const taskContext = page.getByRole("complementary", { name: "Task context" })
  await taskContext.getByRole("button", { name: "Complete & next", exact: true }).click()
  await expect(callItem).toContainText(/Confirm office paperwork · Done, by \S+@\S+/)
  await expect(page.getByText(/Task completed by \S+@\S+ ·/)).toBeVisible()
})

test("rail hover details and the message composer preserve compact context", async ({
  page,
}, testInfo) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()

  const appearanceButton = page.getByRole("button", { name: "Account menu" })
  await expect(appearanceButton).toBeVisible()
  await expect(appearanceButton.locator("img")).toBeVisible()
  await expect(appearanceButton).toContainText("messaging@abita.test")
  await appearanceButton.click()
  await expect(page.getByRole("button", { name: "System", exact: true })).toBeVisible()
  await page.screenshot({
    animations: "disabled",
    path: testInfo.outputPath("appearance-menu.png"),
    fullPage: true,
  })
  await page.getByRole("button", { name: "Light", exact: true }).click()
  await page.keyboard.press("Escape")
  await expect(page.locator('[data-slot="sidebar-inner"]')).toHaveCSS(
    "background-color",
    "rgb(250, 250, 250)",
  )

  await createAIStaffTask(
    page,
    "billing",
    "Review billing balance",
    "hover-details",
  )
  await page.reload()
  const taskRow = patientRow(page, "+17275550196")
  await expect(taskRow.getByTestId("patient-row-reason")).toHaveText("Review billing balance")
  await taskRow.getByRole("button").hover()
  const hoverDetails = page.getByTestId("rail-hover-details")
  await expect(hoverDetails).toContainText("1 open")
  await expect(hoverDetails).toContainText("Review billing balance")
  await expect(hoverDetails).toContainText("(727) 555-0196")
  await expect(hoverDetails).toContainText("Fixture Location 1")
  await expect(hoverDetails).toHaveCSS("opacity", "1")
  await expect(hoverDetails).toHaveCSS("width", "320px")
  await expect(hoverDetails.locator('[data-slot="tooltip-arrow"]')).toHaveCount(0)
  await page.screenshot({
    path: testInfo.outputPath("rail-hover-details.png"),
    fullPage: true,
  })

  const searchInput = page.getByLabel("Search tasks, names, or phone")
  await searchInput.fill("7275550199")
  await searchInput.press("Enter")
  await expect(page.getByRole("heading", { name: "(727) 555-0199" })).toBeVisible()
  await page.mouse.move(720, 360)
  await expect(hoverDetails).toBeHidden()
  const composerSurface = page
    .getByRole("form", { name: "Message composer" })
    .locator('[data-slot="input-group"]')
  const composerHeight = await composerSurface.evaluate(
    (element) => element.getBoundingClientRect().height,
  )
  expect(composerHeight).toBeGreaterThanOrEqual(60)
  expect(composerHeight).toBeLessThanOrEqual(72)
  const composerRadius = await composerSurface.evaluate((element) =>
    Number.parseFloat(getComputedStyle(element).borderTopLeftRadius),
  )
  expect(composerRadius).toBeGreaterThanOrEqual(composerHeight / 2)
  const idleComposerPresentation = await composerSurface.evaluate((element) => {
    const styles = getComputedStyle(element)
    return {
      borderColor: styles.borderColor,
      boxShadow: styles.boxShadow,
    }
  })
  const sendButton = page.getByRole("button", { name: "Send message" })
  const sendButtonBox = await sendButton.boundingBox()
  expect(sendButtonBox?.width).toBe(sendButtonBox?.height)
  const sendButtonRadius = await sendButton.evaluate((element) =>
    Number.parseFloat(getComputedStyle(element).borderTopLeftRadius),
  )
  expect(sendButtonRadius).toBeGreaterThanOrEqual((sendButtonBox?.width ?? 0) / 2)
  const messageBox = page.getByRole("textbox", {
    name: "Message",
    exact: true,
  })
  await messageBox.fill("Confirm appointment availability")
  await expect
    .poll(() =>
      composerSurface.evaluate((element) => {
        const styles = getComputedStyle(element)
        return {
          borderColor: styles.borderColor,
          boxShadow: styles.boxShadow,
        }
      }),
    )
    .toEqual(idleComposerPresentation)
  await expect(sendButton).toBeEnabled()
  await expect(sendButton).toHaveCSS("opacity", "1")
  await expect(sendButton).toHaveCSS("background-color", "rgb(13, 13, 13)")
  await page.screenshot({
    path: testInfo.outputPath("message-composer-quiet-focus.png"),
    fullPage: true,
  })

  await taskRow.getByRole("button").click()
  await page
    .getByRole("complementary", { name: "Task context" })
    .getByRole("button", { name: "Complete & next", exact: true })
    .click()
  await expect(taskRow).toHaveCount(0)
})

test("messaging sends, receives, and keeps exact-phone correspondence in one workspace", async ({
  context,
  page,
}, testInfo) => {
  test.setTimeout(180_000)
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  const stalePracticeID = "00000000-0000-0000-0000-000000000091"
  const staleLocationID = "00000000-0000-0000-0000-000000000092"
  let sourcePracticeID = ""
  await page.route(`${portalURL}/v1/access`, async (route) => {
    const response = await route.fetch()
    const discovery = (await response.json()) as {
      practices: Array<{ id: string }>
      [key: string]: unknown
    }
    sourcePracticeID = discovery.practices[0]?.id ?? ""
    await route.fulfill({
      response,
      json: {
        ...discovery,
        practices: [
          ...discovery.practices,
          {
            id: stalePracticeID,
            name: "Stale Callback Practice",
            version: 1,
            locations: [
              { id: staleLocationID, name: "Stale Callback Location" },
            ],
          },
        ],
      },
    })
  })
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()

  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toHaveAttribute("title", /Abita Eye Group/)
  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toHaveAttribute("title", /Fixture Location 1/)
  await expect(page.getByRole("tablist", { name: "Work state" })).toHaveCount(0)
  await expect(railScope(page, "My Tasks")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "all")).toHaveAttribute("aria-pressed", "true")
  await expect(railFilter(page, "texts")).toBeVisible()
  await expect(page.getByRole("button", { name: "New text" })).toHaveCount(0)
  await openNumberInbox(page, "7275550199")
  await expect(page.getByRole("button", { name: "Call", exact: true })).toBeVisible()
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  await page.getByRole("button", { name: "Copy phone number" }).click()
  await expect(page.getByRole("button", { name: "Number copied" })).toBeVisible()
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toBe("+17275550199")

  const retryDraft = "Retry this exact message safely."
  const retryKeys: string[] = []
  let sendAttempt = 0
  const retryRoute = async (route: Route) => {
    sendAttempt += 1
    retryKeys.push(
      (route.request().postDataJSON() as { idempotencyKey: string })
        .idempotencyKey,
    )
    if (sendAttempt === 1) {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "DEPENDENCY_UNAVAILABLE",
            message: "Messaging is temporarily unavailable.",
            correlationId: "message-retry",
            retryable: true,
          },
        }),
      })
      return
    }
    await route.continue()
  }
  await page.route(`${portalURL}/v1/messages`, retryRoute)
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(retryDraft)
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(
    page.getByText("The message was not queued. Nothing was sent."),
  ).toBeVisible()
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(page.getByRole("article").filter({ hasText: retryDraft })).toBeVisible()
  expect(retryKeys).toHaveLength(2)
  expect(retryKeys[1]).toBe(retryKeys[0])
  await page.unroute(`${portalURL}/v1/messages`, retryRoute)

  let failPreview = true
  const previewRoute = async (route: Route) => {
    if (route.request().method() === "GET" && failPreview) {
      await route.fulfill({ status: 503, json: { message: "Synthetic attachment outage" } })
    } else await route.continue()
  }
  await page.route(`${portalURL}/v1/attachments/*`, previewRoute)
  const outgoingText = "Your records are ready for pickup."
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(outgoingText)
  await page.locator('input[type="file"]').setInputFiles({
    name: "fixture-photo.png",
    mimeType: "image/png",
    buffer: Buffer.from(
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
      "base64",
    ),
  })
  const draftAttachment = page
    .getByRole("form", { name: "Message composer" })
    .locator('[data-slot="attachment"]')
  await expect(draftAttachment).toHaveAttribute("data-state", "idle")
  await expect(draftAttachment.getByText("fixture-photo.png")).toBeVisible()
  const composer = page.getByRole("form", { name: "Message composer" })
  const inputGroup = composer.locator('[data-slot="input-group"]')
  await expect(inputGroup.getByText("fixture-photo.png")).toBeVisible()
  const attachBounds = await composer.getByRole("button", { name: "Attach one file" }).boundingBox()
  const sendBounds = await composer.getByRole("button", { name: "Send message" }).boundingBox()
  expect(attachBounds!.x).toBeGreaterThan((await composer.getByRole("textbox", { name: "Message", exact: true }).boundingBox())!.x)
  expect(attachBounds!.x + attachBounds!.width).toBeLessThanOrEqual(sendBounds!.x)

  expect(
    (await draftAttachment.locator('[data-slot="attachment-media"]').boundingBox())
      ?.width,
  ).toBeLessThanOrEqual(32)
  await page.getByRole("button", { name: "Send message" }).click()

  const outgoing = page
    .getByRole("article")
    .filter({ hasText: outgoingText })
  await expect(outgoing).toBeVisible()
  await expect(outgoing.locator('[data-slot="bubble"]')).toHaveAttribute(
    "data-variant",
    "muted",
  )
  await expect(outgoing.getByText("Sending", { exact: true })).toBeVisible()
  await expect(outgoing.getByText("Sent", { exact: true })).toBeVisible()
  const sentAttachment = outgoing.locator('[data-slot="attachment"]')
  await expect(sentAttachment).toContainText("Attachment unavailable.")
  failPreview = false
  const download = page.waitForEvent("download")
  await sentAttachment.getByRole("button", { name: "Download fixture-photo.png", exact: true }).click()
  expect((await download).suggestedFilename()).toBe("fixture-photo.png")
  await sentAttachment.getByRole("button", { name: "Retry fixture-photo.png", exact: true }).click()
  const previewButton = outgoing.getByRole("button", { name: "Preview fixture-photo.png" })
  await expect(
    previewButton.getByRole("img", { name: "fixture-photo.png" }),
  ).toBeVisible()
  await page.unroute(`${portalURL}/v1/attachments/*`, previewRoute)
  await expect(sentAttachment).toHaveCount(0)
  await expect(outgoing).not.toContainText("Stored")

  await previewButton.click()
  const imagePreview = page.getByRole("dialog", { name: "fixture-photo.png", exact: true })
  await expect(imagePreview.getByRole("img", { name: "fixture-photo.png" })).toBeVisible()
  await expect(imagePreview.getByRole("button", { name: "Download", exact: true })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(imagePreview).toBeHidden()
  await expect(previewButton).toBeFocused()

  const providerMessage = await expect
    .poll(async () => {
      const response = await page.request.get(
        `${telnyxFixtureURL}/fixture/messages`,
        { headers: { authorization: "Bearer fixture-control" } },
      )
      const body = (await response.json()) as {
        data: Array<{
          id: string
          from: string
          to: string
          text: string
          messaging_profile_id: string
          media_urls: string[]
        }>
      }
      return body.data.find((message) => message.text === outgoingText)
    })
    .not.toBeUndefined()
    .then(async () => {
      const response = await page.request.get(
        `${telnyxFixtureURL}/fixture/messages`,
        { headers: { authorization: "Bearer fixture-control" } },
      )
      const body = (await response.json()) as {
        data: Array<{
          id: string
          from: string
          to: string
          text: string
          messaging_profile_id: string
          media_urls: string[]
        }>
      }
      return body.data.find((message) => message.text === outgoingText)!
    })
  expect(providerMessage).toEqual(
    expect.objectContaining({
      from: "+17275550101",
      to: "+17275550199",
      messaging_profile_id: "fixture-messaging-profile-1",
      media_urls: [expect.stringContaining("/v1/provider/messaging-media/")],
    }),
  )

  const delivery = await page.request.post(
    `${telnyxFixtureURL}/fixture/messages/${providerMessage.id}/status`,
    {
      headers: { authorization: "Bearer fixture-control" },
      data: { status: "delivered" },
    },
  )
  expect(delivery.ok()).toBeTruthy()
  await expect(outgoing.getByText("Delivered", { exact: true })).toBeVisible()

  await openNumberInbox(page, "(727) 555-0198")
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill("Keep this second conversation selected.")
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(
    page
      .getByRole("article")
      .filter({ hasText: "Keep this second conversation selected." })
      .getByText("Sent", { exact: true }),
  ).toBeVisible()

  const inboundText = "Please call me about the pickup time."
  await sendInbound(page, "messaging-inbound", inboundText)
  const texter = patientRow(page, "+17275550199")
  await expect(texter.getByTestId("patient-row-reason")).toHaveText(inboundText)
  const firstThread = texter.getByRole("button")
  await expect(page.getByText("Correspondence ledger", { exact: true })).toHaveCount(0)
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)
  await firstThread.click()
  const inbound = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inbound).toBeVisible()
  await expect(inbound.locator('[data-slot="bubble"]')).toHaveAttribute(
    "data-variant",
    "ghost",
  )
  await expect(page.getByText("Today", { exact: true }).first()).toBeVisible()
  await expect(
    page
      .getByTestId("text-attention-row")
      .filter({ hasText: "(727) 555-0199" }),
  ).toHaveCount(0)

  await expect(
    page.getByTestId("patient-row").filter({ hasText: "Follow up on text" }),
  ).toHaveCount(0)
  await expect(texter.getByTestId("patient-row-count")).toHaveCount(0)

  await page.setViewportSize({ width: 390, height: 844 })
  const messageActions = inbound.locator('[data-slot="message-footer"]')
  await expect(messageActions).toHaveCSS("opacity", "0")
  const composerSurface = page
    .getByRole("form", { name: "Message composer" })
    .locator('[data-slot="input-group"]')
  const restingComposerHeight = await composerSurface.evaluate(
    (element) => element.getBoundingClientRect().height,
  )
  expect(restingComposerHeight).toBeGreaterThanOrEqual(60)
  expect(restingComposerHeight).toBeLessThanOrEqual(72)
  const sendButton = page.getByRole("button", { name: "Send message" })
  const sendButtonBox = await sendButton.boundingBox()
  expect(sendButtonBox?.width).toBe(sendButtonBox?.height)
  const sendButtonRadius = await sendButton.evaluate((element) =>
    Number.parseFloat(getComputedStyle(element).borderTopLeftRadius),
  )
  expect(sendButtonRadius).toBeGreaterThanOrEqual((sendButtonBox?.width ?? 0) / 2)
  const messageInput = page.getByRole("textbox", {
    name: "Message",
    exact: true,
  })
  await messageInput.fill("First line\nSecond line\nThird line")
  await expect
    .poll(() =>
      composerSurface.evaluate((element) => element.getBoundingClientRect().height),
    )
    .toBeGreaterThan(restingComposerHeight)
  await messageInput.fill("")

  await page.setViewportSize({ width: 1280, height: 320 })
  const timeline = page.getByTestId("message-timeline")
  await expect
    .poll(() =>
      timeline.evaluate(
        (element) => element.scrollHeight > element.clientHeight,
      ),
    )
    .toBe(true)
  const inboxHeading = page.getByRole("heading", {
    name: "(727) 555-0199",
    exact: true,
  })
  const headerTop = await inboxHeading.evaluate(
    (element) => element.closest("header")?.getBoundingClientRect().top,
  )
  expect(await inboxHeading.evaluate((element) => Boolean(element.closest('[data-testid="workspace-panel"]')))).toBe(true)
  await timeline.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  const latestScrollTop = await timeline.evaluate((element) => element.scrollTop)
  expect(latestScrollTop).toBeGreaterThan(0)
  await page.waitForTimeout(600)
  await timeline.hover()
  await page.mouse.wheel(0, -1_000)
  await expect
    .poll(() => timeline.evaluate((element) => element.scrollTop))
    .toBeLessThan(latestScrollTop)
  const scrollToLatest = page.getByRole("button", { name: "Scroll to end" })
  await expect(scrollToLatest).toBeVisible()
  await scrollToLatest.click({ timeout: 10_000 })
  expect(await page.getByTestId("workspace-panel").evaluate((element) => element.scrollTop)).toBe(0)
  await expect
    .poll(() => timeline.evaluate((element) => element.scrollTop))
    .toBeGreaterThan(0)
  await expect
    .poll(() =>
      inboxHeading.evaluate(
        (element) => element.closest("header")?.getBoundingClientRect().top,
      ),
    )
    .toBe(headerTop)
  expect(await page.evaluate(() => window.scrollY)).toBe(0)
  await page.setViewportSize({ width: 1280, height: 720 })

  const copyMessage = inbound.getByRole("button", { name: "Copy message" })
  const createTask = inbound.getByRole("button", { name: "Create task" })
  await expect(messageActions).toHaveCSS("opacity", "0")
  await inbound.hover()
  await expect(messageActions).toHaveCSS("opacity", "1")
  await copyMessage.hover()
  const copyTooltip = page.getByTestId("copy-message-tooltip")
  await expect(copyTooltip).toBeVisible()
  await expect(copyTooltip).toHaveCSS("opacity", "1")
  await page.screenshot({
    path: testInfo.outputPath("message-copy-hover.png"),
    fullPage: true,
  })
  await copyMessage.click()
  await expect(inbound.getByRole("status")).toContainText("Message copied")
  await inbound.focus()
  await expect(messageActions).toHaveCSS("opacity", "1")
  await createTask.click()
  const taskTouchpoint = page.getByRole("button", {
    name: /View task: Follow up on text/,
  })
  await expect(taskTouchpoint).toBeVisible()
  await taskTouchpoint.click()
  const contextPanel = page.getByRole("complementary", {
    name: "Task context",
  })
  await expect(contextPanel).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(
    contextPanel.getByRole("heading", { name: "Follow up on text" }),
  ).toBeVisible()
  await expect(contextPanel).toHaveCSS("width", "288px")
  await expect(contextPanel.getByText("Task context", { exact: true })).toHaveCount(0)
  await expect(contextPanel.getByText("Normal priority", { exact: true })).toBeHidden()
  await page.screenshot({
    path: testInfo.outputPath("task-context.png"),
    fullPage: true,
  })
  expect(
    await contextPanel.evaluate(
      (element) => getComputedStyle(element).transitionProperty,
    ),
  ).toContain("width")
  await contextPanel.getByRole("button", { name: "Close context panel" }).click()
  await expect(contextPanel).not.toBeVisible()
  await expect(page.getByTestId("context-panel")).toHaveAttribute(
    "data-state",
    "closed",
  )

  await expect(texter.getByTestId("patient-row-count")).toHaveText("2 open")
  await expect(texter.getByTestId("patient-row-reason")).toHaveText(inboundText)
  await taskTouchpoint.click()
  const sidebarTaskContext = page.getByRole("complementary", {
    name: "Task context",
  })
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(
    sidebarTaskContext.getByRole("heading", {
      name: "Follow up on text",
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByRole("region", { name: "Task conversation" }),
  ).toHaveCount(0)

  await page.reload()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/workspace\?task=[^&]+$/)
  await expect(page.getByTestId("context-panel")).toHaveAttribute("data-state", "open")
  await expect(
    sidebarTaskContext.getByRole("heading", {
      name: "Follow up on text",
      exact: true,
    }),
  ).toBeVisible()
  await expect(
    page.getByRole("article").filter({ hasText: inboundText }),
  ).toBeVisible()
  const taskReply = "I will call you with the pickup time."
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(taskReply)
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(
    page.getByRole("article").filter({ hasText: taskReply }),
  ).toBeVisible()

  await sidebarTaskContext
    .getByRole("button", { name: "Complete & next", exact: true })
    .click()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await openNumberInbox(page, "7275550199")
  const messageAfterCompletion = "The Task is complete; texting remains available."
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(messageAfterCompletion)
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(
    page
      .getByRole("article")
      .filter({ hasText: messageAfterCompletion }),
  ).toBeVisible()
  await page
    .getByRole("button", {
      name: /View task: Follow up on text/,
    })
    .last()
    .click()
  const completedTaskContext = page.getByRole("complementary", {
    name: "Task context",
  })
  await expect(completedTaskContext).toBeVisible()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await completedTaskContext.getByRole("button", { name: "Reopen", exact: true }).click()
  await expect(
    completedTaskContext.getByRole("button", { name: "Complete & next" }),
  ).toBeVisible()
  await expect(texter.getByTestId("patient-row-reason")).toHaveText("Follow up on text")
  await expect(
    completedTaskContext.getByRole("heading", {
      name: "Follow up on text",
      exact: true,
    }),
  ).toBeVisible()

  await sendInbound(page, "messaging-stop", "STOP")
  await expect(
    page.getByText("Outbound messaging is blocked after STOP"),
  ).toBeVisible()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeDisabled()
  await expect(
    page
      .getByTestId("text-attention-row")
      .filter({ hasText: "(727) 555-0199" }),
  ).toHaveCount(0)

  await sendInbound(page, "messaging-start", "START")
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await expect(
    page.getByText("Outbound messaging is blocked after STOP"),
  ).not.toBeVisible()

  await createAIStaffTask(page, "billing", "Review billing balance")
  const billingRow = patientRow(page, "+17275550196")
  await expect(billingRow.getByTestId("patient-row-reason")).toHaveText("Review billing balance")
  await createAIStaffTask(page, "medication", "Review medication refill")
  const medicationRow = patientRow(page, "+17275550195")
  await expect(medicationRow.getByTestId("patient-row-reason")).toHaveText("Review medication refill")

  await page.reload()
  await expect(billingRow).toBeVisible()
  await expect(medicationRow).toBeVisible()

  await billingRow.getByRole("button").hover()
  const taskHoverDetails = page.getByTestId("rail-hover-details")
  await expect(taskHoverDetails).toContainText("(727) 555-0196")
  await expect(taskHoverDetails).toContainText("Fixture Location 1")
  await expect(taskHoverDetails).toContainText("Review billing balance")
  await expect(billingRow.getByRole("button")).toHaveCount(1)

  await billingRow.getByRole("button").click()
  await page.getByRole("button", { name: "1 open Task", exact: true }).click()
  const patientTasks = page.getByRole("complementary", { name: "Patient Tasks" })
  const taskItem = patientTasks
    .getByTestId("patient-task-row")
    .filter({ hasText: "Review billing balance" })
  const completeTask = taskItem.getByRole("button", {
    name: "Mark done: Review billing balance",
    exact: true,
  })
  await expect(completeTask).toBeVisible()

  await page.route("**/api/auth/token", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: "temporarily unavailable" }),
    }),
  )
  await page.evaluate(async () => {
    const sender = new BroadcastChannel("acuity-auth-token")
    const observer = new BroadcastChannel("acuity-auth-token")
    await new Promise<void>((resolve) => {
      observer.onmessage = () => resolve()
      sender.postMessage("clear-access-token")
    })
    sender.close()
    observer.close()
  })
  await completeTask.click()
  await expect(
    taskItem
      .getByRole("alert")
      .filter({ hasText: "Task completion is temporarily unavailable" }),
  ).toBeVisible()
  await expect(taskItem.getByRole("alert")).not.toContainText("session expired")
  await page.unroute("**/api/auth/token")
  await page.evaluate(() => {
    const channel = new BroadcastChannel("acuity-auth-token")
    channel.postMessage("clear-access-token")
    channel.close()
  })

  let completionAttempt = 0
  let releaseSuccess = () => {}
  const successGate = new Promise<void>((resolve) => {
    releaseSuccess = resolve
  })
  const completionRoute = async (route: Route) => {
    completionAttempt += 1
    if (completionAttempt === 1) {
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "TASK_CONFLICT",
            message: "The Task state changed. Refresh and try again.",
            correlationId: "sidebar-conflict",
            retryable: false,
          },
        }),
      })
      return
    }
    if (completionAttempt === 2) {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          error: {
            code: "DEPENDENCY_UNAVAILABLE",
            message: "Task storage is unavailable.",
            correlationId: "sidebar-failure",
            retryable: true,
          },
        }),
      })
      return
    }
    await successGate
    await route.continue()
  }
  await page.route(/\/v1\/tasks\/[^/]+\/complete$/, completionRoute)

  await completeTask.click()
  await expect(taskItem).toBeVisible()
  await expect(billingRow).toBeVisible()
  await expect(
    taskItem.getByRole("alert").filter({ hasText: "changed elsewhere" }),
  ).toBeVisible()

  await completeTask.click()
  await expect(taskItem).toBeVisible()
  await expect(
    taskItem.getByRole("alert").filter({ hasText: "could not be completed" }),
  ).toBeVisible()

  await completeTask.click()
  await expect(completeTask).toHaveAttribute("aria-busy", "true")
  await expect(completeTask.getByRole("status", { name: "Loading" })).toBeVisible()
  await expect(taskItem).toBeVisible()
  releaseSuccess()
  await expect(taskItem).toHaveCount(0)
  await expect(billingRow).toHaveCount(0)
  await page.unroute(/\/v1\/tasks\/[^/]+\/complete$/, completionRoute)

  await medicationRow.getByRole("button").click()
  await page.getByRole("button", { name: "1 open Task", exact: true }).click()
  const medicationItem = patientTasks
    .getByTestId("patient-task-row")
    .filter({ hasText: "Review medication refill" })
  const completeMedication = medicationItem.getByRole("button", {
    name: "Mark done: Review medication refill",
    exact: true,
  })
  let releaseStaleCompletion = () => {}
  const staleCompletionGate = new Promise<void>((resolve) => {
    releaseStaleCompletion = resolve
  })
  let finishStaleCompletion = (status: number) => {
    void status
  }
  const staleCompletionHandled = new Promise<number>((resolve) => {
    finishStaleCompletion = resolve
  })
  const staleCompletionRoute = async (route: Route) => {
    await staleCompletionGate
    const response = await route.fetch()
    await route.fulfill({ response })
    finishStaleCompletion(response.status())
  }
  let sourceTaskQueries = 0
  const taskQueryRoute = async (route: Route) => {
    const body = route.request().postDataJSON() as { practiceId?: string }
    if (body.practiceId === sourcePracticeID) sourceTaskQueries += 1
    await route.continue()
  }
  await page.route(/\/v1\/tasks\/(patients\/)?query$/, taskQueryRoute)
  await page.route(
    /\/v1\/tasks\/[^/]+\/complete$/,
    staleCompletionRoute,
  )

  await expect(medicationItem).toBeVisible()
  await completeMedication.click()
  await expect(completeMedication).toHaveAttribute("aria-busy", "true")
  await page.getByRole("button", { name: "Workspace selector" }).click()
  await page
    .getByRole("button", { name: "Stale Callback Location", exact: true })
    .click()
  await expect(page.getByTestId("patient-row")).toHaveCount(0)
  sourceTaskQueries = 0
  releaseStaleCompletion()
  expect(await staleCompletionHandled).toBe(200)
  await page.waitForTimeout(500)
  expect(sourceTaskQueries).toBe(0)
})

async function openNumberInbox(
  page: Page,
  phone: string,
) {
  await page.getByLabel("Search tasks, names, or phone").fill(phone)
  await page.getByLabel("Search tasks, names, or phone").press("Enter")
  await expect(page.getByRole("heading", { name: /\(727\) 555-01\d\d/ })).toBeVisible()
}

async function sendInbound(
  page: Page,
  eventID: string,
  text: string,
  from = "+17275550199",
) {
  const response = await page.request.post(
    `${telnyxFixtureURL}/fixture/message-inbound`,
    {
      headers: { authorization: "Bearer fixture-control" },
      data: {
        eventId: eventID,
        providerMessageId: `provider-${eventID}`,
        from,
        to: "+17275550101",
        text,
      },
    },
  )
  expect(response.ok()).toBeTruthy()
}

async function createAIStaffTask(
  page: Page,
  category: "billing" | "medication",
  summary: string,
  idempotencySuffix?: string,
) {
  const suffix = idempotencySuffix ?? (category === "billing" ? "billing" : "medication")
  const response = await page.request.post(`${portalURL}/v1/tasks`, {
    headers: { authorization: "Bearer synthetic-service-token" },
    data: {
      callId: `messaging-${suffix}`,
      callerPhone: category === "billing" ? "+17275550196" : "+17275550195",
      category,
      idempotencyKey: `messaging-${suffix}`,
      message: summary,
      officeKey: "spring-hill",
      officePhone: "+17275550101",
      source: "agent",
      summary,
      urgency: "normal",
    },
  })
  expect([200, 201]).toContain(response.status())
}

async function createAIAppointmentReview(page: Page, sourceCallId = "messaging-booking-review", occurredAt = new Date()) {
  const startedAt = new Date(occurredAt.getTime() - 60_000)
  const response = await page.request.post(`${portalURL}/v1/ai/interactions`, {
    headers: { authorization: "Bearer synthetic-production-token" },
    data: {
      kind: "CLOSEOUT",
      officeKey: "spring-hill",
      sourceCallId,
      callerPhone: "+17275550188",
      officePhone: "+17275550101",
      startedAt: startedAt.toISOString(),
      endedAt: occurredAt.toISOString(),
      status: "COMPLETED",
      summary: "Caller booked an appointment.",
      closeoutPayload: { callId: sourceCallId },
      appointmentOutcome: {
        action: "BOOKED",
        occurredAt: occurredAt.toISOString(),
        newAppointmentId: "messaging-booking",
        bookingResult: {
          status: "booked",
          appointmentId: "messaging-booking",
        },
      },
    },
  })
  expect([200, 201]).toContain(response.status())
}

async function createAINoAppointmentCall(page: Page) {
  const endedAt = new Date()
  const startedAt = new Date(endedAt.getTime() - 90_000)
  const response = await page.request.post(`${portalURL}/v1/ai/interactions`, {
    headers: { authorization: "Bearer synthetic-production-token" },
    data: {
      kind: "CLOSEOUT",
      officeKey: "spring-hill",
      sourceCallId: "messaging-no-appointment-call",
      callerPhone: "+17275550187",
      officePhone: "+17275550101",
      startedAt: startedAt.toISOString(),
      endedAt: endedAt.toISOString(),
      status: "COMPLETED",
      summary: "Caller asked about office hours.",
      closeoutPayload: { callId: "messaging-no-appointment-call" },
    },
  })
  expect([200, 201]).toContain(response.status())
}


test("the Texts filter counts every patient, including old work and unloaded pages", async ({ page }) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  for (let index = 0; index < 64; index += 1) {
    await sendInbound(page, `shared-inbox-${index}`, "Synthetic inbox question", `+1555040${String(index).padStart(4, "0")}`)
  }
  await page.getByLabel("Search tasks, names, or phone").fill("555040")
  await page.getByLabel("Search tasks, names, or phone").press("Enter")
  const texts = railFilter(page, "texts")
  await texts.click()
  await expect(texts).toHaveText("Texts64", { timeout: 30_000 })
  await expect(railFilter(page, "all")).toHaveText("All64")
  await expect(page.getByTestId("patient-row")).toHaveCount(50)
  await page.getByRole("button", { name: "Show more", exact: true }).click()
  await expect(page.getByTestId("patient-row")).toHaveCount(64)
  await page.getByTestId("patient-row").first().getByRole("button").click()
  await expect(texts).toHaveText("Texts64")
  await page.reload()
  await page.getByLabel("Search tasks, names, or phone").fill("555040")
  await page.getByLabel("Search tasks, names, or phone").press("Enter")
  await expect(texts).toHaveText("Texts64")
  await expect(texts).toHaveAttribute("aria-pressed", "true")
})

test("staff replies complete text conversations and new texts return to the inbox", async ({ page }) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  const phone = "+15550509999"
  await sendInbound(page, "review-version-first", "Initial synthetic question", phone)
  await railFilter(page, "texts").click()
  const row = patientRow(page, phone)
  await expect(row).toBeVisible({ timeout: 30_000 })
  await expect(row.getByTestId("patient-row-name")).toHaveText("(555) 050-9999")
  await expect(row.getByTestId("patient-row-reason")).toHaveText("Initial synthetic question")
  await row.getByRole("button").first().click()
  await expect(page.getByTestId("context-panel")).toHaveAttribute("data-state", "closed")
  await expect(page.getByRole("button", { name: "Mark done", exact: true })).toBeVisible()
  await page.getByRole("textbox", { name: "Message", exact: true }).fill("Here is the answer to your synthetic question.")
  await page.getByRole("button", { name: "Send message", exact: true }).click()
  await expect(page.getByRole("article").filter({ hasText: "Here is the answer to your synthetic question." }).getByText("Sent", { exact: true })).toBeVisible()
  await expect(row).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Reopen", exact: true })).toBeVisible()
  await sendInbound(page, "review-version-second", "Another synthetic question", phone)
  await expect(row).toBeVisible()
  await row.getByRole("button").first().click()
  const reviewID = await row.getAttribute("data-task-id")
  await page.getByRole("button", { name: "Mark done", exact: true }).click()
  await expect(row).toHaveCount(0)
  await page.getByRole("button", { name: "Recently completed", exact: true }).click()
  const completedReview = page.locator(`[data-task-id="${reviewID}"]`)
  await completedReview.getByRole("button").first().click()
  await page.getByRole("button", { name: "Reopen", exact: true }).click()
  await expect(completedReview).toBeVisible()
})

test("refresh and back keep the selected Task", async ({ page }) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await sendInbound(page, "location-first", "First synthetic location question", "+15550601111")
  await sendInbound(page, "location-second", "Second synthetic location question", "+15550602222")
  await railFilter(page, "texts").click()
  const first = patientRow(page, "+15550601111")
  const second = patientRow(page, "+15550602222")
  await expect(first).toBeVisible({ timeout: 30_000 })
  await expect(second).toBeVisible()
  const firstID = await first.getAttribute("data-task-id")
  const secondID = await second.getAttribute("data-task-id")
  await first.getByRole("button").first().click()
  await expect(page).toHaveURL(new RegExp(`/workspace\\?task=${firstID}$`))
  await second.getByRole("button").first().click()
  await expect(page).toHaveURL(new RegExp(`/workspace\\?task=${secondID}$`))
  await page.reload()
  await expect(page.getByRole("article").filter({ hasText: "Second synthetic location question" })).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/workspace\\?task=${secondID}$`))
  await page.goBack()
  await expect(page).toHaveURL(new RegExp(`/workspace\\?task=${firstID}$`))
  await expect(page.getByRole("article").filter({ hasText: "First synthetic location question" })).toBeVisible()
  await expect(page.getByRole("article").filter({ hasText: "Second synthetic location question" })).toHaveCount(0)
})
