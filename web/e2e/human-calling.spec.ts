import {
  expect,
  test,
  type BrowserContext,
  type Page,
  type Request,
} from "@playwright/test"
import { Pool } from "pg"

import { signInAs } from "./support"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"
const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
const telnyxFixtureURL =
  process.env.E2E_TELNYX_FIXTURE_URL ?? "http://127.0.0.1:19000"
const provisioningOutput = process.env.E2E_PROVISIONING_OUTPUT
const databaseURL = process.env.E2E_DATABASE_URL

test("caller ringback is a stable public WAV", async ({ request }) => {
  const response = await request.get(`${webURL}/ringback.wav`)

  expect(response.ok()).toBeTruthy()
  expect(response.headers()["content-type"]).toBe("audio/wav")
  expect(response.headers()["cache-control"]).toContain("immutable")
  const audio = Buffer.from(await response.body())
	  expect(audio.byteLength).toBe(44 + 8_000 * 20 * 2)
  expect(audio.subarray(0, 4).toString("ascii")).toBe("RIFF")
  expect(audio.subarray(8, 12).toString("ascii")).toBe("WAVE")
})

test("production browser path fans out exact CallLegs and bridges one provider-confirmed winner", async ({
  browser,
  page: selectedPage,
}, testInfo) => {
  test.setTimeout(180_000)
  test.skip(
    !provisioningOutput || !databaseURL,
    "E2E_PROVISIONING_OUTPUT and E2E_DATABASE_URL are required",
  )
  const secondaryContext = await browser.newContext()
  const sameUserContext = await browser.newContext()
  await Promise.all([
    prepareBrowser(selectedPage.context()),
    prepareBrowser(secondaryContext),
    prepareBrowser(sameUserContext),
  ])
  const secondaryPage = await secondaryContext.newPage()
  const sameUserPage = await sameUserContext.newPage()
  const database = new Pool({ connectionString: databaseURL })

  try {
    await Promise.all([
      signInAs(
        selectedPage,
        "selected@abita.test",
        "Fixture Selected Staff",
      ),
      signInAs(
        secondaryPage,
        "secondary@abita.test",
        "Fixture Secondary Staff",
      ),
    ])
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_credentials credential
             JOIN auth."user" actor ON actor.id = credential.user_subject
            WHERE credential.state = 'ACTIVE'
              AND actor.email IN ('selected@abita.test', 'secondary@abita.test')`,
        )
        return Number(result.rows[0]?.count ?? 0)
      }, { timeout: 40_000 })
      .toBe(2)

    await Promise.all([
      ensureCallingAvailability(selectedPage),
      ensureCallingAvailability(secondaryPage),
    ])
    await signInAs(
      sameUserPage,
      "selected@abita.test",
      "Fixture Selected Staff",
    )
    await expect(
      sameUserPage.getByRole("button", { name: "Use this browser" }),
    ).toBeVisible({ timeout: 40_000 })
    await sameUserContext.close()

    const selectedAvailability = selectedPage.getByRole("switch", {
      name: "Availability",
    })
    await selectedAvailability.click()
    await expect(selectedAvailability).not.toBeChecked()
    await selectedPage.reload()
    const restoredAvailability = selectedPage.getByRole("switch", {
      name: "Availability",
    })
    await expect(restoredAvailability).not.toBeChecked()
    await restoredAvailability.click()
    await expect(restoredAvailability).toBeChecked()
    const scope = await database.query<{ practice_id: string; location_id: string }>(
      `SELECT practice.id::text AS practice_id, location.id::text AS location_id
         FROM access_practices practice
         JOIN access_locations location ON location.practice_id = practice.id
        WHERE practice.provisioning_key = 'abita-eye-group'
          AND location.provisioning_key = 'fixture-location-1'`,
    )
    expect(scope.rows[0]).toBeTruthy()
    const navigationTaskTitle = "Verify active Call navigation"
    const navigationTaskResponse = await selectedPage.request.post(
      `${portalURL}/v1/tasks`,
      {
        headers: { authorization: "Bearer synthetic-production-token" },
        data: {
          callId: "softphone-runtime-navigation-task",
          callerPhone: "+15555550121",
          category: "billing",
          idempotencyKey: "softphone-runtime-navigation-task",
          message: navigationTaskTitle,
          officeKey: "spring-hill",
          officePhone: "+17275550101",
          source: "agent",
          summary: navigationTaskTitle,
          urgency: "normal",
        },
      },
    )
    expect([200, 201]).toContain(navigationTaskResponse.status())
    const tasksSection = selectedPage.getByRole("button", { name: /^Tasks/ })
    if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
      await tasksSection.click()
    }
    const navigationTask = selectedPage.getByRole("button", {
      name: navigationTaskTitle,
      exact: true,
    })
    await expect(navigationTask).toBeVisible({ timeout: 20_000 })

    const handoffResponse = await selectedPage.request.post(`${portalURL}/v1/handoffs`, {
      headers: { authorization: "Bearer synthetic-production-token" },
      data: {
        practiceId: scope.rows[0].practice_id,
        locationId: scope.rows[0].location_id,
        sourceCallId: "callleg-browser-source",
        idempotencyKey: "callleg-browser-attempt",
        contact: {
          phone: "+15555550100",
          phoneSource: "Abita",
          displayName: "CallLeg Browser Caller",
          nameSource: "Abita",
          transferReason: "Prove exact browser fanout",
          reasonSource: "Abita AI",
        },
      },
    })
    expect(handoffResponse.status()).toBe(201)
    const handoff = (await handoffResponse.json()) as {
      sipDestination: string
    }
    expect(handoff.sipDestination).toBe(
      "sip:acuity-handoff@synthetic.sip.telnyx.com",
    )

    const occurredAt = new Date().toISOString()
    await deliverProviderEvent(selectedPage, {
      eventType: "call.initiated",
      eventId: "callleg-browser-caller-initiated",
      occurredAt,
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-caller-session",
        from: "+15555550100",
        to: "+14843989071",
      },
    })
    const callID = await expect
      .poll(async () => {
        const result = await database.query<{ id: string }>(
          `SELECT call.id::text
             FROM human_calling_calls call
             JOIN human_calling_call_legs caller
               ON caller.call_id = call.id AND caller.role = 'CALLER'
            WHERE caller.provider_call_leg_id = 'fixture-caller-leg'`,
        )
        return result.rows[0]?.id ?? ""
      })
      .not.toBe("")
      .then(async () => {
        const result = await database.query<{ id: string }>(
          `SELECT call_id::text AS id FROM human_calling_call_legs
            WHERE provider_call_leg_id = 'fixture-caller-leg'`,
        )
        return result.rows[0].id
      })

    await deliverProviderEvent(selectedPage, {
      eventType: "call.answered",
      eventId: "callleg-browser-caller-answered",
      occurredAt: new Date().toISOString(),
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-caller-session",
      },
    })

    const staffLegs = await readStaffLegs(database, callID)
    expect(staffLegs).toHaveLength(2)
    expect(staffLegs.map((leg) => leg.email).sort()).toEqual([
      "secondary@abita.test",
      "selected@abita.test",
    ])
    expect(staffLegs.every((leg) => leg.bridge_on_answer === false)).toBe(true)
    expect(new Set(staffLegs.map((leg) => leg.provider_leg_id)).size).toBe(2)

    await Promise.all([
      expect(callCenter(selectedPage)).toBeVisible(),
      expect(callCenter(secondaryPage)).toBeVisible(),
    ])
    for (const page of [selectedPage, secondaryPage]) {
      const offer = callCenter(page)
      await expect(
        offer.getByText("(555) 555-0100", { exact: false }),
      ).toBeVisible()
      await expect(
        offer.getByLabel("Incoming offer countdown for (555) 555-0100"),
      ).toHaveText(/^\d+s$/)
    }
    const deadlineResult = await database.query<{ deadline: Date }>(
      `SELECT ring.sent_at + interval '20 seconds' AS deadline
         FROM human_calling_provider_commands ring
        WHERE ring.call_id = $1 AND ring.action = 'START_RING_WINDOW'
          AND ring.sent_at IS NOT NULL`,
      [callID],
    )
    expect(deadlineResult.rows[0]?.deadline).toBeTruthy()
    const browserNow = await selectedPage.evaluate(() => Date.now())
    await selectedPage.clock.setFixedTime(deadlineResult.rows[0].deadline)
    await expect(callCenter(selectedPage)).toHaveCount(0)
    await selectedPage.clock.setFixedTime(browserNow)
    await expect(callCenter(selectedPage)).toBeVisible()
    const selectedLeg = staffLegs.find((leg) => leg.email === "selected@abita.test")!
    const secondaryLeg = staffLegs.find(
      (leg) => leg.email === "secondary@abita.test",
    )!
    const selectedAnswer = selectedPage.getByRole("button", {
      name: "Answer (555) 555-0100",
      exact: true,
    })
    const secondaryAnswer = secondaryPage.getByRole("button", {
      name: "Answer (555) 555-0100",
      exact: true,
    })
    await Promise.all([
      expect(selectedAnswer).toBeDisabled(),
      expect(secondaryAnswer).toBeDisabled(),
      expect(callCenter(selectedPage).getByRole("status")).toHaveText(
        "Incoming call",
      ),
      expect(callCenter(secondaryPage).getByRole("status")).toHaveText(
        "Incoming call",
      ),
      expect.poll(() => mediaAnswers(selectedPage)).toBe(0),
      expect.poll(() => mediaAnswers(secondaryPage)).toBe(0),
    ])
    const mismatchedMediaToken = "z".repeat(43)
    await sendIncomingLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      mismatchedMediaToken,
    )
    await expect(selectedAnswer).toBeDisabled()
    await expect.poll(() => mediaAnswers(selectedPage)).toBe(0)
    await endMediaLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      mismatchedMediaToken,
    )
    await sendIncomingLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      selectedLeg.media_token,
    )
    await expect(selectedAnswer).toBeEnabled()
    await endMediaLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      selectedLeg.media_token,
    )
    await Promise.all([
      expect(selectedAnswer).toBeDisabled(),
      expect(callCenter(selectedPage).getByRole("status")).toHaveText(
        "Incoming call",
      ),
      expect.poll(() => mediaAnswers(selectedPage)).toBe(0),
    ])
    await Promise.all([
      sendIncomingLeg(
        selectedPage,
        selectedLeg.provider_leg_id,
        selectedLeg.media_token,
      ),
      sendIncomingLeg(
        secondaryPage,
        secondaryLeg.provider_leg_id,
        secondaryLeg.media_token,
      ),
    ])
    await Promise.all([
      expect(selectedAnswer).toBeEnabled(),
      expect(secondaryAnswer).toBeEnabled(),
    ])
    await failNextMediaAnswer(selectedPage)
    await selectedAnswer.click()
    await Promise.all([
      expect.poll(() => mediaAnswers(selectedPage)).toBe(1),
      expect(selectedAnswer).toBeEnabled(),
      expect(
        callCenter(selectedPage).getByRole("alert"),
      ).toContainText("Calls are paused until then"),
      expect(
        callCenter(selectedPage).getByRole("button", {
          name: "Refresh page",
        }),
      ).toBeVisible(),
    ])
    await Promise.all([
      selectedPage.waitForEvent("framenavigated"),
      callCenter(selectedPage)
        .getByRole("button", { name: "Refresh page" })
        .click(),
    ])
    await expect(callCenter(selectedPage).getByRole("alert")).toHaveCount(0)
    await expect.poll(() => mediaConnections(selectedPage)).toBe(1)
    await sendIncomingLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      selectedLeg.media_token,
    )
    await expect(selectedAnswer).toBeEnabled()
    await deferMediaAnswer(secondaryPage, secondaryLeg.media_token)
    await Promise.all([
      selectedAnswer.click(),
      secondaryAnswer.dblclick(),
    ])
    await Promise.all([
      expect.poll(() => mediaAnswers(selectedPage)).toBe(1),
      expect.poll(() => mediaAnswers(secondaryPage)).toBe(1),
    ])

    await deliverProviderEvent(selectedPage, {
      eventType: "call.answered",
      eventId: "callleg-browser-staff-answered",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: selectedLeg.control_id,
        call_leg_id: selectedLeg.provider_leg_id,
        call_session_id: "fixture-staff-session",
        client_state: selectedLeg.client_state,
      },
    })
    const bridge = await readBridgeCommand(database, callID)
    await deliverProviderEvent(secondaryPage, {
      eventType: "call.answered",
      eventId: "callleg-browser-secondary-staff-answered",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: secondaryLeg.control_id,
        call_leg_id: secondaryLeg.provider_leg_id,
        call_session_id: "fixture-secondary-staff-session",
        client_state: secondaryLeg.client_state,
      },
    })
    expect(bridge.target_id).toBe(selectedLeg.control_id)
    expect(bridge.peer_call_leg_id).toBeTruthy()
    expect(bridge.prevent_double_bridge).toBe(true)
    expect(bridge.caller_control_id).toBe("fixture-caller-control")
    expect(bridge.record).toBe("record-from-answer")
    expect(bridge.record_channels).toBe("dual")
    expect(bridge.record_format).toBe("mp3")
    expect(bridge.record_track).toBe("both")

    const conditionalStateRead = selectedPage.waitForResponse(
      (response) =>
        response.url() === `${portalURL}/v1/calling/state` &&
        response.request().method() === "GET" &&
        Boolean(response.request().headers()["if-none-match"]),
    )
    await selectedPage.evaluate(() =>
      document.dispatchEvent(new Event("visibilitychange")),
    )
    const conditionalStateResponse = await conditionalStateRead
    expect([200, 304]).toContain(conditionalStateResponse.status())
    expect(
      conditionalStateResponse.request().headers()["if-none-match"],
    ).toBeTruthy()

    await deliverProviderEvent(selectedPage, {
      eventType: "call.bridged",
      eventId: "callleg-browser-bridge-confirmed",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: selectedLeg.control_id,
        call_leg_id: selectedLeg.provider_leg_id,
        call_session_id: "fixture-staff-session",
        client_state: bridge.client_state,
      },
    })
    await deliverProviderEvent(selectedPage, {
      eventType: "call.bridged",
      eventId: "callleg-browser-caller-bridge-confirmed",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-caller-session",
        client_state: bridge.caller_client_state,
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{ role: string; state: string; count: string }>(
          `SELECT role, state, count(*)::text
             FROM human_calling_call_legs
            WHERE call_id = $1
            GROUP BY role, state
            ORDER BY role, state`,
          [callID],
        )
        return result.rows
      })
      .toEqual([
        { role: "CALLER", state: "BRIDGED", count: "1" },
        { role: "STAFF", state: "BRIDGED", count: "1" },
        { role: "STAFF", state: "ENDING", count: "1" },
      ])
    const committed = await database.query<{ committed_at: Date }>(
      `SELECT max(updated_at) AS committed_at
         FROM human_calling_call_legs
        WHERE call_id = $1 AND state = 'BRIDGED'`,
      [callID],
    )
    const backendCommittedAt = committed.rows[0]!.committed_at.getTime()
    await expect(
      callCenter(selectedPage).getByRole("status"),
    ).toHaveText(/^Connected \d{2}:\d{2}$/, { timeout: 1_000 })
    const browserRenderedAt = await selectedPage.evaluate(() => Date.now())
    expect(browserRenderedAt - backendCommittedAt).toBeLessThanOrEqual(1_000)
    await deliverProviderEvent(secondaryPage, {
      eventType: "call.hangup",
      eventId: "callleg-browser-secondary-loser-hangup",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: secondaryLeg.control_id,
        call_leg_id: secondaryLeg.provider_leg_id,
        call_session_id: "fixture-secondary-staff-session",
        hangup_cause: "NORMAL_CLEARING",
        hangup_source: "CALL_CONTROL",
      },
    })
    await endMediaLeg(
      secondaryPage,
      secondaryLeg.provider_leg_id,
      secondaryLeg.media_token,
    )
    await expect
      .poll(async () => {
        const result = await database.query<{
          loser_state: string
          provider_termination: string | null
        }>(
          `SELECT loser.state AS loser_state, call.provider_termination
             FROM human_calling_calls call
             JOIN human_calling_call_legs loser
               ON loser.call_id = call.id AND loser.id = $2
            WHERE call.id = $1`,
          [callID, secondaryLeg.id],
        )
        return result.rows[0]
      })
      .toEqual({ loser_state: "ENDED", provider_termination: null })
    await expect(
      callCenter(selectedPage).getByRole("status"),
    ).toHaveText(/^Connected \d{2}:\d{2}$/, { timeout: 20_000 })
    await expect(
      callCenter(selectedPage).getByRole("heading", {
        name: "CallLeg Browser Caller",
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      selectedPage.getByRole("button", { name: "Workspace selector" }),
    ).toBeDisabled()
    await expect(
      callCenter(selectedPage).getByText("waiting for exact leg", {
        exact: false,
      }),
    ).toHaveCount(0)
    await expect(
      callCenter(selectedPage).getByText("waiting for provider bridge", {
        exact: false,
      }),
    ).toHaveCount(0)
    const answersBeforeNavigation = await mediaAnswers(selectedPage)
    if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
      await tasksSection.click()
    }
    await expect(navigationTask).toBeVisible()
    await navigationTask.click()
    await expect(
      callCenter(selectedPage).getByRole("status"),
    ).toHaveText(/^Connected \d{2}:\d{2}$/)
    await expect.poll(() => mediaAnswers(selectedPage)).toBe(
      answersBeforeNavigation,
    )
    await expect(
      selectedPage.getByRole("button", { name: "End", exact: true }),
    ).toBeVisible()
    await selectedPage.reload()
    await expect(
      callCenter(selectedPage).getByRole("status"),
    ).toHaveText(/^Connected \d{2}:\d{2}$/, { timeout: 20_000 })
    await expect(
      selectedPage.getByRole("button", { name: "End", exact: true }),
    ).toBeVisible()
    await expect(
      selectedPage.getByRole("button", { name: "Mute", exact: true }),
    ).toHaveCount(0)
    await sendIncomingLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      selectedLeg.media_token,
      true,
    )
    await expect.poll(() => mediaAnswers(selectedPage)).toBe(1)
    await expect(
      selectedPage.getByRole("button", { name: "End", exact: true }),
    ).toBeVisible()
    const recordingEndedAt = new Date()
    const recordingStartedAt = new Date(recordingEndedAt.getTime() - 30_000)
    await deliverProviderEvent(selectedPage, {
      eventType: "call.recording.saved",
      eventId: "callleg-browser-recording-saved",
      occurredAt: recordingEndedAt.toISOString(),
      payload: {
        call_control_id: selectedLeg.control_id,
        call_leg_id: selectedLeg.provider_leg_id,
        call_session_id: "fixture-staff-session",
        client_state: bridge.client_state,
        recording_id: "callleg-browser-recording",
        recording_started_at: recordingStartedAt.toISOString(),
        recording_ended_at: recordingEndedAt.toISOString(),
      },
    })
    await expect(
      selectedPage.getByRole("heading", { name: "Call recording" }),
    ).toBeVisible({ timeout: 20_000 })
    await selectedPage.getByRole("button", { name: "Play" }).click()
    const callRecording = selectedPage.getByLabel("Call recording")
    await expect(callRecording).toBeVisible()
    await expect(callRecording).toHaveAttribute(
      "src",
      /\/v1\/calling\/recording-playback\//,
    )
    const contextPanel = selectedPage.getByRole("complementary", {
      name: "Call context",
    })
    await expect(contextPanel).toBeVisible()
    await expect(
      contextPanel.getByRole("heading", {
        name: /Call connected|Call ended|Call resolved|Follow-up required/,
      }),
    ).toBeVisible()
    await expect(contextPanel.getByText("Contact Context")).toHaveCount(0)
    await expect(contextPanel.getByText("Transfer reason")).toHaveCount(0)
    await expect(contextPanel.getByText("Details", { exact: true })).toBeVisible()
    await selectedPage.screenshot({
      path: testInfo.outputPath("call-context.png"),
      fullPage: true,
    })
    const contextPanelBox = await contextPanel.boundingBox()
    const viewport = selectedPage.viewportSize()
    expect(contextPanelBox).not.toBeNull()
    expect(viewport).not.toBeNull()
    expect(contextPanelBox!.height).toBeLessThan(viewport!.height * 0.75)
    await selectedPage.setViewportSize({ width: 800, height: 1200 })
    await expect
      .poll(async () => {
        const compactPanelBox = await contextPanel.boundingBox()
        return compactPanelBox?.height ?? 1200
      })
      .toBeLessThan(900)
    await expect(callCenter(secondaryPage)).toHaveCount(0)
    await expect(
      secondaryPage.getByRole("button", { name: "End", exact: true }),
    ).toHaveCount(0)

    await expect(
      secondaryPage.getByRole("switch", { name: "Availability" }),
    ).toBeChecked({ timeout: 20_000 })
    await selectedPage
      .getByRole("button", { name: "Transfer", exact: true })
      .click()
    await selectedPage
      .getByLabel("Transfer to")
      .selectOption({ label: "secondary@abita.test" })
    await selectedPage
      .getByLabel("Handoff note (optional)")
      .fill("Caller needs the secondary desk")
    await selectedPage
      .getByRole("button", { name: "Transfer", exact: true })
      .click()

    type TransferEvidence = {
      id: string
      source_leg_id: string
      target_leg_id: string
      customer_leg_id: string
      customer_role: string
      target_client_state: string
      media_token: string
    }
    const transferEvidence = await expect
      .poll(async () => {
        const result = await database.query<TransferEvidence>(
          `SELECT transfer.id::text,
                  transfer.source_staff_leg_id::text AS source_leg_id,
                  transfer.target_staff_leg_id::text AS target_leg_id,
                  transfer.customer_leg_id::text AS customer_leg_id,
                  customer.role AS customer_role,
                  command.payload->>'target_leg_client_state' AS target_client_state,
                  command.payload->'custom_headers'->0->>'value' AS media_token
             FROM human_calling_staff_transfers transfer
             JOIN human_calling_call_legs customer ON customer.id = transfer.customer_leg_id
             JOIN human_calling_provider_commands command
               ON command.id = transfer.provider_command_id
            WHERE transfer.call_id = $1 AND command.state = 'SENT'`,
          [callID],
        )
        return result.rows[0]
      }, { timeout: 20_000 })
      .toBeTruthy()
      .then(async () => {
        const result = await database.query<TransferEvidence>(
          `SELECT transfer.id::text,
                  transfer.source_staff_leg_id::text AS source_leg_id,
                  transfer.target_staff_leg_id::text AS target_leg_id,
                  transfer.customer_leg_id::text AS customer_leg_id,
                  customer.role AS customer_role,
                  command.payload->>'target_leg_client_state' AS target_client_state,
                  command.payload->'custom_headers'->0->>'value' AS media_token
             FROM human_calling_staff_transfers transfer
             JOIN human_calling_call_legs customer ON customer.id = transfer.customer_leg_id
             JOIN human_calling_provider_commands command
               ON command.id = transfer.provider_command_id
            WHERE transfer.call_id = $1`,
          [callID],
        )
        return result.rows[0]!
      })
    expect(transferEvidence.source_leg_id).toBe(selectedLeg.id)
    expect(transferEvidence.customer_role).toBe("CALLER")
    expect(transferEvidence.customer_leg_id).toBe(bridge.peer_call_leg_id)

    const transferAnswer = secondaryPage.getByRole("button", {
      name: "Answer (555) 555-0100",
      exact: true,
    })
    await expect(
      secondaryPage.getByText("From selected@abita.test"),
    ).toBeVisible({ timeout: 20_000 })
    await expect(
      secondaryPage.getByText("Caller needs the secondary desk"),
    ).toBeVisible()
    await sendIncomingLeg(
      secondaryPage,
      "fixture-transfer-target-leg",
      selectedLeg.media_token,
    )
    await expect(transferAnswer).toBeDisabled()
    await endMediaLeg(
      secondaryPage,
      "fixture-transfer-target-leg",
      selectedLeg.media_token,
    )
    await deliverProviderEvent(secondaryPage, {
      eventType: "call.initiated",
      eventId: "callleg-browser-transfer-target-initiated",
      occurredAt: new Date().toISOString(),
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: "fixture-transfer-target-control",
        call_leg_id: "fixture-transfer-target-leg",
        call_session_id: "fixture-transfer-target-session",
        client_state: transferEvidence.target_client_state,
      },
    })
    await sendIncomingLeg(
      secondaryPage,
      "fixture-transfer-target-leg",
      transferEvidence.media_token,
    )
    await expect(transferAnswer).toBeEnabled()
    await transferAnswer.click()
    await deliverProviderEvent(secondaryPage, {
      eventType: "call.answered",
      eventId: "callleg-browser-transfer-target-answered",
      occurredAt: new Date().toISOString(),
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: "fixture-transfer-target-control",
        call_leg_id: "fixture-transfer-target-leg",
        call_session_id: "fixture-transfer-target-session",
        client_state: transferEvidence.target_client_state,
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{
          transfer: string
          source: string
          target: string
        }>(
          `SELECT transfer.state AS transfer, source.state AS source, target.state AS target
             FROM human_calling_staff_transfers transfer
             JOIN human_calling_call_legs source ON source.id = transfer.source_staff_leg_id
             JOIN human_calling_call_legs target ON target.id = transfer.target_staff_leg_id
            WHERE transfer.id = $1`,
          [transferEvidence.id],
        )
        return result.rows[0]
      })
      .toEqual({ transfer: "ACCEPTED", source: "BRIDGED", target: "ANSWERED" })
    await expect(
      selectedPage.getByRole("button", { name: "End", exact: true }),
    ).toBeVisible()
    await expect(
      secondaryPage.getByRole("button", { name: "End", exact: true }),
    ).toHaveCount(0)
    await deliverProviderEvent(secondaryPage, {
      eventType: "call.bridged",
      eventId: "callleg-browser-transfer-target-bridged",
      occurredAt: new Date().toISOString(),
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: "fixture-transfer-target-control",
        call_leg_id: "fixture-transfer-target-leg",
        call_session_id: "fixture-transfer-target-session",
        client_state: transferEvidence.target_client_state,
      },
    })
    await expect(
      callCenter(secondaryPage).getByRole("status"),
    ).toHaveText(/^Connected \d{2}:\d{2}$/, { timeout: 20_000 })
    await expect(
      secondaryPage.getByRole("button", { name: "End", exact: true }),
    ).toBeVisible()
    await expect(callCenter(selectedPage)).toHaveCount(0)
    await expect
      .poll(async () => {
        const result = await database.query<{
          current_owners: string
          recordings: string
          recording_id: string | null
        }>(
          `SELECT
              (SELECT count(*)::text FROM human_calling_call_legs
                WHERE call_id = $1 AND role = 'STAFF' AND state = 'BRIDGED') AS current_owners,
              (SELECT count(*)::text FROM human_calling_call_recordings
                WHERE call_id = $1) AS recordings,
              (SELECT provider_recording_id FROM human_calling_call_recordings
                WHERE call_id = $1 LIMIT 1) AS recording_id`,
          [callID],
        )
        return result.rows[0]
      })
      .toEqual({
        current_owners: "1",
        recordings: "1",
        recording_id: "callleg-browser-recording",
      })
    await deliverProviderEvent(selectedPage, {
      eventType: "call.hangup",
      eventId: "callleg-browser-old-source-delayed-hangup",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: selectedLeg.control_id,
        call_leg_id: selectedLeg.provider_leg_id,
        call_session_id: "fixture-staff-session",
        hangup_cause: "NORMAL_CLEARING",
        hangup_source: "CALL_CONTROL",
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{ terminal_outcome: string | null }>(
          `SELECT terminal_outcome FROM human_calling_calls WHERE id = $1`,
          [callID],
        )
        return result.rows[0]?.terminal_outcome ?? null
      })
      .toBeNull()

    await deliverProviderEvent(secondaryPage, {
      eventType: "call.hangup",
      eventId: "callleg-browser-remote-hangup",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-transfer-target-control",
        call_leg_id: "fixture-transfer-target-leg",
        call_session_id: "fixture-transfer-target-session",
        hangup_cause: "NORMAL_CLEARING",
        hangup_source: "STAFF",
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{ terminal_outcome: string | null }>(
          `SELECT terminal_outcome
             FROM human_calling_calls
            WHERE id = $1`,
          [callID],
        )
        return result.rows[0]?.terminal_outcome ?? ""
      })
      .toBe("ENDED")
    const hangupCommit = await database.query<{ updated_at: Date }>(
      `SELECT updated_at FROM human_calling_calls WHERE id = $1`,
      [callID],
    )
    const outcome = callCenter(secondaryPage)
    await expect(outcome.getByRole("status")).toHaveText("Outcome", {
      timeout: 1_000,
    })
    const hangupRenderedAt = await selectedPage.evaluate(() => Date.now())
    expect(
      hangupRenderedAt - hangupCommit.rows[0]!.updated_at.getTime(),
    ).toBeLessThanOrEqual(1_000)
    await expect(
      selectedPage.getByText("End was not committed", { exact: false }),
    ).toHaveCount(0)
    await expect(
      selectedPage.getByText("Calling ownership or the Call state changed", {
        exact: false,
      }),
    ).toHaveCount(0)
    await outcome.getByRole("button", { name: "Resolved", exact: true }).click()
    await expect(outcome).toHaveCount(0)
    if ((await tasksSection.getAttribute("aria-expanded")) === "false") {
      await tasksSection.click()
    }
    await expect(navigationTask).toBeVisible()
    await navigationTask.click()
    const navigationContext = selectedPage.getByRole("complementary", {
      name: "Task context",
    })
    await expect(
      navigationContext.getByRole("heading", {
        name: navigationTaskTitle,
        exact: true,
      }),
    ).toBeVisible()
    await navigationContext
      .getByRole("button", { name: "Complete", exact: true })
      .click()
    await expect(navigationTask).toHaveCount(0)
  } finally {
    await database.end()
    await secondaryContext.close()
    await sameUserContext.close().catch(() => undefined)
  }
})

test("voicemail and meaningful missed calls refresh into their recovery folders", async ({
  page,
}, testInfo) => {
  test.setTimeout(180_000)
  test.skip(
    !provisioningOutput || !databaseURL,
    "E2E_PROVISIONING_OUTPUT and E2E_DATABASE_URL are required",
  )
  await prepareBrowser(page.context())
  const database = new Pool({ connectionString: databaseURL })

  try {
    await signInAs(page, "messaging@abita.test", "Fixture Messaging Staff")
    await expect(page.getByTestId("mounted-workspace")).toBeVisible()
    await expect(
      page.getByRole("switch", { name: "Availability" }),
    ).toBeChecked({ timeout: 40_000 })
    const scope = await database.query<{ practice_id: string; location_id: string }>(
      `SELECT practice.id::text AS practice_id, location.id::text AS location_id
         FROM access_practices practice
         JOIN access_locations location ON location.practice_id = practice.id
        WHERE practice.provisioning_key = 'abita-eye-group'
          AND location.provisioning_key = 'fixture-location-1'`,
    )
    const practiceID = scope.rows[0]!.practice_id
    const locationID = scope.rows[0]!.location_id
    const voicemailPhone = "+15555550111"

    for (const attempt of ["first", "second"] as const) {
      const caller = await startAnsweredInboundCall(
        page,
        database,
        practiceID,
        locationID,
        voicemailPhone,
        `voicemail-${attempt}`,
        "Voicemail caller",
      )
      const ring = await readSentVoiceCommand(
        database,
        caller.callID,
        "START_RING_WINDOW",
      )
      await deliverProviderEvent(page, {
        eventType: "call.playback.ended",
        eventId: `voicemail-${attempt}-ring-ended`,
        occurredAt: new Date().toISOString(),
        payload: {
          call_control_id: caller.controlID,
          call_leg_id: caller.providerLegID,
          call_session_id: caller.sessionID,
          client_state: ring.client_state,
          status: "completed",
        },
      })
      const speak = await readSentVoiceCommand(
        database,
        caller.callID,
        "SPEAK_VOICEMAIL",
      )
      const activeCall = callCenter(page)
      await expect(activeCall).toHaveCount(0, { timeout: 30_000 })
      await expect(callCenter(page)).toHaveCount(0, { timeout: 30_000 })
      await expect(
        page.getByRole("switch", { name: "Availability" }),
      ).toBeChecked()

      if (attempt === "first") {
        await startAndEndOutboundWhileVoicemail(
          page,
          database,
          "+15555550113",
        )
      }

      await deliverProviderEvent(page, {
        eventType: "call.speak.ended",
        eventId: `voicemail-${attempt}-speak-ended`,
        occurredAt: new Date().toISOString(),
        payload: {
          call_control_id: caller.controlID,
          call_leg_id: caller.providerLegID,
          call_session_id: caller.sessionID,
          client_state: speak.client_state,
          status: "completed",
        },
      })
      const recording = await readSentVoiceCommand(
        database,
        caller.callID,
        "START_VOICEMAIL_RECORDING",
      )
      await expect(activeCall).toHaveCount(0, { timeout: 30_000 })
      await expect(
        page.getByRole("switch", { name: "Availability" }),
      ).toBeChecked()

      const recordingStartedAt = recording.sent_at
      const recordingEndedAt = new Date()
      const savedEventID = `voicemail-${attempt}-recording-saved`
      const savedEvent = {
        eventType: "call.recording.saved",
        eventId: savedEventID,
        occurredAt: recordingEndedAt.toISOString(),
        payload: {
          call_control_id: caller.controlID,
          call_leg_id: caller.providerLegID,
          call_session_id: caller.sessionID,
          client_state: recording.client_state,
          recording_id: `voicemail-${attempt}-recording`,
          recording_started_at: recordingStartedAt.toISOString(),
          recording_ended_at: recordingEndedAt.toISOString(),
        },
      }
      await deliverProviderEvent(page, savedEvent)
      await deliverProviderEvent(page, savedEvent)
      await expect(activeCall).toHaveCount(0, { timeout: 30_000 })

      const recoveryFolder = page.getByRole("button", {
        name: /^Missed Calls \d+$/,
      })
      if ((await recoveryFolder.getAttribute("aria-expanded")) === "false") {
        await recoveryFolder.click()
      }
      await expect(
        page.getByRole("button", {
          name: /\(555\) 555-0111.*Voicemail/,
        }),
      ).toBeVisible({ timeout: 30_000 })

      await expect
        .poll(async () => {
          const result = await database.query<{ duplicate_count: number }>(
            `SELECT duplicate_count
               FROM human_calling_provider_receipts
              WHERE event_id = $1`,
            [savedEventID],
          )
          return result.rows[0]?.duplicate_count ?? 0
        })
        .toBe(1)
      if (attempt === "second") {
        const availability = page.getByRole("switch", { name: "Availability" })
        if (!(await availability.isChecked())) await availability.click()
        await expect(availability).toBeChecked()
        await page.waitForTimeout(2_500)
        await expect(activeCall).toHaveCount(0)
      }
    }

    const voicemailTask = await database.query<{
      id: string
      version: string
      interactions: string
      activities: string[]
    }>(
      `SELECT task.id::text,
              task.version::text,
              count(DISTINCT interaction.call_id)::text AS interactions,
              array_agg(
                DISTINCT activity.task_version::text || ':' || activity.kind
                ORDER BY activity.task_version::text || ':' || activity.kind
              ) AS activities
         FROM work_tasks task
         JOIN work_task_interactions interaction ON interaction.task_id = task.id
         JOIN work_task_activities activity ON activity.task_id = task.id
        WHERE task.phone = $1 AND task.origin = 'VOICEMAIL_RECOVERY'
        GROUP BY task.id`,
      [voicemailPhone],
    )
    expect(voicemailTask.rows).toHaveLength(1)
    expect(voicemailTask.rows[0]).toMatchObject({
      version: "2",
      interactions: "2",
      activities: ["1:TASK_CREATED", "2:INTERACTION_ATTACHED"],
    })
    const voicemailRow = page.getByRole("button", {
      name: /\(555\) 555-0111.*Voicemail/,
    })
    await voicemailRow.click()
    const taskContext = page.getByRole("complementary", {
      name: "Task context",
    })
    await expect(taskContext).toBeVisible()
    await expect(
      taskContext.getByRole("heading", { name: "Review voicemail" }),
    ).toBeVisible()
    await expect(taskContext.getByText("1 earlier call")).toBeVisible()
    await taskContext.getByRole("button", { name: "Play" }).click()
    await expect(
      taskContext.getByLabel("Voicemail recording"),
    ).toBeVisible()
    await taskContext
      .getByRole("button", { name: "Resolve", exact: true })
      .click()
    await expect(voicemailRow).toHaveCount(0)

    const missedPhone = "+15555550112"
    const missedCaller = await startAnsweredInboundCall(
      page,
      database,
      practiceID,
      locationID,
      missedPhone,
      "meaningful-missed",
    )
    const missedRing = await readSentVoiceCommand(
      database,
      missedCaller.callID,
      "START_RING_WINDOW",
    )
    await deliverProviderEvent(page, {
      eventType: "call.playback.ended",
      eventId: "meaningful-missed-ring-ended",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: missedCaller.controlID,
        call_leg_id: missedCaller.providerLegID,
        call_session_id: missedCaller.sessionID,
        client_state: missedRing.client_state,
        status: "completed",
      },
    })
    await readSentVoiceCommand(database, missedCaller.callID, "SPEAK_VOICEMAIL")
    const missedHangup = {
      eventType: "call.hangup",
      eventId: "meaningful-missed-hangup",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: missedCaller.controlID,
        call_leg_id: missedCaller.providerLegID,
        call_session_id: missedCaller.sessionID,
        hangup_cause: "NORMAL_CLEARING",
        hangup_source: "CALLER",
      },
    }
    await deliverProviderEvent(page, missedHangup)
    await deliverProviderEvent(page, missedHangup)
    const recoveryFolder = page.getByRole("button", {
      name: /^Missed Calls \d+$/,
    })
    if ((await recoveryFolder.getAttribute("aria-expanded")) === "false") {
      await recoveryFolder.click()
    }
    await expect(
      page.getByRole("button", {
        name: /\(555\) 555-0112.*Missed call/,
      }),
    ).toBeVisible({ timeout: 30_000 })
    await page.getByLabel("Search tasks, names, or phone").fill(missedPhone)
    await page.getByLabel("Search tasks, names, or phone").press("Enter")
    await page.getByRole("button", { name: /View call:/ }).last().click()
    const missedCallContext = page.getByRole("complementary", {
      name: "Call context",
    })
    await expect(
      missedCallContext.getByRole("heading", { name: "Missed call" }),
    ).toBeVisible()
    await expect(missedCallContext).toHaveCSS("width", "288px")
    await expect(missedCallContext.getByText("Live call")).toHaveCount(0)
    await expect(missedCallContext.getByText("Contact Context")).toHaveCount(0)
    await page.screenshot({
      path: testInfo.outputPath("missed-call-context.png"),
      fullPage: true,
    })
    const missedTask = await database.query<{ id: string; version: string }>(
      `SELECT id::text, version::text
         FROM work_tasks
        WHERE phone = $1
          AND state = 'OPEN'
          AND origin = 'MISSED_CALL_RECOVERY'`,
      [missedPhone],
    )
    expect(missedTask.rows).toHaveLength(1)

    const tokenResponse = await page.request.get(`${webURL}/api/auth/token`)
    expect(tokenResponse.ok()).toBeTruthy()
    const { token } = (await tokenResponse.json()) as { token: string }
    for (const task of [missedTask.rows[0]!]) {
      const completed = await page.request.post(
        `${portalURL}/v1/tasks/${task.id}/complete`,
        {
          headers: { authorization: `Bearer ${token}` },
          data: { expectedVersion: Number(task.version) },
        },
      )
      expect(completed.status()).toBe(200)
    }
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM work_tasks
            WHERE phone = ANY($1::text[])
              AND state = 'OPEN'`,
          [[voicemailPhone, missedPhone]],
        )
        return Number(result.rows[0]?.count ?? 0)
      })
      .toBe(0)
  } finally {
    await database.end()
  }
})

async function startAndEndOutboundWhileVoicemail(
  page: Page,
  database: Pool,
  destination: string,
) {
  await page.getByLabel("Search tasks, names, or phone").fill(destination)
  await page.getByLabel("Search tasks, names, or phone").press("Enter")
  const callButton = page.getByRole("button", { name: "Call", exact: true })
  await expect(callButton).toBeEnabled()
  const [commitResponse] = await Promise.all([
    page.waitForResponse((response) =>
      response.url() === `${portalURL}/v1/calling/outbound-calls` &&
      response.request().method() === "POST",
    ),
    callButton.click(),
  ])
  expect(commitResponse.status()).toBe(201)

  await expect
    .poll(async () => {
      const result = await database.query<{ count: string }>(
        `SELECT count(*)::text
           FROM human_calling_calls
          WHERE direction = 'OUTBOUND' AND destination_phone = $1`,
        [destination],
      )
      return Number(result.rows[0]?.count ?? 0)
    })
    .toBe(1)
  const outbound = await database.query<{ id: string }>(
    `SELECT id::text
       FROM human_calling_calls
      WHERE direction = 'OUTBOUND' AND destination_phone = $1`,
    [destination],
  )
  const outboundCallID = outbound.rows[0]!.id
  const callURL = `${portalURL}/v1/calling/calls/${outboundCallID}`
  const hangupURL = `${callURL}/hangup`
  const initialCall = callCenter(page)
  await expect(initialCall.getByRole("status")).toHaveText("Calling…")
  const outboundIdentity = await initialCall.getByRole("heading").textContent()
  expect(outboundIdentity).toBeTruthy()
  const initialEnd = page.getByRole("button", { name: "End", exact: true })
  await expect(initialEnd).toBeVisible()
  await expect(initialEnd).toHaveClass(/rounded-full/)
  const stableEndPosition =
    (await initialEnd.locator("..").getAttribute("data-control-slot")) ?? ""
  expect(stableEndPosition).toBe("end")

  const staffLeg = await readOutboundLeg(
    database,
    outboundCallID,
    "STAFF",
    "DIAL_OUTBOUND_STAFF",
  )
  await expect
    .poll(async () => {
      const result = await database.query<{ state: string }>(
        `SELECT state
           FROM human_calling_call_legs
          WHERE call_id = $1 AND role = 'STAFF'`,
        [outboundCallID],
      )
      return result.rows[0]?.state ?? ""
    })
    .toMatch(/^(PENDING|DIALING|RINGING)$/)

  await page.reload()
  const restoredCall = callCenter(page)
  await expect(restoredCall.getByRole("status")).toHaveText("Calling…")
  await expect(restoredCall.getByRole("heading")).toHaveText(outboundIdentity!)
  await expect(
    restoredCall.getByRole("button", { name: /^Answer/ }),
  ).toHaveCount(0)
  const restoredEnd = restoredCall.getByRole("button", {
    name: "End",
    exact: true,
  })
  await expect(restoredEnd).toBeEnabled()
  await expect(restoredEnd.locator("..")).toHaveAttribute(
    "data-control-slot",
    stableEndPosition,
  )

  const staffSessionID = staffLeg.session_id || "voicemail-concurrent-staff-session"
  await deliverProviderEvent(page, {
    eventType: "call.initiated",
    eventId: "voicemail-concurrent-outbound-initiated",
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: staffLeg.control_id,
      call_leg_id: staffLeg.leg_id,
      call_session_id: staffSessionID,
      client_state: staffLeg.client_state,
    },
  })
  await deliverProviderEvent(page, {
    eventType: "call.answered",
    eventId: "voicemail-concurrent-outbound-staff-answered",
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: staffLeg.control_id,
      call_leg_id: staffLeg.leg_id,
      call_session_id: staffSessionID,
      client_state: staffLeg.client_state,
    },
  })
  await expect
    .poll(async () => {
      const result = await database.query<{ state: string }>(
        `SELECT state
           FROM human_calling_call_legs
          WHERE call_id = $1 AND role = 'STAFF'`,
        [outboundCallID],
      )
      return result.rows[0]?.state ?? ""
    })
    .toBe("BRIDGE_PENDING")
  const answersBeforeOutboundMedia = await mediaAnswers(page)
  await sendIncomingLeg(page, staffLeg.leg_id, staffLeg.media_token)
  await expect.poll(() => mediaAnswers(page)).toBe(answersBeforeOutboundMedia + 1)

  const destinationLeg = await readOutboundLeg(
    database,
    outboundCallID,
    "DESTINATION",
    "DIAL_OUTBOUND_DESTINATION",
  )
  const destinationSessionID =
    destinationLeg.session_id || "voicemail-concurrent-destination-session"
  for (const eventType of ["call.initiated", "call.answered"] as const) {
    await deliverProviderEvent(page, {
      eventType,
      eventId: `voicemail-concurrent-destination-${eventType.split(".")[1]}`,
      occurredAt: new Date().toISOString(),
      payload: {
        connection_id: "fixture-call-control",
        call_control_id: destinationLeg.control_id,
        call_leg_id: destinationLeg.leg_id,
        call_session_id: destinationSessionID,
        client_state: destinationLeg.client_state,
      },
    })
  }
  const bridge = await readBridgeCommand(database, outboundCallID)
  await deliverProviderEvent(page, {
    eventType: "call.bridged",
    eventId: "voicemail-concurrent-destination-bridged",
    occurredAt: new Date().toISOString(),
    payload: {
      call_control_id: destinationLeg.control_id,
      call_leg_id: destinationLeg.leg_id,
      call_session_id: destinationSessionID,
      client_state: bridge.client_state,
    },
  })
  await deliverProviderEvent(page, {
    eventType: "call.bridged",
    eventId: "voicemail-concurrent-staff-bridged",
    occurredAt: new Date().toISOString(),
    payload: {
      call_control_id: staffLeg.control_id,
      call_leg_id: staffLeg.leg_id,
      call_session_id: staffSessionID,
    },
  })
  await expect
    .poll(async () => {
      const result = await database.query<{ count: string }>(
        `SELECT count(*)::text
           FROM human_calling_call_legs
          WHERE call_id = $1 AND role IN ('STAFF', 'DESTINATION')
            AND state = 'BRIDGED'`,
        [outboundCallID],
      )
      return Number(result.rows[0]?.count ?? 0)
    })
    .toBe(2)
  const connectedCommit = await database.query<{ updated_at: Date }>(
    `SELECT max(updated_at) AS updated_at
       FROM human_calling_call_legs
      WHERE call_id = $1 AND state = 'BRIDGED'`,
    [outboundCallID],
  )
  await expect(callCenter(page).getByRole("status")).toHaveText(
    /^Connected \d{2}:\d{2}$/,
    { timeout: 1_000 },
  )
  const connectedRenderedAt = await page.evaluate(() => Date.now())
  expect(
    connectedRenderedAt - connectedCommit.rows[0]!.updated_at.getTime(),
  ).toBeLessThanOrEqual(1_000)
  const connectedEnd = page.getByRole("button", { name: "End", exact: true })
  expect(
    (await connectedEnd.locator("..").getAttribute("data-control-slot")) ?? "",
  ).toBe(stableEndPosition)

  let hangupResponseLost = false
  let hangupRefreshes = 0
  const observeHangupRefresh = (request: Request) => {
    if (
      hangupResponseLost &&
      request.url() === callURL &&
      request.method() === "GET"
    ) {
      hangupRefreshes += 1
    }
  }
  page.on("request", observeHangupRefresh)
  await page.route(hangupURL, async (route) => {
    const committed = await route.fetch()
    expect(committed.status()).toBe(202)
    hangupResponseLost = true
    await route.abort("failed")
  })
  await connectedEnd.click()
  await expect.poll(() => hangupRefreshes, { timeout: 10_000 }).toBeGreaterThan(0)
  const endingButton = page.getByRole("button", { name: "Ending", exact: true })
  await expect(endingButton).toBeVisible()
  await expect(endingButton).toBeDisabled()
  await expect(
    endingButton.locator("xpath=following-sibling::span"),
  ).toHaveText("Ending…")
  await expect(
    page.getByText("End was not committed", { exact: false }),
  ).toHaveCount(0)
  await page.unroute(hangupURL)
  page.off("request", observeHangupRefresh)

  await deliverProviderEvent(page, {
    eventType: "call.hangup",
    eventId: "voicemail-concurrent-outbound-hangup",
    occurredAt: new Date().toISOString(),
    payload: {
      call_control_id: staffLeg.control_id,
      call_leg_id: staffLeg.leg_id,
      call_session_id: staffSessionID,
      client_state: staffLeg.client_state,
      hangup_cause: "NORMAL_CLEARING",
      hangup_source: "STAFF",
    },
  })
  await endMediaLeg(page, staffLeg.leg_id, staffLeg.media_token)
  await expect
    .poll(async () => {
      const result = await database.query<{ terminal_outcome: string | null }>(
        `SELECT terminal_outcome FROM human_calling_calls WHERE id = $1`,
        [outboundCallID],
      )
      return result.rows[0]?.terminal_outcome ?? ""
    })
    .not.toBe("")
  const remoteCommit = await database.query<{ updated_at: Date }>(
    `SELECT updated_at FROM human_calling_calls WHERE id = $1`,
    [outboundCallID],
  )
  await expect(callCenter(page).getByRole("status")).toHaveText(
    "Outcome",
    { timeout: 5_000 },
  )
  const remoteRenderedAt = await page.evaluate(() => Date.now())
  expect(
    remoteRenderedAt - remoteCommit.rows[0]!.updated_at.getTime(),
  ).toBeLessThanOrEqual(1_000)
  await callCenter(page)
    .getByRole("button", { name: "Resolved", exact: true })
    .click()
  await expect(callCenter(page)).toHaveCount(0)
  await expect(
    page.getByRole("switch", { name: "Availability" }),
  ).toBeChecked()
}

async function readOutboundLeg(
  database: Pool,
  callID: string,
  role: "STAFF" | "DESTINATION",
  action: "DIAL_OUTBOUND_STAFF" | "DIAL_OUTBOUND_DESTINATION",
) {
  await expect
    .poll(async () => {
      const result = await database.query<{ ready: boolean }>(
        `SELECT leg.provider_call_control_id IS NOT NULL
                AND leg.provider_call_leg_id IS NOT NULL
                AND command.state IN ('SENT', 'RECONCILED') AS ready
           FROM human_calling_call_legs leg
           JOIN human_calling_provider_commands command
             ON command.call_leg_id = leg.id AND command.action = $3
          WHERE leg.call_id = $1 AND leg.role = $2`,
        [callID, role, action],
      )
      return result.rows[0]?.ready ?? false
    }, { timeout: 30_000 })
    .toBe(true)
  const result = await database.query<{
    control_id: string
    leg_id: string
    session_id: string
    client_state: string
    media_token: string
  }>(
    `SELECT leg.provider_call_control_id AS control_id,
            leg.provider_call_leg_id AS leg_id,
            COALESCE(leg.provider_call_session_id, '') AS session_id,
            command.payload->>'client_state' AS client_state,
            COALESCE(command.payload->'custom_headers'->0->>'value', '') AS media_token
       FROM human_calling_call_legs leg
       JOIN human_calling_provider_commands command
         ON command.call_leg_id = leg.id AND command.action = $3
      WHERE leg.call_id = $1 AND leg.role = $2`,
    [callID, role, action],
  )
  return result.rows[0]!
}

type StaffLeg = {
  id: string
  email: string
  control_id: string
  provider_leg_id: string
  client_state: string
  media_token: string
  bridge_on_answer: boolean
}

type InboundCaller = {
  callID: string
  controlID: string
  providerLegID: string
  sessionID: string
}

async function startAnsweredInboundCall(
  page: Page,
  database: Pool,
  practiceID: string,
  locationID: string,
  phone: string,
  prefix: string,
  displayName = `${prefix} caller`,
): Promise<InboundCaller> {
  const handoffResponse = await page.request.post(`${portalURL}/v1/handoffs`, {
    headers: { authorization: "Bearer synthetic-production-token" },
    data: {
      practiceId: practiceID,
      locationId: locationID,
      sourceCallId: `${prefix}-source`,
      idempotencyKey: `${prefix}-handoff`,
      contact: {
        phone,
        phoneSource: "Abita",
        displayName,
        nameSource: "Abita",
        transferReason: "Needs a staff response",
        reasonSource: "Abita AI",
      },
    },
  })
  expect(handoffResponse.status()).toBe(201)
  const caller = {
    callID: "",
    controlID: `${prefix}-caller-control`,
    providerLegID: `${prefix}-caller-leg`,
    sessionID: `${prefix}-caller-session`,
  }
  await deliverProviderEvent(page, {
    eventType: "call.initiated",
    eventId: `${prefix}-caller-initiated`,
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: caller.controlID,
      call_leg_id: caller.providerLegID,
      call_session_id: caller.sessionID,
      from: phone,
      to: "+17275550101",
    },
  })
  caller.callID = await expect
    .poll(async () => {
      const result = await database.query<{ id: string }>(
        `SELECT call_id::text AS id
           FROM human_calling_call_legs
          WHERE provider_call_leg_id = $1`,
        [caller.providerLegID],
      )
      return result.rows[0]?.id ?? ""
    })
    .not.toBe("")
    .then(async () => {
      const result = await database.query<{ id: string }>(
        `SELECT call_id::text AS id
           FROM human_calling_call_legs
          WHERE provider_call_leg_id = $1`,
        [caller.providerLegID],
      )
      return result.rows[0]!.id
    })
  await deliverProviderEvent(page, {
    eventType: "call.answered",
    eventId: `${prefix}-caller-answered`,
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: caller.controlID,
      call_leg_id: caller.providerLegID,
      call_session_id: caller.sessionID,
    },
  })
  await readSentVoiceCommand(database, caller.callID, "START_RING_WINDOW")
  return caller
}

async function readSentVoiceCommand(
  database: Pool,
  callID: string,
  action: string,
) {
  await expect
    .poll(async () => {
      const result = await database.query<{ state: string }>(
        `SELECT state
           FROM human_calling_provider_commands
          WHERE call_id = $1 AND action = $2
          ORDER BY created_at DESC, id DESC
          LIMIT 1`,
        [callID, action],
      )
      return result.rows[0]?.state ?? ""
    }, { timeout: 30_000 })
    .toMatch(/^(SENT|RECONCILED)$/)
  const result = await database.query<{
    client_state: string
    sent_at: Date
  }>(
    `SELECT payload->>'client_state' AS client_state, sent_at
       FROM human_calling_provider_commands
      WHERE call_id = $1 AND action = $2
      ORDER BY created_at DESC, id DESC
      LIMIT 1`,
    [callID, action],
  )
  return result.rows[0]!
}

async function readStaffLegs(database: Pool, callID: string) {
  await expect
    .poll(async () => {
      const result = await database.query<{ count: string }>(
        `SELECT count(*)::text
           FROM human_calling_call_legs leg
           JOIN human_calling_provider_commands command
             ON command.call_leg_id = leg.id AND command.action = 'DIAL_STAFF'
          WHERE leg.call_id = $1 AND leg.role = 'STAFF'
            AND leg.provider_call_control_id IS NOT NULL
            AND leg.provider_call_leg_id IS NOT NULL
            AND command.state IN ('SENT', 'RECONCILED')`,
        [callID],
      )
      return Number(result.rows[0]?.count ?? 0)
    }, { timeout: 30_000 })
    .toBe(2)
  const result = await database.query<StaffLeg>(
    `SELECT leg.id::text, membership.email, leg.provider_call_control_id AS control_id,
            leg.provider_call_leg_id AS provider_leg_id,
            command.payload->>'client_state' AS client_state,
            command.payload->'custom_headers'->0->>'value' AS media_token,
            (command.payload->>'bridge_on_answer')::boolean AS bridge_on_answer
       FROM human_calling_call_legs leg
       JOIN access_memberships membership
         ON membership.practice_id = (
           SELECT practice_id FROM human_calling_calls WHERE id = leg.call_id
         ) AND membership.user_subject = leg.staff_subject
       JOIN human_calling_provider_commands command
         ON command.call_leg_id = leg.id AND command.action = 'DIAL_STAFF'
      WHERE leg.call_id = $1 AND leg.role = 'STAFF'
      ORDER BY membership.email`,
    [callID],
  )
  return result.rows
}

async function readBridgeCommand(database: Pool, callID: string) {
  await expect
    .poll(async () => {
      const result = await database.query<{ state: string }>(
        `SELECT state FROM human_calling_provider_commands
          WHERE call_id = $1 AND action = 'BRIDGE'
          ORDER BY created_at DESC LIMIT 1`,
        [callID],
      )
      return result.rows[0]?.state ?? ""
    }, { timeout: 20_000 })
    .toMatch(/^(SENT|RECONCILED)$/)
  const result = await database.query<{
    target_id: string
    peer_call_leg_id: string
    prevent_double_bridge: boolean
    caller_control_id: string
    caller_client_state: string
    client_state: string
    record: string
    record_channels: string
    record_format: string
    record_track: string
  }>(
    `SELECT target_id, peer_call_leg_id::text,
            (payload->>'prevent_double_bridge')::boolean AS prevent_double_bridge,
            payload->>'call_control_id' AS caller_control_id,
            payload->>'client_state' AS client_state,
            payload->>'record' AS record,
            payload->>'record_channels' AS record_channels,
            payload->>'record_format' AS record_format,
            payload->>'record_track' AS record_track,
            (
              SELECT ring.payload->>'client_state'
              FROM human_calling_provider_commands ring
              WHERE ring.call_id = human_calling_provider_commands.call_id
                AND ring.action = 'START_RING_WINDOW'
              ORDER BY ring.created_at LIMIT 1
            ) AS caller_client_state
       FROM human_calling_provider_commands
      WHERE call_id = $1 AND action = 'BRIDGE'
      ORDER BY created_at DESC LIMIT 1`,
    [callID],
  )
  return result.rows[0]
}

async function prepareBrowser(context: BrowserContext) {
  await context.grantPermissions(["microphone", "notifications"], { origin: webURL })
  await context.addInitScript(() => {
    const state = {
      answers: 0,
      answerFailures: 0,
      connections: 0,
      deferredMediaToken: "",
      finishDeferredAnswer: undefined as undefined | (() => void),
      endedMediaTokens: new Set<string>(),
      incoming: undefined as
        | undefined
        | ((providerLegID: string, mediaToken: string, recovery: boolean) => void),
      end: undefined as
        | undefined
        | ((providerLegID: string, mediaToken: string) => void),
    }
    const microphone = {
      readyState: "live",
      stop: () => undefined,
      addEventListener: () => undefined,
    }
    Object.defineProperty(navigator, "mediaDevices", {
      configurable: true,
      value: {
        getUserMedia: async () => ({
          getTracks: () => [microphone],
          getAudioTracks: () => [microphone],
        }),
        enumerateDevices: async () => [{ kind: "audioinput" }],
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
      },
    })
    class FixtureAudioContext {
      currentTime = 0
      destination = {}
      resume = async () => undefined
      close = async () => undefined
      createOscillator() {
        return {
          frequency: { value: 0 },
          connect: (target: unknown) => target,
          start: () => undefined,
          stop: () => undefined,
        }
      }
      createGain() {
        return {
          gain: {
            setValueAtTime: () => undefined,
            exponentialRampToValueAtTime: () => undefined,
          },
          connect: () => this.destination,
        }
      }
    }
    Object.defineProperty(window, "AudioContext", {
      configurable: true,
      value: FixtureAudioContext,
    })
    class FixtureNotification {
      static permission = "granted"
      static requestPermission = async () => "granted"
      constructor() {}
      close() {}
    }
    Object.defineProperty(window, "Notification", {
      configurable: true,
      value: FixtureNotification,
    })
    Object.assign(window, {
      __acuityCallingTestState: state,
      __acuityCallingMediaFactory: () => ({
        connect: async (
          _token: string,
          _audio: string,
          callbacks: {
            onState: (state: string) => void
            onEnded?: (leg: {
              providerLegID: string
              mediaToken: string
            }) => void
            onIncoming: (leg: {
              providerLegID: string
              mediaToken: string
              recovery: boolean
              answer: () => Promise<"attached" | "ended">
              reject: () => Promise<void>
              mute: () => void
              unmute: () => void
              sendDTMF: () => boolean
            }) => void
          },
        ) => {
          state.connections += 1
          state.end = (providerLegID, mediaToken) => {
            state.endedMediaTokens.add(mediaToken)
            callbacks.onEnded?.({ providerLegID, mediaToken })
            state.finishDeferredAnswer?.()
            state.finishDeferredAnswer = undefined
          }
          state.incoming = (providerLegID, mediaToken, recovery) => {
            state.endedMediaTokens.delete(mediaToken)
            callbacks.onIncoming({
              providerLegID,
              mediaToken,
              recovery,
              answer: async () => {
                state.answers += 1
                if (state.answerFailures > 0) {
                  state.answerFailures -= 1
                  throw new Error("fixture media answer failed")
                }
                if (state.deferredMediaToken === mediaToken) {
                  await new Promise<void>((resolve) => {
                    state.finishDeferredAnswer = resolve
                  })
                }
                return state.endedMediaTokens.has(mediaToken)
                  ? "ended"
                  : "attached"
              },
              reject: async () => undefined,
              mute: () => undefined,
              unmute: () => undefined,
              sendDTMF: () => true,
            })
          }
          callbacks.onState("ready")
        },
        disconnect: async () => undefined,
      }),
    })
  })
}

function callCenter(page: Page) {
  return page.getByRole("region", { name: "Call controls" })
}

async function ensureCallingAvailability(page: Page) {
  const availability = page.getByRole("switch", { name: "Availability" })
  const useThisBrowser = page.getByRole("button", { name: "Use this browser" })

  await expect
    .poll(async () => {
      if (await availability.isChecked().catch(() => false)) return "available"
      if (await useThisBrowser.isVisible().catch(() => false)) return "takeover"
      return "waiting"
    }, { timeout: 40_000 })
    .not.toBe("waiting")

  if (!(await availability.isChecked().catch(() => false))) {
    await useThisBrowser.click()
  }
  await expect(availability).toBeChecked({ timeout: 40_000 })
}

async function deliverProviderEvent(
  page: Page,
  event: {
    eventType: string
    eventId: string
    occurredAt: string
    payload: Record<string, unknown>
  },
) {
  const response = await page.request.post(`${telnyxFixtureURL}/fixture/webhook`, {
    headers: { authorization: "Bearer fixture-control" },
    data: event,
  })
  expect(response.ok()).toBeTruthy()
}

async function sendIncomingLeg(
  page: Page,
  providerLegID: string,
  mediaToken: string,
  recovery = false,
) {
  await expect
    .poll(() =>
      page.evaluate(
        () =>
          typeof (
            window as typeof window & {
              __acuityCallingTestState: { incoming?: unknown }
            }
          ).__acuityCallingTestState.incoming === "function",
      ),
    )
    .toBe(true)
  await page.evaluate(
    ({ providerLegID, mediaToken, recovery }) => {
      const fixture = window as typeof window & {
        __acuityCallingTestState: {
          incoming?: (providerLegID: string, mediaToken: string, recovery: boolean) => void
        }
      }
      fixture.__acuityCallingTestState.incoming?.(providerLegID, mediaToken, recovery)
    },
    { providerLegID, mediaToken, recovery },
  )
}

async function mediaAnswers(page: Page) {
  return page.evaluate(
    () =>
      (
        window as typeof window & {
          __acuityCallingTestState: { answers: number }
        }
      ).__acuityCallingTestState.answers,
  )
}

async function mediaConnections(page: Page) {
  return page.evaluate(
    () =>
      (
        window as typeof window & {
          __acuityCallingTestState: { connections: number }
        }
      ).__acuityCallingTestState.connections,
  )
}

async function failNextMediaAnswer(page: Page) {
  await page.evaluate(() => {
    const fixture = window as typeof window & {
      __acuityCallingTestState: { answerFailures: number }
    }
    fixture.__acuityCallingTestState.answerFailures += 1
  })
}

async function deferMediaAnswer(page: Page, mediaToken: string) {
  await page.evaluate((token) => {
    const fixture = window as typeof window & {
      __acuityCallingTestState: { deferredMediaToken: string }
    }
    fixture.__acuityCallingTestState.deferredMediaToken = token
  }, mediaToken)
}

async function endMediaLeg(
  page: Page,
  providerLegID: string,
  mediaToken: string,
) {
  await page.evaluate(
    ({ providerLegID, mediaToken }) => {
      const fixture = window as typeof window & {
        __acuityCallingTestState: {
          end?: (providerLegID: string, mediaToken: string) => void
        }
      }
      fixture.__acuityCallingTestState.end?.(providerLegID, mediaToken)
    },
    { providerLegID, mediaToken },
  )
}
