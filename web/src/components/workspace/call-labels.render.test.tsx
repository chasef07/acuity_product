import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { CallLabel, CallLabels } from "./call-labels.tsx"

test("both views use the same intent and outcome names", () => {
  const html = renderToStaticMarkup(<CallLabels call={{ intent: "existing_patient_booking", outcome: "resolved", labelingStatus: "complete" }} />)
  assert.match(html, /Existing patient booking/)
  assert.match(html, /Resolved/)
  assert.match(html, /Intent/)
  assert.match(html, /Outcome/)
})

test("missing labels distinguish pending, failed, and historical calls", () => {
  assert.match(renderToStaticMarkup(<CallLabel kind="outcome" call={{ labelingStatus: "processing" }} />), /Pending/)
  assert.match(renderToStaticMarkup(<CallLabel kind="outcome" call={{ labelingStatus: "failed" }} />), /Unavailable/)
  assert.match(renderToStaticMarkup(<CallLabel kind="outcome" call={{ labelingStatus: "skipped" }} />), /Unavailable/)
  assert.match(renderToStaticMarkup(<CallLabel kind="outcome" call={{}} />), /Not labeled/)
  assert.doesNotMatch(renderToStaticMarkup(<CallLabel kind="outcome" call={{ labelingStatus: "failed", intent: "insurance_question" }} />), /Abandoned|Resolved|Transferred/)
  assert.match(renderToStaticMarkup(<CallLabel kind="intent" call={{ labelingStatus: "failed", intent: "insurance_question" }} />), /Insurance question/)
})
