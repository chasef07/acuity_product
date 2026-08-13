import assert from "node:assert/strict"
import test from "node:test"

import type { ConversationTimelineItem } from "./api/generated/types.gen.ts"
import {
  presentTimeline,
  recoveryFollowUpCallIDs,
  technicalTimelineItems,
} from "./workspace-history.ts"

const base = {
  id: "item",
  occurredAt: "2026-08-13T12:00:00Z",
} as const

test("history suppresses a transfer-only AI entry when the inbound staff call exists", () => {
  const items = [
    {
      ...base,
      type: "AI_INTERACTION",
      aiInteraction: {
        id: "ai",
        locationId: "location",
        locationName: "Office",
        sourceCallId: "source-call",
        phone: "+12025550123",
        startedAt: base.occurredAt,
        status: "ESCALATED",
        appointmentOutcome: "INDETERMINATE",
      },
    },
    {
      ...base,
      id: "staff-call",
      type: "CALL",
      call: {
        id: "staff-call",
        type: "CALL",
        locationId: "location",
        locationName: "Office",
        direction: "INBOUND",
        outcome: "RESOLVED",
        startedAt: base.occurredAt,
        durationSeconds: 40,
        answeredByEmail: "",
        transferReason: "",
        current: false,
        originating: false,
        sourceCallId: "source-call",
      },
    },
  ] as ConversationTimelineItem[]

  assert.deepEqual(presentTimeline(items).map((item) => item.id), ["staff-call"])
})

test("history keeps a meaningful AI outcome beside the inbound staff call", () => {
  const items = [
    {
      ...base,
      type: "AI_INTERACTION",
      aiInteraction: {
        id: "ai",
        locationId: "location",
        locationName: "Office",
        sourceCallId: "source-call",
        phone: "+12025550123",
        startedAt: base.occurredAt,
        status: "ESCALATED",
        appointmentOutcome: "BOOKING",
      },
    },
    {
      ...base,
      id: "staff-call",
      type: "CALL",
      call: {
        id: "staff-call",
        type: "CALL",
        locationId: "location",
        locationName: "Office",
        direction: "INBOUND",
        outcome: "RESOLVED",
        startedAt: base.occurredAt,
        durationSeconds: 40,
        answeredByEmail: "",
        transferReason: "",
        current: false,
        originating: false,
        sourceCallId: "source-call",
      },
    },
  ] as ConversationTimelineItem[]

  assert.deepEqual(presentTimeline(items).map((item) => item.id), [
    "item",
    "staff-call",
  ])
})

test("history combines recovery bookkeeping with its call and keeps the evidence available", () => {
  const items = [
    {
      ...base,
      type: "CALL",
      call: {
        id: "call",
        type: "CALL",
        locationId: "location",
        locationName: "Office",
        direction: "INBOUND",
        outcome: "VOICEMAIL",
        startedAt: base.occurredAt,
        durationSeconds: 0,
        answeredByEmail: "",
        transferReason: "",
        current: false,
        originating: false,
      },
    },
    {
      ...base,
      id: "task",
      type: "TASK",
      taskActivity: "TASK_CREATED",
      task: {
        id: "task",
        practiceId: "practice",
        locationId: "location",
        locationName: "Office",
        phone: "+12025550123",
        title: "Return voicemail",
        state: "OPEN",
        origin: "VOICEMAIL_RECOVERY",
        urgency: "normal",
        createdBy: { kind: "SERVICE", subject: "service" },
        createdAt: base.occurredAt,
        updatedAt: base.occurredAt,
        version: 1,
        callId: "call",
        relatedInteractionCount: 1,
        unread: false,
        interactions: [
          {
            callId: "later-call",
            type: "MISSED_CALL",
            occurredAt: "2026-08-13T12:05:00Z",
          },
        ],
      },
    },
  ] as ConversationTimelineItem[]

  assert.deepEqual(presentTimeline(items).map((item) => item.id), ["item"])
  assert.deepEqual(technicalTimelineItems(items).map((item) => item.id), ["task"])
  assert.deepEqual([...recoveryFollowUpCallIDs(items)], ["call", "later-call"])
})
