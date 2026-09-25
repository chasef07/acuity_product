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

test("collapsing Task details preserves the Task in the outbound Call request", async ({ page }) => {
  test.skip(!provisioningOutput, "E2E_PROVISIONING_OUTPUT is required")
  await prepareBrowser(page.context())
  await signInAs(page, "secondary@abita.test", "Fixture Secondary Staff")
  await expect(page.getByRole("switch", { name: "Availability" })).toBeChecked({ timeout: 40_000 })
  const key = `collapsed-task-callback-${Date.now()}`
  const title = `Collapsed Task callback ${key}`
  const created = await page.request.post(`${portalURL}/v1/tasks`, {
    headers: { authorization: "Bearer synthetic-service-token" },
    data: {
      callId: key, callerPhone: "+12025550219", category: "optical",
      idempotencyKey: key, officeKey: "spring-hill", officePhone: "+17275550101",
      source: "agent", urgency: "normal", summary: title, message: "Synthetic callback request.",
    },
  })
  expect(created.ok(), await created.text()).toBeTruthy()
  const { taskId } = await created.json()
  expect(taskId).toEqual(expect.any(String))
  const search = page.getByLabel("Search tasks, names, or phone")
  await search.fill(title)
  await search.press("Enter")
  const taskRow = page.getByRole("button", { name: title, exact: true })
  await taskRow.click()
  const context = page.getByRole("complementary", { name: "Task context" })
  await expect(context).toBeVisible()
  await taskRow.click()
  await expect(context).toBeHidden()

  // Inspect the browser command without starting a provider call.
  await page.route(`${portalURL}/v1/calling/outbound-calls`, (route) =>
    route.fulfill({ status: 409, json: { message: "Synthetic outbound request inspection" } }),
  )
  const [outbound] = await Promise.all([
    page.waitForRequest((request) => request.url() === `${portalURL}/v1/calling/outbound-calls` && request.method() === "POST"),
    page.getByRole("button", { name: "Call", exact: true }).click(),
  ])
  expect(outbound.postDataJSON()).toMatchObject({ taskId })
  expect(outbound.postDataJSON()).not.toHaveProperty("destination")
  await taskRow.click()
  await context.getByRole("button", { name: "Complete & next", exact: true }).click()
  await expect(taskRow).toHaveCount(0)
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
    await page.getByRole("button", { name: /^Missed Calls & Voicemails/ }).click()
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

      await expect(
        page.getByRole("button", {
          name: /^Review voicemail/,
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
      name: /^Review voicemail/,
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
    const desktopViewport = page.viewportSize()!
    await page.setViewportSize({ width: 390, height: 844 })
    await page.getByTitle("Call this Task", { exact: true }).click({ trial: true, timeout: 10_000 })
    await page.setViewportSize(desktopViewport)
    await taskContext.getByRole("button", { name: "Play" }).click()
    await expect(
      taskContext.getByLabel("Voicemail recording"),
    ).toBeVisible()
    await expect(voicemailRow).toHaveCount(0, { timeout: 10_000 })
    await expect(taskContext.getByRole("button", { name: "Reopen", exact: true })).toBeVisible()

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

    await expect(
      page.getByRole("button", {
        name: /^Return missed call/,
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
  const initialEnd = page.getByRole("button", { name: "Cancel call", exact: true })
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
    name: "Cancel call",
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
  await deliverProviderEvent(page, {
    eventType: "call.initiated",
    eventId: "voicemail-concurrent-destination-initiated",
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: destinationLeg.control_id,
      call_leg_id: destinationLeg.leg_id,
      call_session_id: destinationSessionID,
      client_state: destinationLeg.client_state,
    },
  })
  const bridge = await readBridgeCommand(database, outboundCallID)
  expect(bridge.play_ringtone).toBe(true)
  expect(bridge.ringtone).toBe("us")
  await expect(restoredCall.getByRole("status")).toHaveText("Calling…")
  await deliverProviderEvent(page, {
    eventType: "call.answered",
    eventId: "voicemail-concurrent-destination-answered",
    occurredAt: new Date().toISOString(),
    payload: {
      connection_id: "fixture-call-control",
      call_control_id: destinationLeg.control_id,
      call_leg_id: destinationLeg.leg_id,
      call_session_id: destinationSessionID,
      client_state: destinationLeg.client_state,
    },
  })
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
    "Connected",
    { timeout: 1_000 },
  )
  const connectedRenderedAt = await page.evaluate(() => Date.now())
  expect(
    connectedRenderedAt - connectedCommit.rows[0]!.updated_at.getTime(),
  ).toBeLessThanOrEqual(1_000)
  const connectedEnd = page.getByRole("button", { name: "End call", exact: true })
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
  const endingButton = page.getByRole("button", { name: "Ending…", exact: true })
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
    "Call ended",
    { timeout: 5_000 },
  )
  const remoteRenderedAt = await page.evaluate(() => Date.now())
  expect(
    remoteRenderedAt - remoteCommit.rows[0]!.updated_at.getTime(),
  ).toBeLessThanOrEqual(1_000)
  await callCenter(page)
    .getByRole("button", { name: "Resolved on call", exact: true })
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
  // Keep the synthetic provider fact causally after the committed handoff.
  // JavaScript millisecond rounding can otherwise precede its microsecond timestamp.
  const admittedAt = await database.query<{ occurred_at: string }>(
    `SELECT to_char(clock_timestamp() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"') AS occurred_at`,
  )
  const caller = {
    callID: "",
    controlID: `${prefix}-caller-control`,
    providerLegID: `${prefix}-caller-leg`,
    sessionID: `${prefix}-caller-session`,
  }
  await deliverProviderEvent(page, {
    eventType: "call.initiated",
    eventId: `${prefix}-caller-initiated`,
    occurredAt: admittedAt.rows[0]!.occurred_at,
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
    play_ringtone: boolean | null
    ringtone: string | null
  }>(
    `SELECT target_id, peer_call_leg_id::text,
            (payload->>'prevent_double_bridge')::boolean AS prevent_double_bridge,
            payload->>'call_control_id' AS caller_control_id,
            payload->>'client_state' AS client_state,
            payload->>'record' AS record,
            payload->>'record_channels' AS record_channels,
            payload->>'record_format' AS record_format,
            payload->>'record_track' AS record_track,
            (payload->>'play_ringtone')::boolean AS play_ringtone,
            payload->>'ringtone' AS ringtone,
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
