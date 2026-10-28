import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { CallTranscript } from "./agent-call-panel.tsx"
import { CallFlow } from "./scorecard-flow.tsx"

for (const [result, label] of [["pass", "Pass"], ["fail", "Fail"], ["unknown", "Incomplete"], [undefined, "Incomplete"]] as const) {
  test(`staff call flow shows ${label} with no detailed scorecard (${result})`, () => {
    const markup = renderToStaticMarkup(<CallFlow call={{ intent: "appointment_information", outcome: "resolved" }} result={result} />)
    assert.match(markup, /Appointment confirmation\/details/)
    assert.match(markup, /Resolved/)
    assert.match(markup, new RegExp(`>${label}<`))
    assert.doesNotMatch(markup, /Review judge checks|Caller sentiment|<details|Scorecard/)
  })
}

test("the transcript puts the agent on the right and the caller on the left, like Work", () => {
  const markup = renderToStaticMarkup(
    <CallTranscript
      messages={[
        { speaker: "Caller", text: "I need to reschedule.", occurredAt: "2026-08-30T12:00:00Z" },
        { speaker: "Agent", text: "I can help with that.", occurredAt: "2026-08-30T12:00:05Z" },
      ]}
    />,
  )
  const sides = [...markup.matchAll(/data-slot="message" data-align="(start|end)"[\s\S]*?data-slot="message-header"[^>]*>(Caller|Agent)</g)]
    .map(([, align, speaker]) => `${speaker}:${align}`)
  assert.deepEqual(sides, ["Caller:start", "Agent:end"])
})

test("an empty transcript says nothing was recorded", () => {
  assert.match(renderToStaticMarkup(<CallTranscript messages={[]} />), /No transcript available/)
})

test("a knowledge gap highlights only the first caller message with the judged words", () => {
  const markup = renderToStaticMarkup(
    <CallTranscript
      highlight="You open on Thanksgiving?"
      messages={[
        { speaker: "Agent", text: "You open on Thanksgiving?", occurredAt: "2026-08-30T12:00:00Z" },
        { speaker: "Caller", text: "You open on Thanksgiving?", occurredAt: "2026-08-30T12:00:05Z" },
        { speaker: "Caller", text: "You open on Thanksgiving?", occurredAt: "2026-08-30T12:00:09Z" },
      ]}
    />,
  )
  assert.equal(markup.match(/Not in the knowledge base/g)?.length, 1)
  const label = markup.indexOf("Not in the knowledge base")
  assert.ok(markup.indexOf("2026-08-30T12:00:00Z") < label && label < markup.indexOf("2026-08-30T12:00:05Z"))
})
