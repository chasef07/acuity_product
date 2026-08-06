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

  await expect(
    page.getByRole("button", { name: "Workspace selector" }),
  ).toContainText("Abita Eye Group · Fixture Location 1")
  await expect(
    page.getByRole("button", { name: /^Tasks \d+$/ }),
  ).toHaveAttribute("aria-expanded", "true")
  await expect(
    page.getByRole("button", {
      name: /^Missed Calls & Voicemails \d+$/,
    }),
  ).toHaveAttribute("aria-expanded", "true")
  await expect(
    page.getByRole("button", { name: /^Texts \d+$/ }),
  ).toHaveAttribute("aria-expanded", "true")
  await expect(
    page.getByRole("button", { name: "Recent", exact: true }),
  ).toHaveAttribute("aria-expanded", "false")
  await expect(
    page.getByRole("button", { name: "Messages", exact: true }),
  ).toHaveCount(0)
  const initialTaskCount = await attentionCount(page, "Tasks")
  const initialTextCount = await attentionCount(page, "Texts")
  const newMessage = page.getByRole("button", { name: "New message" })
  await newMessage.click()

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

  await page.getByRole("button", { name: "New message" }).click()
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
  await expect(page.getByText("Correspondence ledger", { exact: true })).toHaveCount(0)
  await expect(
    page.getByRole("button", {
      name: `Texts ${initialTextCount + 1}`,
      exact: true,
    }),
  ).toBeVisible()
  await expect(firstThread.getByLabel("Unread message")).toBeVisible()
  await firstThread.click()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(page.getByText("Engagement History", { exact: true })).toHaveCount(0)
  const inbound = page
    .getByRole("article")
    .filter({ hasText: inboundText })
  await expect(inbound).toBeVisible()
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)

  for (let index = 0; index < 16; index += 1) {
    await sendInbound(
      page,
      `slice-5-scroll-fill-${index}`,
      `Earlier context line ${index + 1}.`,
    )
  }
  await expect(
    page.getByRole("article").filter({ hasText: "Earlier context line 16." }),
  ).toBeVisible()
  const timelineScroller = page.getByTestId("message-timeline")
  await expect
    .poll(() =>
      timelineScroller.evaluate(
        (element) => element.scrollHeight - element.clientHeight,
      ),
    )
    .toBeGreaterThan(100)
  await timelineScroller.hover()
  await page.mouse.wheel(0, -10_000)
  await expect
    .poll(() =>
      timelineScroller.evaluate(
        (element) =>
          element.scrollHeight - element.scrollTop - element.clientHeight,
      ),
    )
    .toBeGreaterThan(72)
  const preservedScrollTop = await timelineScroller.evaluate(
    (element) => element.scrollTop,
  )
  const unseenText = "New activity while reviewing older history."
  await sendInbound(page, "slice-5-scroll-unseen", unseenText)
  await expect(firstThread.getByLabel("Unread message")).toBeVisible()
  await expect(
    page.getByRole("article").filter({ hasText: unseenText }),
  ).toHaveCount(0)
  await expect
    .poll(async () =>
      Math.abs(
        (await timelineScroller.evaluate((element) => element.scrollTop)) -
          preservedScrollTop,
      ),
    )
    .toBeLessThanOrEqual(4)
  await expect(
    page.getByRole("button", { name: "New activity", exact: true }),
  ).toHaveCount(0)
  await timelineScroller.evaluate((element) => {
    element.scrollTop = element.scrollHeight
  })
  await expect(
    page.getByRole("article").filter({ hasText: unseenText }),
  ).toBeVisible()
  await expect(firstThread.getByLabel("Unread message")).toHaveCount(0)

  await expect(
    page.getByRole("button", { name: /^Follow up on text \(727\)/ }),
  ).toHaveCount(0)
  await inbound.hover()
  await inbound.getByRole("button", { name: "Create Task" }).click()
  await expect(
    page.getByRole("button", {
      name: /Task: Follow up on text\. Created/,
    }),
  ).toBeVisible()

  await expect(
    page.getByRole("button", {
      name: `Tasks ${initialTaskCount + 1}`,
      exact: true,
    }),
  ).toBeVisible()
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .first()
    .click()
  await expect(
    page.getByRole("heading", { name: "Follow up on text", exact: true }),
  ).toBeVisible()
  const selectedItem = page.getByRole("complementary", {
    name: "Selected item",
  })
  await expect(selectedItem).toContainText("Follow up on text")
  await expect(selectedItem.getByText(/^Created/)).toHaveCount(0)
  await expect(selectedItem.getByText(/^Last changed/)).toHaveCount(0)
  await expect(selectedItem.getByText(/Task · v/)).toHaveCount(0)
  await expect(page.getByText("More context", { exact: true })).toHaveCount(0)
  const taskConversation = page.getByTestId("message-timeline")
  await page.reload()
  await expect(taskConversation).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(selectedItem).toHaveCount(0)
  const numberSearch = page.getByRole("textbox", { name: "Search numbers" })
  await numberSearch.fill("727")
  await expect(
    page.getByText("Enter a full phone number.", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText("No authorized recorded activity for that number.", {
      exact: true,
    }),
  ).toHaveCount(0)
  let prefixSearchRequests = 0
  await page.route("**/v1/engagements/query", async (route) => {
    const body = route.request().postDataJSON() as { phone?: string }
    if (body.phone !== "+44 20 7183") {
      await route.continue()
      return
    }
    prefixSearchRequests += 1
    await route.abort()
  })
  await numberSearch.fill("+44 20 7183")
  await expect(
    page.getByText("Enter a full phone number.", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText("No authorized recorded activity for that number.", {
      exact: true,
    }),
  ).toHaveCount(0)
  await page.waitForTimeout(300)
  expect(prefixSearchRequests).toBe(0)
  await numberSearch.press("Enter")
  await expect(
    page.getByText("Number search is unavailable. Try again.", { exact: true }),
  ).toBeVisible()
  await expect(
    page.getByText("No authorized recorded activity for that number.", {
      exact: true,
    }),
  ).toHaveCount(0)
  expect(prefixSearchRequests).toBe(1)
  await page.unroute("**/v1/engagements/query")
  await numberSearch.fill("+44 20 7183 8750")
  await numberSearch.press("Enter")
  await expect(
    page.getByText("No authorized recorded activity for that number.", {
      exact: true,
    }),
  ).toBeVisible()
  await numberSearch.fill("727.555.0199")
  await numberSearch.press("Enter")
  const numberSearchResults = page.getByTestId("number-search-results")
  const originalSearchResult = numberSearchResults.getByRole("button", {
    name: /\(727\) 555-0199/,
  })
  await expect(originalSearchResult).toBeVisible()
  await numberSearch.fill("727.555.0198")
  await expect(originalSearchResult).toHaveCount(0)
  await numberSearch.press("Enter")
  await expect(
    numberSearchResults.getByRole("button", { name: /\(727\) 555-0198/ }),
  ).toBeVisible()
  await numberSearch.fill("727.555.0199")
  await numberSearch.press("Enter")
  await numberSearchResults
    .getByRole("button", { name: /\(727\) 555-0199/ })
    .click()
  await expect(page.getByRole("button", { name: "Outbound call" })).toBeVisible()
  await expect(page.getByRole("button", { name: "New message" })).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  const recent = page.getByRole("button", { name: "Recent", exact: true })
  await recent.click()
  await expect(recent).toHaveAttribute("aria-expanded", "true")
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .first()
    .click()
  await expect(
    page.getByRole("heading", { name: "Follow up on text", exact: true }),
  ).toBeVisible()
  await expect(taskConversation).toContainText(inboundText)
  const taskReply = "I will call you with the pickup time."
  await page
    .getByRole("textbox", { name: "Message", exact: true })
    .fill(taskReply)
  await page.getByRole("button", { name: "Send message" }).click()
  await expect(
    page.getByRole("article").filter({ hasText: taskReply }),
  ).toBeVisible()
  await expect(
    page.getByRole("button", {
      name: `Texts ${initialTextCount + 1}`,
      exact: true,
    }),
  ).toBeVisible()
  await page.reload()
  await expect(
    page.getByRole("heading", { name: "(727) 555-0199", exact: true }),
  ).toBeVisible()
  await expect(
    page.getByRole("button", {
      name: `Texts ${initialTextCount + 1}`,
      exact: true,
    }),
  ).toBeVisible()
  await page
    .getByRole("button", { name: "Looks handled — Mark complete" })
    .click()
  await expect(
    page.getByRole("button", {
      name: `Texts ${initialTextCount}`,
      exact: true,
    }),
  ).toBeVisible()
  await page.reload()
  await expect(
    page.getByRole("button", {
      name: `Texts ${initialTextCount}`,
      exact: true,
    }),
  ).toBeVisible()
  await page
    .getByRole("button", { name: /^Follow up on text \(727\)/ })
    .first()
    .click()

  await selectedItem.getByRole("button", { name: "Complete", exact: true }).click()
  await expect(
    page.getByRole("textbox", { name: "Message", exact: true }),
  ).toBeEnabled()
  await expect(
    page.getByRole("button", {
      name: `Tasks ${initialTaskCount}`,
      exact: true,
    }),
  ).toBeVisible()
  await expect(selectedItem).toHaveCount(0)
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
      name: /Task: Follow up on text\. Completed/,
    })
    .last()
    .click()
  await selectedItem.getByRole("button", { name: "Reopen" }).click()
  await expect(
    page.getByRole("button", {
      name: `Tasks ${initialTaskCount + 1}`,
      exact: true,
    }),
  ).toBeVisible()
  const consolidatedFollowUp = page
    .getByTestId("message-timeline")
    .getByRole("button", { name: /Task: Follow up on text\. Reopened/ })
  await expect(consolidatedFollowUp).toHaveCount(1)
  await expect(
    page
      .getByTestId("message-timeline")
      .getByRole("button", { name: /Task: Follow up on text\./ }),
  ).toHaveCount(1)
  await expect(
    consolidatedFollowUp.locator('[data-timeline-side="outbound"]'),
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

async function attentionCount(page: Page, section: string) {
  const label = await page
    .getByRole("button", { name: new RegExp(`^${section} \\d+$`) })
    .textContent()
  const count = label?.match(/(\d+)\s*$/)?.[1]
  expect(count).toBeTruthy()
  return Number(count)
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
