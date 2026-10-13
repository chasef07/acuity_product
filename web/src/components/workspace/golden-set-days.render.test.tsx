import assert from "node:assert/strict"
import test from "node:test"
import { renderToStaticMarkup } from "react-dom/server"
import { GoldenSetDays } from "./golden-set-days.tsx"

test("the golden set by day shows false alarms and misses as counts, per question, and a 7-day total", () => {
  const html = renderToStaticMarkup(
    <GoldenSetDays
      label={(question) => `Label ${question}`}
      week={{ judgeNo: 14, falseAlarms: 2, humanNo: 5, misses: 1 }}
      days={[
        {
          date: "2026-10-11", judgeNo: 14, falseAlarms: 2, humanNo: 5, misses: 1,
          questions: [{ question: "promises_backed", judgeNo: 4, falseAlarms: 1, humanNo: 3, misses: 0 }],
        },
      ]}
    />,
  )
  assert.match(html, /2026-10-11/)
  assert.match(html, /2 of 14/)
  assert.match(html, /1 of 5/)
  assert.match(html, /Label promises_backed/)
  assert.match(html, /1 of 4/)
  assert.match(html, /Last 7 days/)
})

test("an empty week says so", () => {
  const html = renderToStaticMarkup(<GoldenSetDays label={(question) => question} week={{ judgeNo: 0, falseAlarms: 0, humanNo: 0, misses: 0 }} days={[]} />)
  assert.match(html, /No reviews in the last 7 days/)
  assert.match(html, /0 of 0/)
})
