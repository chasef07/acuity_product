import { readFile } from "node:fs/promises"

import { expect, test, type Page } from "@playwright/test"

import { latestEmail } from "./support"

const telnyxFixtureURL =
  process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT

test("Slice 5 sends, receives, and turns exact-phone correspondence into explicit work", async ({
  context,
  page,
}) => {
  test.setTimeout(180_000)
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  const provisioned = JSON.parse(
    await readFile(provisioningOutput!, "utf8"),
  ) as {
    invitations: Array<{ email: string; token: string }>
  }
  const invitation = provisioned.invitations.find(
    (item) => item.email === "messaging@abita.test",
  )
  expect(invitation?.token).toBeTruthy()

  await signUp(
    page,
    "messaging@abita.test",
    invitation!.token,
    "Fixture Messaging Staff",
  )

  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toContainText("Abita Eye Group")
  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toContainText("Fixture Location 1")
  await expect(page.getByRole("tablist", { name: "Work state" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: /^Tasks/ })).toHaveAttribute(
    "aria-expanded",
    "true",
  )
  await expect(page.getByRole("button", { name: /^Missed Calls/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Voicemails/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Bookings/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Cancellations/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Reschedules/ })).toBeVisible()
  await expect(page.getByRole("button", { name: /^Texts/ })).toBeVisible()
  await expect(page.getByRole("button", { name: "Recent" })).toBeVisible()
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
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)

  await expect(
    page.getByRole("button", { name: /^Follow up on text \(727\)/ }),
  ).toHaveCount(0)
  await inbound.getByRole("button", { name: "Create Task" }).click()
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
  await contextPanel.getByRole("button", { name: "Close context panel" }).click()
  await expect(contextPanel).not.toBeVisible()

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
  const recentSection = page.getByRole("button", { name: "Recent", exact: true })
  if ((await recentSection.getAttribute("aria-expanded")) === "false") {
    await recentSection.click()
  }
  await page
    .getByRole("button", { name: /\(727\) 555-0199/ })
    .last()
    .click()
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

  await sendInbound(page, "slice-5-start", "START")
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await expect(
    page.getByText("Outbound messaging is blocked after STOP"),
  ).not.toBeVisible()
})

async function openNumberInbox(
  page: Page,
  phone: string,
) {
  await page.getByLabel("Search phone number").fill(phone)
  await page.getByLabel("Search phone number").press("Enter")
  await expect(page.getByRole("heading", { name: /\(727\) 555-01\d\d/ })).toBeVisible()
}

async function signUp(
  page: Page,
  email: string,
  invitationToken: string,
  name: string,
) {
  await page.goto(`/invite#${invitationToken}`)
  await page.getByLabel("Your name").fill(name)
  await page.getByLabel("Create password").fill("fixture-password-1234")
  await page.getByLabel("Confirm password").fill("fixture-password-1234")
  await page.getByRole("button", { name: "Create private account" }).click()
  const verificationURL = await latestEmail(page, email, "verification")
  await page.goto(verificationURL)
  await page.getByRole("button", { name: "Use email instead" }).click()
  await page.getByLabel("Email").fill(email)
  await page.getByLabel("Password").fill("fixture-password-1234")
  await page.getByRole("button", { name: "Sign in" }).click()
  await expect(page.getByTestId("mounted-workspace")).toBeVisible()
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
