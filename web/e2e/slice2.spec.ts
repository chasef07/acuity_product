import { expect, test, type BrowserContext, type Page } from "@playwright/test"
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
}) => {
  test.setTimeout(180_000)
  test.skip(
    !provisioningOutput || !databaseURL,
    "E2E_PROVISIONING_OUTPUT and E2E_DATABASE_URL are required",
  )
  const secondaryContext = await browser.newContext()
  await Promise.all([
    prepareBrowser(selectedPage.context()),
    prepareBrowser(secondaryContext),
  ])
  const secondaryPage = await secondaryContext.newPage()
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
      expect(
        selectedPage.getByRole("switch", { name: "Availability" }),
      ).toBeChecked({ timeout: 40_000 }),
      expect(
        secondaryPage.getByRole("switch", { name: "Availability" }),
      ).toBeChecked({ timeout: 40_000 }),
    ])

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
        offer.getByLabel(/Incoming offer countdown for/),
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
    await sendIncomingLeg(
      selectedPage,
      selectedLeg.provider_leg_id,
      selectedLeg.media_token,
    )
    await selectedPage
      .getByRole("button", { name: "Answer (555) 555-0100", exact: true })
      .click()
    await expect.poll(() => mediaAnswers(selectedPage)).toBe(1)

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
    expect(bridge.target_id).toBe(selectedLeg.control_id)
    expect(bridge.peer_call_leg_id).toBeTruthy()
    expect(bridge.prevent_double_bridge).toBe(true)
    expect(bridge.caller_control_id).toBe("fixture-caller-control")

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
    await expect(
      callCenter(selectedPage).getByText("Connected", { exact: true }),
    ).toBeVisible({ timeout: 20_000 })
    await expect(
      selectedPage.getByRole("heading", {
        name: "(555) 555-0100",
        exact: true,
      }),
    ).toBeVisible()
    const contextPanel = selectedPage.getByRole("complementary", {
      name: "Call context",
    })
    await expect(contextPanel).toBeVisible()
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
  } finally {
    await database.end()
    await secondaryContext.close()
  }
})

test("voicemail and meaningful missed calls refresh into their recovery folders", async ({
  page,
}) => {
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
      const activeCall = page.getByRole("region", {
        name: "Active call controls",
      })
      await expect(
        activeCall.getByText("Voicemail greeting", { exact: true }).first(),
      ).toBeVisible({ timeout: 30_000 })

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
      await expect(
        activeCall.getByText("Recording voicemail", { exact: true }).first(),
      ).toBeVisible({ timeout: 30_000 })

      const recordingEndedAt = new Date()
      const recordingStartedAt = new Date(recordingEndedAt.getTime() - 8_000)
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
      await expect(
        activeCall.getByText("Voicemail", { exact: true }).first(),
      ).toBeVisible({ timeout: 30_000 })

      const recoveryFolder = page.getByRole("button", {
        name: /^Missed Calls \d+$/,
      })
      if ((await recoveryFolder.getAttribute("aria-expanded")) === "false") {
        await recoveryFolder.click()
      }
      const expectedCount = attempt === "first" ? 1 : 2
      await expect(
        page.getByRole("button", {
          name: new RegExp(`\\(555\\) 555-0111.*${expectedCount} voicemail`),
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
      await activeCall.getByRole("button", { name: "Close" }).click()
      await expect(activeCall).toHaveCount(0)
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
        name: /\(555\) 555-0112.*1 missed/,
      }),
    ).toBeVisible({ timeout: 30_000 })
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
    for (const task of [voicemailTask.rows[0]!, missedTask.rows[0]!]) {
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
        displayName: `${prefix} caller`,
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
  const result = await database.query<{ client_state: string }>(
    `SELECT payload->>'client_state' AS client_state
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
  }>(
    `SELECT target_id, peer_call_leg_id::text,
            (payload->>'prevent_double_bridge')::boolean AS prevent_double_bridge,
            payload->>'call_control_id' AS caller_control_id,
            payload->>'client_state' AS client_state,
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
      incoming: undefined as
        | undefined
        | ((providerLegID: string, mediaToken: string, recovery: boolean) => void),
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
            onIncoming: (leg: {
              providerLegID: string
              mediaToken: string
              recovery: boolean
              answer: () => Promise<void>
              reject: () => Promise<void>
              mute: () => void
              unmute: () => void
              sendDTMF: () => boolean
            }) => void
          },
        ) => {
          state.incoming = (providerLegID, mediaToken, recovery) =>
            callbacks.onIncoming({
              providerLegID,
              mediaToken,
              recovery,
              answer: async () => {
                state.answers += 1
              },
              reject: async () => undefined,
              mute: () => undefined,
              unmute: () => undefined,
              sendDTMF: () => true,
            })
          callbacks.onState("ready")
        },
        disconnect: async () => undefined,
      }),
    })
  })
}

function callCenter(page: Page) {
  return page.getByRole("region", { name: /^(Incoming calls|Active call controls)$/ })
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
