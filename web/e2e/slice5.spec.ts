import { expect, test, type Page } from "@playwright/test"

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

  const searchInput = page.getByLabel("Search tasks, names, or phone")
  const submitButton = page.getByRole("button", { name: "Search" })
  await expect(submitButton).toBeVisible()
  await searchInput.fill("7275550199")
  await submitButton.click()
  await page.keyboard.press("Escape")

  await expect(page.getByRole("heading", { name: "(727) 555-0199" })).toBeVisible()
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
  await expect(bookingsSection).toHaveAttribute("aria-expanded", "false")
  await expect(cancellationsSection).toHaveAttribute("aria-expanded", "true")

  await expect(appointmentsSection).toContainText("1")
  await bookingsSection.click()
  const bookingReview = page.getByRole("button", {
    name: /\(727\) 555-0188/,
  })
  await expect(bookingReview).toBeVisible()
  await bookingReview.click()
  await expect(page.getByRole("heading", { name: "Appointment booked" })).toBeVisible()
  await expect(bookingReview).toHaveCount(0)
  await expect(appointmentsSection).toContainText("0")
})

test("Slice 5 sends, receives, and keeps exact-phone correspondence in one inbox", async ({
  context,
  page,
}) => {
  test.setTimeout(180_000)
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
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
  await expect(firstThread.getByLabel("Unread message")).toBeVisible()
  await firstThread.click()
  const inbound = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inbound).toBeVisible()
  await expect(
    page
      .getByTestId("text-attention-row")
      .filter({ hasText: "(727) 555-0199" }),
  ).toHaveCount(0)

  await expect(
    page.getByRole("button", { name: /^Follow up on text \(727\)/ }),
  ).toHaveCount(0)
  await expect(
    inbound.getByRole("button", { name: "Create Task" }),
  ).toHaveCount(0)

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

  await inbound.hover()
  await inbound.getByRole("button", { name: "Message actions" }).click()
  await page.getByRole("menuitem", { name: "Create task" }).click()
  const taskTouchpoint = page.getByRole("button", {
    name: /Fixture Location 1 · Task · Created.*Follow up on text/,
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
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .click()
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
      name: /Fixture Location 1 · Task · Completed.*Follow up on text/,
    })
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
  const taskCategory = page.getByLabel("Task category")
  await expect(taskCategory).toHaveValue("all")
  await expect(page.getByRole("button", { name: /Review billing balance/ })).toBeVisible()
  await createAIStaffTask(page, "medication", "Review medication refill")
  await expect(page.getByRole("button", { name: /Review medication refill/ })).toBeVisible()
  await taskCategory.selectOption("billing")
  await expect(page.getByRole("button", { name: /Review billing balance/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /Review medication refill/ })).toHaveCount(0)
  await expect(
    page.getByRole("button", { name: /^Missed Calls \d+$/ }),
  ).toBeVisible()
  await taskCategory.selectOption("all")
  await expect(page.getByRole("button", { name: /Review medication refill/ })).toBeVisible()
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
) {
  const suffix = category === "billing" ? "billing" : "medication"
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
