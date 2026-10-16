import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"

import { CallTranscript } from "./agent-call-panel.tsx"

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
