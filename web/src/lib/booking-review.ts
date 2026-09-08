export const failureBuckets = [
  {
    id: "availability",
    label: "No suitable availability",
    scenario:
      "A new caller wants an appointment this week, but no offered slot fits.",
    expected:
      "Clarify scheduling constraints, offer useful alternatives, and arrange an explicit follow-up if no slot works.",
  },
  {
    id: "insurance",
    label: "Insurance or eligibility",
    scenario:
      "A caller wants to book but is unsure whether their plan is accepted.",
    expected:
      "Resolve the plan and visit-type ambiguity before continuing; explain any remaining uncertainty and next action.",
  },
  {
    id: "identity",
    label: "Patient lookup or verification",
    scenario:
      "An existing caller wants an appointment, but the initial patient lookup does not find a unique match.",
    expected:
      "Recover the lookup without repeating the same failed step or claiming verified identity prematurely.",
  },
  {
    id: "tool",
    label: "Tool error or booking failure",
    scenario:
      "A caller accepts an offered slot, but the booking tool returns an error.",
    expected:
      "Do not claim success. Recover safely or preserve an actionable follow-up with a clear explanation to the caller.",
  },
  {
    id: "conversation",
    label: "Conversation friction or loop",
    scenario:
      "A caller has already stated their scheduling preference, but the agent asks for it again.",
    expected:
      "Retain the preference, avoid repeated availability loops, and advance toward a confirmed outcome.",
  },
  {
    id: "dropoff",
    label: "Caller dropped off or declined",
    scenario:
      "After hearing available slots, a caller declines or ends the conversation before choosing one.",
    expected:
      "Clarify the obstacle while the caller is present and accurately represent the unresolved outcome.",
  },
  {
    id: "handoff",
    label: "Appropriate handoff / other intent",
    scenario:
      "After an availability check, the caller asks to speak with staff about a request the agent cannot resolve.",
    expected:
      "Honor the request and preserve the handoff evidence without claiming an appointment was booked.",
  },
  {
    id: "evidence",
    label: "Insufficient or conflicting evidence",
    scenario:
      "The conversation suggests a booking, but there is no successful booking receipt.",
    expected:
      "Treat the booking as unconfirmed and reconcile the evidence before reporting success.",
  },
  {
    id: "other",
    label: "Other pattern",
    scenario:
      "Describe a synthetic caller and the specific obstacle to booking.",
    expected:
      "Describe the observable behavior that would resolve the caller's need.",
  },
] as const

export type FailureBucket = (typeof failureBuckets)[number]["id"]
export type BookingReviewNote = {
  bucket: FailureBucket
  observation: string
  scenario: string
  expected: string
  savedAt: string
}
export type BookingReviewNotes = Record<string, BookingReviewNote>

export function parseBookingReviewNotes(
  raw: string | null,
): BookingReviewNotes {
  if (!raw) return {}
  const data: unknown = JSON.parse(raw)
  if (!data || typeof data !== "object" || Array.isArray(data))
    throw new Error("Invalid review file")
  const result: BookingReviewNotes = {}
  for (const [id, note] of Object.entries(data)) {
    if (
      !note ||
      typeof note !== "object" ||
      !failureBuckets.some((b) => b.id === note.bucket) ||
      ![note.observation, note.scenario, note.expected, note.savedAt].every(
        (v) => typeof v === "string",
      )
    )
      throw new Error("Invalid review note")
    result[id] = note
  }
  return result
}

// Only human-authored scenario fields leave the review. Never copy transcript,
// patient details, tool payloads, or freeform evidence observations into drafts.
export function scenarioMarkdown(
  notes: BookingReviewNotes,
  callIDs: string[],
): string {
  const entries = callIDs.flatMap((id) => {
    const note = notes[id]
    if (!note?.scenario.trim() || !note.expected.trim()) return []
    const label = failureBuckets.find((b) => b.id === note.bucket)!.label
    return [
      `## ${label}\n\nCaller scenario\n\n${note.scenario.trim()}\n\nExpected behavior / pass criteria\n\n${note.expected.trim()}`,
    ]
  })
  return `# LiveKit scenario drafts\n\nHuman-authored scenarios for manual adaptation in LiveKit. Review for synthetic, PHI-free content before use.\n\n${entries.join("\n\n---\n\n")}\n`
}
