import { readFile } from "node:fs/promises"

import { expect, test, type BrowserContext, type Page } from "@playwright/test"
import { Pool } from "pg"

import { latestEmail } from "./support"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"
const portalURL =
  process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"
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
  expect(audio.subarray(0, 4).toString("ascii")).toBe("RIFF")
  expect(audio.subarray(8, 12).toString("ascii")).toBe("WAVE")
})

test("Slice 2 real HTTP/PostgreSQL path elects one browser and requires provider evidence", async ({
  browser,
  page: selectedPage,
}) => {
  test.setTimeout(240_000)
  test.skip(
    !provisioningOutput || !databaseURL,
    "E2E_PROVISIONING_OUTPUT and E2E_DATABASE_URL are required",
  )
  const provisioned = JSON.parse(
    await readFile(provisioningOutput!, "utf8"),
  ) as {
    invitations: Array<{ email: string; token: string }>
  }
  const invitation = (email: string) => {
    const found = provisioned.invitations.find((item) => item.email === email)
    expect(found?.token).toBeTruthy()
    return found!.token
  }

  const selectedContext = selectedPage.context()
  const secondaryContext = await browser.newContext()
  await Promise.all([
    prepareBrowser(selectedContext),
    prepareBrowser(secondaryContext),
  ])
  const secondaryPage = await secondaryContext.newPage()
  const database = new Pool({ connectionString: databaseURL })

  try {
    await Promise.all([
      signUp(
        selectedPage,
        "selected@abita.test",
        invitation("selected@abita.test"),
        "Fixture Selected Staff",
      ),
      signUp(
        secondaryPage,
        "secondary@abita.test",
        invitation("secondary@abita.test"),
        "Fixture Secondary Staff",
      ),
    ])
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_credentials c
             JOIN auth."user" u ON u.id = c.user_subject
            WHERE c.state = 'ACTIVE'
              AND u.email IN ('selected@abita.test', 'secondary@abita.test')`,
        )
        return Number(result.rows[0]?.count ?? 0)
      }, { timeout: 40_000 })
      .toBe(2)

    await test.step("AI Tasks converge without stealing either browser's selection", async () => {
      await Promise.all([
        expect(selectedPage.getByText("No follow-up tasks")).toBeVisible(),
        expect(secondaryPage.getByText("No follow-up tasks")).toBeVisible(),
      ])
      const createStaffTask = async ({
        idempotencyKey,
        summary,
        message,
        urgency,
      }: {
        idempotencyKey: string
        summary: string
        message: string
        urgency: "high_priority" | "normal" | "non_urgent"
      }) =>
        selectedPage.request.post(`${portalURL}/v1/tasks`, {
          headers: { authorization: "Bearer synthetic-service-token" },
          data: {
            callId: "slice-4-e2e-source-call",
            callerPhone: "+17275551212",
            category: "documentation",
            idempotencyKey,
            message,
            officeKey: "spring-hill",
            officePhone: "+17275919997",
            patient: {
              id: "compatibility-only-patient-id",
              dob: "01/01/1980",
              name: "Synthetic Caller",
            },
            source: "agent",
            summary,
            urgency,
          },
        })

      const firstTitle = "Send records to specialist"
      const firstMessage =
        "Caller asked the office to send records to the named specialist."
      const firstResponse = await createStaffTask({
        idempotencyKey: "slice_4_e2e_first",
        summary: firstTitle,
        message: firstMessage,
        urgency: "normal",
      })
      expect(firstResponse.status()).toBe(201)
      const firstReceipt = (await firstResponse.json()) as {
        status: string
        taskId: string
      }
      expect(firstReceipt).toEqual({
        status: "created",
        taskId: expect.any(String),
        category: "documentation",
        urgency: "normal",
      })
      const replayResponse = await createStaffTask({
        idempotencyKey: "slice_4_e2e_first",
        summary: firstTitle,
        message: firstMessage,
        urgency: "normal",
      })
      expect(replayResponse.status()).toBe(200)
      expect(await replayResponse.json()).toEqual({
        status: "duplicate",
        taskId: firstReceipt.taskId,
        category: "documentation",
        urgency: "normal",
      })

      const firstTaskButtons = [
        selectedPage.getByRole("button", { name: new RegExp(firstTitle) }),
        secondaryPage.getByRole("button", { name: new RegExp(firstTitle) }),
      ]
      await Promise.all(firstTaskButtons.map((button) => expect(button).toBeVisible()))
      await Promise.all([
        expect(
          selectedPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).not.toBeVisible(),
        expect(
          secondaryPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).not.toBeVisible(),
      ])
      await Promise.all(firstTaskButtons.map((button) => button.click()))
      await Promise.all([
        expect(
          selectedPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).toBeVisible(),
        expect(
          secondaryPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).toBeVisible(),
      ])
      await expect(
        selectedPage.getByRole("region", { name: "AI Task source" }),
      ).toContainText(firstMessage)
      await expect(
        selectedPage.getByRole("region", { name: "AI Task source" }),
      ).toContainText("AI-supplied name: Synthetic Caller")

      const secondTitle = "Urgent document correction"
      const secondResponse = await createStaffTask({
        idempotencyKey: "slice_4_e2e_second",
        summary: secondTitle,
        message: "Caller identified an urgent correction as a separate outcome.",
        urgency: "high_priority",
      })
      expect(secondResponse.status()).toBe(201)
      await Promise.all([
        expect(
          selectedPage.getByRole("button", { name: new RegExp(secondTitle) }),
        ).toBeVisible(),
        expect(
          secondaryPage.getByRole("button", { name: new RegExp(secondTitle) }),
        ).toBeVisible(),
      ])
      await Promise.all([
        expect(
          selectedPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).toBeVisible(),
        expect(
          secondaryPage.getByRole("heading", { name: firstTitle, exact: true }),
        ).toBeVisible(),
      ])

      await secondaryPage.getByLabel("Order tasks").selectOption("priority")
      await expect(secondaryPage.getByLabel("Order tasks")).toHaveValue(
        "priority",
      )
      await expect(selectedPage.getByLabel("Order tasks")).toHaveValue("time")
      await secondaryPage.reload()
      await expect(secondaryPage.getByLabel("Order tasks")).toHaveValue(
        "priority",
      )
      await secondaryPage
        .getByRole("button", { name: new RegExp(firstTitle) })
        .click()

      await selectedPage.getByRole("button", { name: "Complete" }).click()
      await expect(
        secondaryPage.getByRole("button", { name: "Reopen" }),
      ).toBeVisible()
      await secondaryPage.getByRole("button", { name: "Reopen" }).click()
      await expect(
        selectedPage.getByRole("button", { name: "Complete" }),
      ).toBeVisible()

      const committed = await database.query<{
        origin: string
        source_message: string
        actor_kind: string
        actor_email: string | null
        raw_task: string
        creation_activities: string
      }>(
        `SELECT
           task.origin,
           task.source_message,
           task.created_by_kind AS actor_kind,
           task.created_by_email AS actor_email,
           to_jsonb(task)::text AS raw_task,
           count(activity.id)::text AS creation_activities
         FROM work_tasks task
         LEFT JOIN work_task_activities activity
           ON activity.task_id = task.id
           AND activity.kind = 'TASK_CREATED'
         WHERE task.id = $1
         GROUP BY task.id`,
        [firstReceipt.taskId],
      )
      expect(committed.rows[0]).toEqual({
        origin: "ABITA_AI",
        source_message: firstMessage,
        actor_kind: "SERVICE",
        actor_email: null,
        raw_task: expect.not.stringContaining("compatibility-only-patient-id"),
        creation_activities: "1",
      })
    })

    await Promise.all([
      enableCalling(selectedPage),
      enableCalling(secondaryPage),
      setPageHidden(selectedPage),
      setPageHidden(secondaryPage),
    ])

    const authority = await database.query<{
      practice_id: string
      location_id: string
    }>(
      `SELECT p.id::text AS practice_id, l.id::text AS location_id
         FROM access_practices p
         JOIN access_locations l ON l.practice_id = p.id
        WHERE p.provisioning_key = 'abita-eye-group'
          AND l.provisioning_key = 'fixture-location-1'`,
    )
    const scope = authority.rows[0]
    expect(scope).toBeTruthy()
    const handoffResponse = await selectedPage.request.post(
      `${portalURL}/v1/handoffs`,
      {
        headers: { authorization: "Bearer synthetic-service-token" },
        data: {
          practiceId: scope.practice_id,
          locationId: scope.location_id,
          sourceCallId: "slice-2-e2e-source",
          idempotencyKey: "slice-2-e2e-handoff",
          contact: {
            phone: "+15555550100",
            phoneSource: "Abita",
            displayName: "Synthetic Caller",
            nameSource: "Abita",
            transferReason: "Scheduling help",
            reasonSource: "Abita AI",
          },
        },
      },
    )
    expect(handoffResponse.status()).toBe(201)
    const handoff = (await handoffResponse.json()) as {
      sipDestination: string
    }
    expect(handoff.sipDestination).toBe(
      "sip:acuity-handoff@synthetic.sip.telnyx.com",
    )

    await deliverProviderEvent(selectedPage, {
      eventType: "call.initiated",
      eventId: "e2e-caller-initiated",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-call-session",
        client_state: "",
        from: "+15555550100",
        to: "+14843336938",
      },
    })
    const secondHandoffResponse = await selectedPage.request.post(
      `${portalURL}/v1/handoffs`,
      {
        headers: { authorization: "Bearer synthetic-service-token" },
        data: {
          practiceId: scope.practice_id,
          locationId: scope.location_id,
          sourceCallId: "slice-2-e2e-source-2",
          idempotencyKey: "slice-2-e2e-handoff-2",
          contact: {
            phone: "+15555550101",
            phoneSource: "Abita",
            displayName: "Second Synthetic Caller",
            nameSource: "Abita",
            transferReason: "Another scheduling request",
            reasonSource: "Abita AI",
          },
        },
      },
    )
    expect(secondHandoffResponse.status()).toBe(201)
    const secondHandoff = (await secondHandoffResponse.json()) as {
      sipDestination: string
    }
    expect(secondHandoff.sipDestination).toBe(
      "sip:acuity-handoff@synthetic.sip.telnyx.com",
    )
    await deliverProviderEvent(selectedPage, {
      eventType: "call.initiated",
      eventId: "e2e-caller-initiated-2",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-caller-control-2",
        call_leg_id: "fixture-caller-leg-2",
        call_session_id: "fixture-call-session-2",
        client_state: "",
        from: "+15555550101",
        to: "+14843336938",
      },
    })
    await Promise.all([
      expect(
        selectedPage
          .getByRole("region", { name: "Call Center" })
          .getByText("Synthetic Caller · Fixture Location 1 · Abita", {
            exact: true,
          }),
      ).toBeVisible(),
      expect(
        secondaryPage
          .getByRole("region", { name: "Call Center" })
          .getByText("Synthetic Caller · Fixture Location 1 · Abita", {
            exact: true,
          }),
      ).toBeVisible(),
      expect(selectedPage.getByTestId("calling-queue-count")).toHaveText("2"),
      expect(secondaryPage.getByTestId("calling-queue-count")).toHaveText("2"),
      expect(selectedPage.getByText("Second Synthetic Caller · Abita")).toBeVisible(),
      expect(secondaryPage.getByText("Second Synthetic Caller · Abita")).toBeVisible(),
    ])
    for (const activePage of [selectedPage, secondaryPage]) {
      await expect
        .poll(() => callingMetric(activePage, "ringtonePulses"))
        .toBeGreaterThan(0)
      const notifications = await callingNotifications(activePage)
      expect(notifications).toHaveLength(2)
      expect(notifications.every((item) =>
        item.body === "Fixture Location 1 · answer in Acuity" &&
        !item.body.includes("Synthetic") &&
        !item.body.includes("+1555"),
      )).toBeTruthy()
    }

    let releaseAcceptResponses: () => void = () => {}
    const acceptResponseGate = new Promise<void>((resolve) => {
      releaseAcceptResponses = resolve
    })
    for (const page of [selectedPage, secondaryPage]) {
      await page.route("**/calling/offers/*/accept", async (route) => {
        const response = await route.fetch()
        await acceptResponseGate
        await route.fulfill({ response })
      })
    }
    await Promise.all([
      selectedPage.getByRole("button", { name: "Accept" }).click(),
      secondaryPage.getByRole("button", { name: "Accept" }).click(),
    ])
    const durableCall = await expect
      .poll(async () => {
        const result = await database.query<{
          id: string
          claimant_subject: string
          claimant_email: string
          expected_staff_call_leg_id: string | null
          current_attempt_id: string
        }>(
          `SELECT
             c.id::text,
             c.claimant_subject,
             u.email AS claimant_email,
             c.expected_staff_call_leg_id,
             c.current_attempt_id::text
           FROM human_calling_calls c
           JOIN auth."user" u ON u.id = c.claimant_subject
          WHERE c.call_session_id = 'fixture-call-session'`,
        )
        return result.rows[0] ?? null
      })
      .not.toBeNull()
      .then(async () => {
        const result = await database.query<{
          id: string
          claimant_subject: string
          claimant_email: string
          expected_staff_call_leg_id: string | null
          current_attempt_id: string
        }>(
          `SELECT
             c.id::text,
             c.claimant_subject,
             u.email AS claimant_email,
             c.expected_staff_call_leg_id,
             c.current_attempt_id::text
           FROM human_calling_calls c
           JOIN auth."user" u ON u.id = c.claimant_subject
          WHERE c.call_session_id = 'fixture-call-session'`,
        )
        return result.rows[0]
      })
    expect(durableCall).toBeTruthy()
    const winnerPage =
      durableCall.claimant_email === "selected@abita.test"
        ? selectedPage
        : secondaryPage
    const loserPage =
      winnerPage === selectedPage ? secondaryPage : selectedPage
    const durableMediaToken = await mediaTokenForCall(database, durableCall.id)
    await sendIncomingLeg(
      winnerPage,
      "unrelated-browser-leg",
      "unrelated-media-token",
    )
    await sendIncomingLeg(
      winnerPage,
      "early-browser-leg",
      durableMediaToken,
    )
    await expect.poll(() => mediaCount(winnerPage, "answers")).toBe(1)
    await expect.poll(() => mediaCount(winnerPage, "rejects")).toBe(1)
    await sendIncomingLeg(
      winnerPage,
      "replayed-browser-leg",
      durableMediaToken,
    )
    await expect.poll(() => mediaCount(winnerPage, "answers")).toBe(1)
    await expect.poll(() => mediaCount(winnerPage, "rejects")).toBe(2)
    releaseAcceptResponses()
    await expect(
      winnerPage
        .getByRole("region", { name: "Call Center" })
        .getByText("Connecting", { exact: true }),
    ).toBeVisible()
    await expect(loserPage.getByText("Another available User claimed this Call.")).toBeVisible()
    await Promise.all([
      expect.poll(() => callingMetric(winnerPage, "ringtoneStops")).toBeGreaterThan(0),
      expect.poll(() => callingMetric(loserPage, "ringtoneStops")).toBeGreaterThan(0),
    ])
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_provider_commands
            WHERE call_id = $1 AND action = 'DIAL_STAFF'`,
          [durableCall.id],
        )
        return Number(result.rows[0]?.count ?? 0)
      })
      .toBe(1)
    await expect
      .poll(async () => {
        const result = await database.query<{ leg: string | null }>(
          `SELECT expected_staff_call_leg_id AS leg
             FROM human_calling_calls
            WHERE id = $1`,
          [durableCall.id],
        )
        return result.rows[0]?.leg ?? ""
      })
      .toBe("fixture-staff-leg")

    await sendIncomingLegs(loserPage, durableMediaToken)
    await expect.poll(() => mediaCount(winnerPage, "answers")).toBe(1)
    await expect.poll(() => mediaCount(loserPage, "answers")).toBe(0)

    const staffClientState = Buffer.from(JSON.stringify({
      v: 1,
      call: durableCall.id,
      leg: "staff",
      attempt: durableCall.current_attempt_id,
    })).toString("base64")
    const callerClientState = Buffer.from(JSON.stringify({
      v: 1,
      call: durableCall.id,
      leg: "caller",
    })).toString("base64")
    await deliverProviderEvent(winnerPage, {
      eventType: "call.initiated",
      eventId: "e2e-staff-initiated",
      occurredAt: new Date().toISOString(),
      payload: providerLegPayload(staffClientState),
    })
    await deliverProviderEvent(winnerPage, {
      eventType: "call.answered",
      eventId: "e2e-staff-answered",
      occurredAt: new Date().toISOString(),
      payload: providerLegPayload(staffClientState),
    })
    await deliverProviderEvent(winnerPage, {
      eventType: "call.bridged",
      eventId: "e2e-staff-bridged",
      occurredAt: new Date().toISOString(),
      payload: providerLegPayload(staffClientState),
    })
    await deliverProviderEvent(winnerPage, {
      eventType: "call.bridged",
      eventId: "e2e-caller-bridged",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-call-session",
        client_state: callerClientState,
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_provider_receipts
            WHERE event_id IN ('e2e-staff-bridged', 'e2e-caller-bridged')
              AND state = 'APPLIED'`,
        )
        return Number(result.rows[0]?.count ?? 0)
      })
      .toBe(2)
    await expect(
      callCenter(winnerPage).getByText("Connected", { exact: true }),
    ).toBeVisible()
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_provider_commands
            WHERE call_id = $1
              AND action = 'START_RECORDING'
              AND payload ->> 'channels' = 'dual'
              AND payload ->> 'transcription' = 'false'`,
          [durableCall.id],
        )
        return Number(result.rows[0]?.count ?? 0)
      })
      .toBe(1)
    await deliverProviderEvent(winnerPage, {
      eventType: "call.recording.saved",
      eventId: "e2e-recording-saved",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-caller-control",
        call_leg_id: "fixture-caller-leg",
        call_session_id: "fixture-call-session",
        recording_id: "fixture-recording",
        recording_urls: {
          wav: `gs://synthetic-recordings/calls/${durableCall.id}.wav`,
        },
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{
          state: string
          object_key: string
        }>(
          `SELECT state, object_key
             FROM human_calling_recordings
            WHERE call_id = $1`,
          [durableCall.id],
        )
        return result.rows[0] ?? null
      })
      .toEqual({
        state: "READY",
        object_key: `calls/${durableCall.id}.wav`,
      })

    const takeoverPage = await winnerPage.context().newPage()
    await takeoverPage.goto("/workspace")
    await expect(takeoverPage.getByRole("region", { name: "Call Center" })).toBeVisible()
    await takeoverPage.getByRole("button", { name: "Enable calling" }).click()
    await takeoverPage.getByRole("button", { name: "Take over softphone" }).click()
    await expect(
      callCenter(takeoverPage).getByText("Connected", { exact: true }),
    ).toBeVisible()
    await expect(
      callCenter(winnerPage).getByText("Inactive in this browser"),
    ).toBeVisible()
    await expect.poll(() => mediaCount(winnerPage, "disconnects")).toBeGreaterThan(0)
    await expect(
      callCenter(takeoverPage).getByText(/Audio: waiting for exact leg/),
    ).toBeVisible()
    await sendIncomingLegs(takeoverPage, durableMediaToken)
    await expect.poll(() => mediaCount(takeoverPage, "answers")).toBe(1)
    await expect(callCenter(takeoverPage).getByText(/Audio: attached/)).toBeVisible()
    await signalMedia(takeoverPage, "reconnecting")
    await expect(
      callCenter(takeoverPage).getByText("Reconnecting", { exact: true }),
    ).toBeVisible()
    await signalMedia(takeoverPage, "ready")
    await expect(
      callCenter(takeoverPage).getByText("Connected", { exact: true }),
    ).toBeVisible()
    await sendIncomingLeg(
      takeoverPage,
      "fixture-browser-leg",
      durableMediaToken,
      true,
    )
    await expect.poll(() => mediaCount(takeoverPage, "answers")).toBe(2)

    await takeoverPage.reload()
    await expect(callCenter(takeoverPage).getByText("Connected", { exact: true })).toBeVisible({
      timeout: 15_000,
    })
    await expect(callCenter(takeoverPage).getByText(/Audio: waiting for exact leg/)).toBeVisible({
      timeout: 15_000,
    })
    await sendIncomingLegs(takeoverPage, durableMediaToken)
    await expect.poll(() => mediaCount(takeoverPage, "answers")).toBe(1)
    await expect(callCenter(takeoverPage).getByText(/Audio: attached/)).toBeVisible()

    await takeoverPage.getByRole("button", { name: "Hang up" }).click()
    await expect
      .poll(async () => {
        const result = await database.query<{ count: string }>(
          `SELECT count(*)::text
             FROM human_calling_provider_commands
            WHERE call_id = $1 AND action = 'HANGUP'`,
          [durableCall.id],
        )
        return Number(result.rows[0]?.count ?? 0)
      })
      .toBeGreaterThan(0)
    await deliverProviderEvent(takeoverPage, {
      eventType: "call.hangup",
      eventId: "e2e-staff-hangup",
      occurredAt: new Date().toISOString(),
      payload: {
        ...providerLegPayload(staffClientState),
        hangup_cause: "normal_clearing",
      },
    })
    await expect(
      callCenter(takeoverPage).getByText("Call ended", { exact: true }),
    ).toBeVisible()
    await takeoverPage.getByRole("button", { name: "Create task" }).click()
    await expect
      .poll(async () => {
        const result = await database.query<{ state: string }>(
          `SELECT state FROM human_calling_calls WHERE id = $1`,
          [durableCall.id],
        )
        return result.rows[0]?.state
      })
      .toBe("FOLLOW_UP_REQUIRED")
    const followUpTask = await expect
      .poll(async () => {
        const result = await database.query<{
          id: string
          title: string
          state: string
        }>(
          `SELECT id::text, title, state
             FROM work_tasks
            WHERE call_id = $1`,
          [durableCall.id],
        )
        return result.rows[0] ?? null
      })
      .not.toBeNull()
      .then(async () => {
        const result = await database.query<{
          id: string
          title: string
          state: string
        }>(
          `SELECT id::text, title, state
             FROM work_tasks
            WHERE call_id = $1`,
          [durableCall.id],
        )
        return result.rows[0]
      })
    expect(followUpTask).toEqual({
      id: expect.any(String),
      title: "Scheduling help",
      state: "OPEN",
    })
    await expect(
      takeoverPage.getByRole("heading", {
        name: "Scheduling help",
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("button", { name: /Scheduling help/ }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("heading", {
        name: "Scheduling help",
        exact: true,
      }),
    ).not.toBeVisible()
    await loserPage.getByRole("button", { name: /Scheduling help/ }).click()
    await expect(
      loserPage.getByRole("heading", {
        name: "Scheduling help",
        exact: true,
      }),
    ).toBeVisible()
    await loserPage.getByLabel("Search tasks").fill("+15555550100")
    await expect(
      loserPage.getByRole("button", { name: /Scheduling help/ }),
    ).toBeVisible()
    expect(loserPage.url()).not.toContain("5555550100")
    await loserPage.getByLabel("Search tasks").fill("")
    await expect(
      callCenter(takeoverPage).getByText("Available", { exact: true }),
    ).toBeVisible()
    await expect
      .poll(async () => {
        const result = await database.query<{ state: string }>(
          `SELECT state
             FROM human_calling_calls
            WHERE call_session_id = 'fixture-call-session-2'`,
        )
        return result.rows[0]?.state
      }, { timeout: 25_000 })
      .toBe("UNANSWERED")
    await expect(
      callCenter(takeoverPage).getByText(
        "Second Synthetic Caller · Fixture Location 1 · Abita",
        { exact: true },
      ),
    ).not.toBeVisible()

    const recoveryHandoffResponse = await takeoverPage.request.post(
      `${portalURL}/v1/handoffs`,
      {
        headers: { authorization: "Bearer synthetic-service-token" },
        data: {
          practiceId: scope.practice_id,
          locationId: scope.location_id,
          sourceCallId: "slice-2-e2e-recovery-source",
          idempotencyKey: "slice-2-e2e-recovery-handoff",
          contact: {
            phone: "+15555550102",
            phoneSource: "Abita",
            displayName: "Recovery Caller",
            nameSource: "Abita",
            transferReason: "Reordered provider proof",
            reasonSource: "Abita AI",
          },
        },
      },
    )
    expect(recoveryHandoffResponse.status()).toBe(201)
    const recoveryHandoff = (await recoveryHandoffResponse.json()) as {
      sipDestination: string
    }
    expect(recoveryHandoff.sipDestination).toBe(
      "sip:acuity-handoff@synthetic.sip.telnyx.com",
    )
    await deliverProviderEvent(takeoverPage, {
      eventType: "call.initiated",
      eventId: "e2e-recovery-caller-initiated",
      occurredAt: new Date().toISOString(),
      payload: {
        call_control_id: "fixture-recovery-caller-control",
        call_leg_id: "fixture-recovery-caller-leg",
        call_session_id: "fixture-recovery-session",
        client_state: "",
        from: "+15555550102",
        to: "+14843336938",
      },
    })
    await expect(
      takeoverPage.getByText(
        "Recovery Caller · Fixture Location 1 · Abita",
        { exact: true },
      ),
    ).toBeVisible()
    await takeoverPage.getByRole("button", { name: "Accept" }).click()
    await expect(
      takeoverPage.getByRole("heading", {
        name: "Recovery Caller",
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("heading", {
        name: "Scheduling help",
        exact: true,
      }),
    ).toBeVisible()
    const recoveryCall = await expect
      .poll(async () => {
        const result = await database.query<{
          id: string
          current_attempt_id: string
          expected_staff_call_leg_id: string | null
        }>(
          `SELECT
             id::text,
             current_attempt_id::text,
             expected_staff_call_leg_id
           FROM human_calling_calls
          WHERE call_session_id = 'fixture-recovery-session'`,
        )
        const row = result.rows[0]
        return row?.expected_staff_call_leg_id ? row : null
      })
      .not.toBeNull()
      .then(async () => {
        const result = await database.query<{
          id: string
          current_attempt_id: string
          expected_staff_call_leg_id: string
        }>(
          `SELECT
             id::text,
             current_attempt_id::text,
             expected_staff_call_leg_id
           FROM human_calling_calls
          WHERE call_session_id = 'fixture-recovery-session'`,
        )
        return result.rows[0]
      })
    await sendIncomingLegs(
      takeoverPage,
      await mediaTokenForCall(database, recoveryCall.id),
    )
    await expect(callCenter(takeoverPage).getByText(/Audio: attached/)).toBeVisible()
    const recoveryStaffState = Buffer.from(JSON.stringify({
      v: 1,
      call: recoveryCall.id,
      leg: "staff",
      attempt: recoveryCall.current_attempt_id,
    })).toString("base64")
    const recoveryBase = Date.now()
    await deliverProviderEvent(takeoverPage, {
      eventType: "call.hangup",
      eventId: "e2e-recovery-hangup-first",
      occurredAt: new Date(recoveryBase + 2_000).toISOString(),
      payload: {
        call_control_id: "fixture-staff-control",
        call_leg_id: "fixture-staff-leg",
        call_session_id: "fixture-recovery-session",
        client_state: recoveryStaffState,
        hangup_cause: "timeout",
      },
    })
    await expect
      .poll(async () => {
        const result = await database.query<{ state: string }>(
          `SELECT state FROM human_calling_calls WHERE id = $1`,
          [recoveryCall.id],
        )
        return result.rows[0]?.state
      }, { timeout: 15_000 })
      .toBe("OFFERING")
    await deliverProviderEvent(takeoverPage, {
      eventType: "call.bridged",
      eventId: "e2e-recovery-delayed-bridge",
      occurredAt: new Date(recoveryBase + 1_000).toISOString(),
      payload: {
        call_control_id: "fixture-staff-control",
        call_leg_id: "fixture-staff-leg",
        call_session_id: "fixture-recovery-session",
        client_state: recoveryStaffState,
      },
    })
    await expect(callCenter(takeoverPage).getByText("Call ended", { exact: true })).toBeVisible({
      timeout: 15_000,
    })
    await takeoverPage.getByRole("button", { name: "Resolved" }).click()
    await expect
      .poll(async () => {
        const result = await database.query<{ state: string }>(
          `SELECT state FROM human_calling_calls WHERE id = $1`,
          [recoveryCall.id],
        )
        return result.rows[0]?.state
      })
      .toBe("RESOLVED")
    await expect(
      callCenter(takeoverPage).getByText("Available", { exact: true }),
    ).toBeVisible()
    await expect(
      takeoverPage.getByRole("heading", {
        name: "Scheduling help",
        exact: true,
      }),
    ).toBeVisible()
    await takeoverPage.getByRole("button", { name: "Rename task" }).click()
    await takeoverPage.getByLabel("Task title").fill("Confirm scheduling plan")
    await takeoverPage.getByLabel("Task title").press("Enter")
    await expect(
      takeoverPage.getByRole("heading", {
        name: "Confirm scheduling plan",
        exact: true,
      }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("heading", {
        name: "Confirm scheduling plan",
        exact: true,
      }),
    ).toBeVisible()
    await takeoverPage.getByRole("button", { name: "Complete" }).click()
    await expect(
      takeoverPage.getByRole("button", { name: "Reopen" }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("button", { name: "Reopen" }),
    ).toBeVisible()
    await loserPage.getByRole("button", { name: "Reopen" }).click()
    await expect(
      takeoverPage.getByRole("button", { name: "Complete" }),
    ).toBeVisible()
    await expect(
      loserPage.getByRole("button", { name: "Complete" }),
    ).toBeVisible()
    await expect
      .poll(async () => {
        const result = await database.query<{
          task_state: string
          activity_count: string
        }>(
          `SELECT
             task.state AS task_state,
             count(activity.id)::text AS activity_count
           FROM work_tasks task
           JOIN work_task_activities activity ON activity.task_id = task.id
          WHERE task.id = $1
          GROUP BY task.state`,
          [followUpTask.id],
        )
        return result.rows[0]
      })
      .toEqual({
        task_state: "OPEN",
        activity_count: "4",
      })
    await takeoverPage.getByRole("button", { name: "Pause calls" }).click()
    await expect(
      callCenter(takeoverPage).getByText("Paused", { exact: true }),
    ).toBeVisible()
    await signalMedia(takeoverPage, "reconnecting")
    await signalMedia(takeoverPage, "ready")
    await expect(
      callCenter(takeoverPage).getByText("Paused", { exact: true }),
    ).toBeVisible()
    await expect
      .poll(async () => {
        const result = await database.query<{ desired_available: boolean }>(
          `SELECT desired_available
             FROM human_calling_softphone_leases lease
             JOIN auth."user" actor ON actor.id = lease.user_subject
            WHERE actor.email = $1`,
          [
            winnerPage === selectedPage
              ? "selected@abita.test"
              : "secondary@abita.test",
          ],
        )
        return result.rows[0]?.desired_available
      })
      .toBe(false)
  } finally {
    await database.end()
    await secondaryContext.close()
  }
})

async function prepareBrowser(context: BrowserContext) {
  await context.grantPermissions(["microphone", "notifications"], {
    origin: webURL,
  })
  await context.addInitScript(() => {
    const state = {
      answers: 0,
      rejects: 0,
      disconnects: 0,
      ringtonePulses: 0,
      ringtoneStops: 0,
      notifications: [] as Array<{ title: string; body: string; tag: string }>,
      incoming: undefined as
        | undefined
        | ((id: string, legID: string, recovery: boolean) => void),
      signal: undefined as undefined | ((state: string) => void),
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
          start: () => {
            state.ringtonePulses += 1
          },
          stop: () => {
            state.ringtoneStops += 1
          },
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
      constructor(
        title: string,
        options?: { body?: string; tag?: string },
      ) {
        state.notifications.push({
          title,
          body: options?.body ?? "",
          tag: options?.tag ?? "",
        })
      }
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
            }) => void
          },
        ) => {
          state.signal = callbacks.onState
          state.incoming = (
            providerLegID: string,
            mediaToken: string,
            recovery: boolean,
          ) =>
            callbacks.onIncoming({
              providerLegID,
              mediaToken,
              recovery,
              answer: async () => {
                state.answers += 1
              },
              reject: async () => {
                state.rejects += 1
              },
              mute: () => undefined,
              unmute: () => undefined,
            })
          callbacks.onState("ready")
        },
        disconnect: async () => {
          state.disconnects += 1
        },
      }),
    })
  })
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
  await expect(page.getByRole("region", { name: "Call Center" })).toBeVisible()
}

async function enableCalling(page: Page) {
  await page.getByRole("button", { name: "Enable calling" }).click()
  await expect(
    callCenter(page).getByText("Available", { exact: true }),
  ).toBeVisible()
}

function callCenter(page: Page) {
  return page.getByRole("region", { name: "Call Center" })
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

function providerLegPayload(clientState: string) {
  return {
    call_control_id: "fixture-staff-control",
    call_leg_id: "fixture-staff-leg",
    call_session_id: "fixture-call-session",
    client_state: clientState,
  }
}

async function sendIncomingLegs(page: Page, mediaToken: string) {
  await sendIncomingLeg(page, "unrelated-browser-leg", "unrelated-media-token")
  await sendIncomingLeg(page, "fixture-browser-leg", mediaToken)
}

async function sendIncomingLeg(
  page: Page,
  providerLegID: string,
  mediaToken: string,
  recovery = false,
) {
  await page.evaluate(({ providerLegID, mediaToken, recovery }) => {
    const fixture = window as typeof window & {
      __acuityCallingTestState: {
        incoming?: (
          providerLegID: string,
          mediaToken: string,
          recovery: boolean,
        ) => void
      }
    }
    fixture.__acuityCallingTestState.incoming?.(
      providerLegID,
      mediaToken,
      recovery,
    )
  }, { providerLegID, mediaToken, recovery })
}

async function mediaTokenForCall(database: Pool, callID: string) {
  return expect
    .poll(async () => {
      const result = await database.query<{ token: string | null }>(
        `SELECT payload->'custom_headers'->0->>'value' AS token
           FROM human_calling_provider_commands
          WHERE call_id = $1 AND action = 'DIAL_STAFF'
          ORDER BY created_at DESC
          LIMIT 1`,
        [callID],
      )
      return result.rows[0]?.token ?? ""
    })
    .not.toBe("")
    .then(async () => {
      const result = await database.query<{ token: string }>(
        `SELECT payload->'custom_headers'->0->>'value' AS token
           FROM human_calling_provider_commands
          WHERE call_id = $1 AND action = 'DIAL_STAFF'
          ORDER BY created_at DESC
          LIMIT 1`,
        [callID],
      )
      return result.rows[0].token
    })
}

async function mediaCount(
  page: Page,
  key: "answers" | "rejects" | "disconnects",
) {
  return page.evaluate(
    ({ value }) =>
      (window as typeof window & {
        __acuityCallingTestState: {
          answers: number
          rejects: number
          disconnects: number
        }
      }).__acuityCallingTestState[value],
    { value: key },
  )
}

async function callingMetric(
  page: Page,
  key: "ringtonePulses" | "ringtoneStops",
) {
  return page.evaluate(
    ({ value }) =>
      (window as typeof window & {
        __acuityCallingTestState: {
          ringtonePulses: number
          ringtoneStops: number
        }
      }).__acuityCallingTestState[value],
    { value: key },
  )
}

async function callingNotifications(page: Page) {
  return page.evaluate(
    () =>
      (window as typeof window & {
        __acuityCallingTestState: {
          notifications: Array<{ title: string; body: string; tag: string }>
        }
      }).__acuityCallingTestState.notifications,
  )
}

async function setPageHidden(page: Page) {
  await page.evaluate(() => {
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => true,
    })
    Object.defineProperty(document, "visibilityState", {
      configurable: true,
      get: () => "hidden",
    })
  })
}

async function signalMedia(
  page: Page,
  state: "reconnecting" | "ready",
) {
  await page.evaluate((nextState) => {
    const fixture = window as typeof window & {
      __acuityCallingTestState: {
        signal?: (state: string) => void
      }
    }
    fixture.__acuityCallingTestState.signal?.(nextState)
  }, state)
}
