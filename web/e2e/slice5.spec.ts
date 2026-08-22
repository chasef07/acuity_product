import { expect, test, type Page, type Route } from "@playwright/test"

import { signInAs } from "./support"

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
  await page.getByRole("button", { name: "Toggle Sidebar" }).click()

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
  await searchInput.fill("7275550199")
  await submitButton.click()
  await page.keyboard.press("Escape")

  await expect(page.getByRole("heading", { name: "(727) 555-0199" })).toBeVisible()
  await expect(
    page.getByRole("status", { name: "Loading conversation" }),
  ).toBeVisible()
  releaseTimeline()
  await expect(page.getByText("No activity yet", { exact: true })).toBeVisible()
  await page.unroute(/\/v1\/engagements\/[^/]+\/timeline/, delayedTimeline)
  await expect(page.getByLabel("Sender office")).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Call", exact: true }),
  ).toBeInViewport({ ratio: 1 })

  await page.setViewportSize({ width: 1280, height: 720 })
  await expect(page.locator('button[aria-label="Search"]')).toBeHidden()
})

test("appointment reviews are nested and leave the queue when opened", async ({
  page,
}) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()
  await createAIAppointmentReview(page)
  await page.reload()
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()

  const appointmentsSection = page.getByRole("button", {
    name: /^Appointments/,
  })
  await expect(appointmentsSection).toHaveAttribute("aria-expanded", "false")
  await expect(page.getByRole("button", { name: /^Bookings/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Cancellations/ })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Reschedules/ })).toHaveCount(0)

  await appointmentsSection.click()
  const tasksSection = page.getByRole("button", { name: /^Tasks/ })
  await tasksSection.click()
  await expect(appointmentsSection).toHaveAttribute("aria-expanded", "true")
  await expect(tasksSection).toHaveAttribute("aria-expanded", "true")
  const bookingsSection = page.getByRole("button", { name: /^Bookings/ })
  const cancellationsSection = page.getByRole("button", {
    name: /^Cancellations/,
  })
  await expect(bookingsSection).toHaveAttribute("aria-expanded", "false")
  await expect(cancellationsSection).toHaveAttribute("aria-expanded", "false")
  await expect(page.getByRole("button", { name: /^Reschedules/ })).toHaveAttribute(
    "aria-expanded",
    "false",
  )
  await bookingsSection.click()
  await expect(bookingsSection).toHaveAttribute("aria-expanded", "true")
  await cancellationsSection.click()
  await expect(bookingsSection).toHaveAttribute("aria-expanded", "true")
  await expect(cancellationsSection).toHaveAttribute("aria-expanded", "true")

  await expect(appointmentsSection).toContainText("1")
  const bookingReview = page.getByRole("button", {
    name: /\(727\) 555-0188/,
  })
  await expect(bookingReview).toBeVisible()
  await bookingReview.click()
  await expect(page.getByRole("heading", { name: "Appointment booked" })).toBeVisible()
  await expect(bookingReview).toHaveCount(0)
  await expect(appointmentsSection).toContainText("0")
})

test("rail hover details and the message composer preserve compact context", async ({
  page,
}) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()

  await createAIStaffTask(
    page,
    "billing",
    "Review billing balance",
    "hover-details",
  )
  await page.reload()
  const tasksSection = page.getByRole("button", { name: /^Tasks/ })
  if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
    await tasksSection.click()
  }
  const taskRow = page.getByRole("button", {
    name: /^Review billing balance/,
  })
  await taskRow.hover()
  const hoverDetails = page.getByTestId("rail-hover-details")
  await expect(hoverDetails).toContainText("(727) 555-0196")
  await expect(hoverDetails).toContainText("Fixture Location 1")
  await expect(hoverDetails).toHaveCSS("opacity", "1")

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
  const sendButton = page.getByRole("button", { name: "Send message" })
  const sendButtonBox = await sendButton.boundingBox()
  expect(sendButtonBox?.width).toBe(sendButtonBox?.height)
  const sendButtonRadius = await sendButton.evaluate((element) =>
    Number.parseFloat(getComputedStyle(element).borderTopLeftRadius),
  )
  expect(sendButtonRadius).toBeGreaterThanOrEqual((sendButtonBox?.width ?? 0) / 2)
  await page.getByRole("textbox", { name: "Message", exact: true }).fill(
    "Confirm appointment availability",
  )
  await expect(sendButton).toBeEnabled()
  await expect(sendButton).toHaveCSS("opacity", "1")
  await expect(sendButton).toHaveCSS("background-color", "rgb(34, 34, 34)")

  await taskRow.hover()
  await page
    .getByRole("button", { name: "Complete Task: Review billing balance" })
    .click()
  await expect(taskRow).toHaveCount(0)
})

test("Slice 5 sends, receives, and keeps exact-phone correspondence in one inbox", async ({
  context,
  page,
}) => {
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
            callingEnabled: false,
          },
        ],
      },
    })
  })
  await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()

  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toContainText("Abita Eye Group")
  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toContainText("Fixture Location 1")
  await expect(page.getByRole("tablist", { name: "Work state" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Tasks/ })).toHaveAttribute(
    "aria-expanded",
    "false",
  )
  await expect(
    page.getByRole("button", { name: /^Missed Calls \d+$/ }),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: /^Texts/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "New text" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Call", exact: true })).toHaveCount(0)
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
  await page.getByRole("button", { name: "Send message" }).click()

  const outgoing = page
    .getByRole("article")
    .filter({ hasText: outgoingText })
  await expect(outgoing).toBeVisible()
  await expect(outgoing.getByText("Sending", { exact: true })).toBeVisible()
  await expect(outgoing.getByText("Sent", { exact: true })).toBeVisible()
  await expect(
    outgoing.getByRole("img", { name: "fixture-photo.png" }),
  ).toBeVisible()

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
  await sendInbound(page, "slice-5-inbound", inboundText)
  const textsSection = page.getByRole("button", { name: /^Texts/ })
  if ((await textsSection.getAttribute("aria-expanded")) === "false") {
    await textsSection.click()
  }
  const firstThread = page
    .getByRole("button", { name: /\(727\) 555-0199/ })
    .first()
  await expect(page.getByText("Correspondence ledger", { exact: true })).toHaveCount(0)
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)
  await firstThread.click()
  const inbound = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inbound).toBeVisible()
  await expect(page.getByText("Today", { exact: true }).first()).toBeVisible()
  await expect(
    page
      .getByTestId("text-attention-row")
      .filter({ hasText: "(727) 555-0199" }),
  ).toHaveCount(0)

  await expect(
    page.getByRole("button", { name: "Follow up on text", exact: true }),
  ).toHaveCount(0)

  await page.setViewportSize({ width: 390, height: 844 })
  const mobileCopy = inbound.getByRole("button", { name: "Copy message" })
  const mobileCreateTask = inbound.getByRole("button", { name: "Create task" })
  await expect(mobileCopy).toHaveCSS("opacity", "1")
  await expect(mobileCreateTask).toHaveCSS("opacity", "1")
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
  await timeline.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  const latestScrollTop = await timeline.evaluate((element) => element.scrollTop)
  expect(latestScrollTop).toBeGreaterThan(0)
  await timeline.hover()
  await page.mouse.wheel(0, -1_000)
  await expect
    .poll(() => timeline.evaluate((element) => element.scrollTop))
    .toBeLessThan(latestScrollTop)
  const scrollToLatest = page.getByRole("button", { name: "Scroll to end" })
  await expect(scrollToLatest).toBeVisible()
  await scrollToLatest.click({ timeout: 10_000 })
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
  await expect(copyMessage).toHaveCSS("opacity", "0")
  await inbound.hover()
  await expect(copyMessage).toHaveCSS("opacity", "1")
  await expect(createTask).toHaveCSS("opacity", "1")
  await copyMessage.click()
  await expect(inbound.getByRole("status")).toContainText("Message copied")
  await inbound.focus()
  await expect(createTask).toHaveCSS("opacity", "1")
  await createTask.click()
  const taskTouchpoint = page.getByRole("button", {
    name: /Open task: Follow up on text/,
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

  const tasksSection = page.getByRole("button", { name: /^Tasks/ })
  if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
    await tasksSection.click()
  }
  const sidebarTask = page.getByRole("button", {
    name: "Follow up on text",
    exact: true,
  })
  await expect(sidebarTask).toBeVisible()
  await sidebarTask.click()
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
  await expect(sidebarTaskContext).toBeVisible()
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
    .getByRole("button", { name: "Complete", exact: true })
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
      name: /Open task: Follow up on text/,
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
  await completedTaskContext.getByRole("button", { name: "Reopen" }).click()
  await expect(
    completedTaskContext.getByRole("button", { name: "Complete" }),
  ).toBeVisible()

  await sendInbound(page, "slice-5-stop", "STOP")
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

  await sendInbound(page, "slice-5-start", "START")
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await expect(
    page.getByText("Outbound messaging is blocked after STOP"),
  ).not.toBeVisible()

  if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
    await tasksSection.click()
  }
  await createAIStaffTask(page, "billing", "Review billing balance")
  const billingTaskButton = page.getByRole("button", {
    name: "Review billing balance",
    exact: true,
  })
  await expect(billingTaskButton).toBeVisible()
  const taskCountsResponse = page.waitForResponse(
    (response) =>
      response.url() === `${portalURL}/v1/tasks/query` &&
      response.request().method() === "POST" &&
      response.ok(),
  )
  await createAIStaffTask(page, "medication", "Review medication refill")
  const counts = ((await (await taskCountsResponse).json()) as {
    counts: {
      tasks: number
      categories: Record<
        | "billing"
        | "appointments"
        | "documentation"
        | "optical"
        | "medication"
        | "referrals"
        | "other",
        number
      >
    }
  }).counts
  const medicationTaskButton = page.getByRole("button", {
    name: "Review medication refill",
    exact: true,
  })
  await expect(medicationTaskButton).toBeVisible()

  const taskFilter = page.getByRole("button", {
    name: "Filter Tasks: All types",
  })
  await taskFilter.click()
  for (const [label, count] of [
    ["All types", counts.tasks],
    ["Billing", counts.categories.billing],
    ["Appointments", counts.categories.appointments],
    ["Documentation", counts.categories.documentation],
    ["Optical", counts.categories.optical],
    ["Medication", counts.categories.medication],
    ["Referrals", counts.categories.referrals],
    ["Other", counts.categories.other],
  ] as const) {
    await expect(
      page.getByRole("menuitemradio", { name: `${label} ${count}` }),
    ).toBeVisible()
  }
  await page.getByRole("menuitemradio", { name: /^Billing / }).click()
  await expect(
    page.getByRole("button", { name: "Filter Tasks: Billing" }),
  ).toBeVisible()
  await expect(billingTaskButton).toBeVisible()
  await expect(medicationTaskButton).toHaveCount(0)
  await expect(
    page.getByRole("button", { name: /^Missed Calls \d+$/ }),
  ).toBeVisible()

  await page.reload()
  const restoredTasksSection = page.getByRole("button", { name: /^Tasks/ })
  if ((await restoredTasksSection.getAttribute("aria-expanded")) === "false") {
    await restoredTasksSection.click()
  }
  await expect(
    page.getByRole("button", { name: "Filter Tasks: Billing" }),
  ).toBeVisible()
  await expect(billingTaskButton).toBeVisible()
  await expect(medicationTaskButton).toHaveCount(0)

  const contextClose = page.getByRole("button", { name: "Close context panel" })
  if (await contextClose.isVisible()) await contextClose.click()
  const taskItem = page
    .getByTestId("task-row")
    .filter({ hasText: "Review billing balance" })
  const taskRow = taskItem.getByRole("button", {
    name: /^Review billing balance/,
  })
  const completeTask = taskItem.getByRole("button", {
    name: "Complete Task: Review billing balance",
  })
  const relativeTime = taskItem.locator("time")
  await expect(completeTask).toHaveCSS("opacity", "0")
  const beforeHover = await taskRow.boundingBox()
  await taskRow.hover()
  const taskHoverDetails = page.getByTestId("rail-hover-details")
  await expect(taskHoverDetails).toContainText("(727) 555-0196")
  await expect(taskHoverDetails).toContainText("Fixture Location 1")
  await expect(completeTask).toHaveCSS("opacity", "1")
  await expect(relativeTime).toHaveCSS("opacity", "0")
  expect(await taskRow.boundingBox()).toEqual(beforeHover)

  await taskRow.focus()
  await expect(completeTask).toHaveCSS("opacity", "1")
  await page.keyboard.press("Tab")
  await expect(completeTask).toBeFocused()
  await page.emulateMedia({ reducedMotion: "reduce" })
  await expect(completeTask).toHaveCSS("transition-duration", "0s")
  await expect(relativeTime).toHaveCSS("transition-duration", "0s")

  await page.evaluate(() => {
    const channel = new BroadcastChannel("acuity-auth-token")
    channel.postMessage("clear-access-token")
    channel.close()
  })
  await page.route("**/api/auth/token", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: "temporarily unavailable" }),
    }),
  )
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
  await expect(page.getByTestId("context-panel")).toHaveAttribute(
    "data-state",
    "closed",
  )
  await expect(
    taskItem.getByRole("alert").filter({ hasText: "changed elsewhere" }),
  ).toBeVisible()

  await taskRow.hover()
  await completeTask.click()
  await expect(taskItem).toBeVisible()
  await expect(
    taskItem.getByRole("alert").filter({ hasText: "could not be completed" }),
  ).toBeVisible()

  await taskRow.hover()
  await completeTask.click()
  await expect(completeTask).toHaveAttribute("aria-busy", "true")
  await expect(completeTask.getByRole("status", { name: "Loading" })).toBeVisible()
  await expect(taskItem).toBeVisible()
  releaseSuccess()
  await expect(taskItem).toHaveCount(0)
  await page.unroute(/\/v1\/tasks\/[^/]+\/complete$/, completionRoute)

  await page.getByRole("button", { name: "Filter Tasks: Billing" }).click()
  await page.getByRole("menuitemradio", { name: /^Medication / }).click()
  await page.keyboard.press("Escape")
  const medicationItem = page
    .getByTestId("task-row")
    .filter({ hasText: "Review medication refill" })
  const completeMedication = medicationItem.getByRole("button", {
    name: "Complete Task: Review medication refill",
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
  await page.route(/\/v1\/tasks\/query$/, taskQueryRoute)
  await page.route(
    /\/v1\/tasks\/[^/]+\/complete$/,
    staleCompletionRoute,
  )

  await expect(medicationItem).toBeVisible()
  await medicationItem.hover()
  await completeMedication.click()
  await expect(completeMedication).toHaveAttribute("aria-busy", "true")
  await page.getByRole("button", { name: "Workspace selector" }).click()
  await page
    .getByRole("button", { name: "Stale Callback Location", exact: true })
    .click()
  await expect(page.getByTestId("task-row")).toHaveCount(0)
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

async function sendInbound(page: Page, eventID: string, text: string) {
  const response = await page.request.post(
    `${telnyxFixtureURL}/fixture/message-inbound`,
    {
      headers: { authorization: "Bearer fixture-control" },
      data: {
        eventId: eventID,
        providerMessageId: `provider-${eventID}`,
        from: "+17275550199",
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
      callId: `slice-5-${suffix}`,
      callerPhone: category === "billing" ? "+17275550196" : "+17275550195",
      category,
      idempotencyKey: `slice-5-${suffix}`,
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

async function createAIAppointmentReview(page: Page) {
  const occurredAt = new Date()
  const startedAt = new Date(occurredAt.getTime() - 60_000)
  const response = await page.request.post(`${portalURL}/v1/ai/interactions`, {
    headers: { authorization: "Bearer synthetic-production-token" },
    data: {
      kind: "CLOSEOUT",
      officeKey: "spring-hill",
      sourceCallId: "slice-5-booking-review",
      callerPhone: "+17275550188",
      officePhone: "+17275550101",
      startedAt: startedAt.toISOString(),
      endedAt: occurredAt.toISOString(),
      status: "COMPLETED",
      summary: "Caller booked an appointment.",
      closeoutPayload: { callId: "slice-5-booking-review" },
      appointmentOutcome: {
        action: "BOOKED",
        occurredAt: occurredAt.toISOString(),
        newAppointmentId: "slice-5-booking",
        bookingResult: {
          status: "booked",
          appointmentId: "slice-5-booking",
        },
      },
    },
  })
  expect([200, 201]).toContain(response.status())
}
