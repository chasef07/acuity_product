import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { MiddlewareRequestDetails } from "./middleware-request-details.tsx"

test("tool detail separates HTTP success from a rejected provider outcome", () => {
  const html = renderToStaticMarkup(
    <MiddlewareRequestDetails
      requests={[
        {
          requestId: "fb672b37-0211-4e69-baf1-b0f56b181911",
          operation: "createPatient",
          attempt: 1,
          durationMs: 1400,
          result: "response",
          httpStatus: 200,
          outcome: "rejected",
          failureReason: "request_rejected",
          retryable: false,
          providerErrorCount: 2,
          providerErrors: [
            {
              operation: "addpatient",
              category: "rejected",
              httpStatus: 200,
              code: "-32602",
              durationMs: 1300,
            },
          ],
        },
      ]}
    />,
  )
  for (const text of [
    "HTTP 200",
    "outcome: rejected",
    "code -32602",
    "attempt 1",
    "retry not allowed",
    "fb672b37-0211-4e69-baf1-b0f56b181911",
    "Showing 1 of 2",
  ])
    assert.ok(html.includes(text), text)
  assert.doesNotMatch(html, /completed|booked successfully/i)
})

test("older tool executions have no empty diagnostic panel", () => {
  assert.equal(renderToStaticMarkup(<MiddlewareRequestDetails />), "")
})

test("tool detail exposes a missing appointment ID without claiming booking success", () => {
  const html = renderToStaticMarkup(
    <MiddlewareRequestDetails
      requests={[
        {
          requestId: "fb672b37-0211-4e69-baf1-b0f56b181911",
          operation: "bookAppointment",
          attempt: 1,
          durationMs: 1,
          result: "response",
          httpStatus: 200,
          responseStatus: "booked",
          failureReason: "invalid_response",
          failureDetail: "missing_appointment_id",
          retryable: false,
        },
      ]}
    />,
  )
  for (const text of ["response: booked", "invalid_response", "missing_appointment_id", "retry not allowed"])
    assert.ok(html.includes(text), text)
  assert.doesNotMatch(html, /completed|booked successfully/i)
})
