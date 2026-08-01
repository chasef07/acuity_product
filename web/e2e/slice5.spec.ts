import { readFile } from "node:fs/promises"

import { expect, test, type Page } from "@playwright/test"

import { latestEmail } from "./support"

const telnyxFixtureURL =
  process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT

test("Slice 5 sends, receives, and turns exact-phone correspondence into explicit work", async ({
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

  await page.getByRole("button", { name: "Messages", exact: true }).click()
  await expect(page.getByLabel("Location")).toHaveValue(/.+/)
  const newText = page.getByRole("button", { name: "New text" })
  await expect(newText).toBeVisible()
  await newText.click()

  const outgoingText = "Your records are ready for pickup."
  await page.getByLabel("Destination phone number").fill("+1 (727) 555-0199")
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

  await page.getByRole("button", { name: "New text" }).click()
  await page.getByLabel("Destination phone number").fill("+1 (727) 555-0198")
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
  const firstThread = page
    .getByRole("button", { name: /\(727\) 555-0199/ })
    .first()
  await expect(firstThread.getByLabel("Unread message")).toBeVisible()
  await firstThread.click()
  const inbound = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inbound).toBeVisible()
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)

  await page.getByRole("button", { name: "Tasks", exact: true }).click()
  await expect(
    page.getByRole("button", { name: /^Follow up on text \(727\)/ }),
  ).toHaveCount(0)
  await page.getByRole("button", { name: "Messages", exact: true }).click()
  await inbound.getByRole("button", { name: "Create Task" }).click()
  await expect(page.getByText("Task · Open")).toBeVisible()

  await page.getByRole("button", { name: "Tasks", exact: true }).click()
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .click()
  await expect(
    page.getByRole("heading", { name: "Follow up on text", exact: true }),
  ).toBeVisible()
  const taskConversation = page.getByRole("region", {
    name: "Task conversation",
  })
  await page.reload()
  await expect(taskConversation).toBeVisible()
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .click()
  await expect(
    page.getByRole("heading", { name: "Follow up on text", exact: true }),
  ).toBeVisible()
  await expect(taskConversation).toContainText(inboundText)
  const taskReply = "I will call you with the pickup time."
  await taskConversation
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(taskReply)
  await taskConversation.getByRole("button", { name: "Send message" }).click()
  await expect(
    taskConversation.getByRole("article").filter({ hasText: taskReply }),
  ).toBeVisible()

  await page.getByRole("button", { name: "Complete" }).click()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeDisabled()
  await expect(
    page.getByText("Reopen this Task to send a message"),
  ).toBeVisible()
  await page.getByRole("button", { name: "Messages", exact: true }).click()
  await page
    .getByRole("button", { name: /\(727\) 555-0199/ })
    .first()
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
  await page.getByRole("button", { name: "Tasks", exact: true }).click()
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .click()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeDisabled()
  await page.getByRole("button", { name: "Reopen" }).click()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()

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
