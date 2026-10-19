import assert from "node:assert/strict"
import test from "node:test"
import { breakdownLists, callFlow, matchesSelection, mergeSelection, rankProblems } from "./call-paths.ts"

const route = (intent: string | undefined, outcome: string | undefined, evaluated: boolean, failures: string[], calls: number) =>
  ({ intent, outcome, evaluated, failures, calls }) as Parameters<typeof rankProblems>[0][number]

const paths = [
  route("new_patient_booking", "abandoned", true, [], 12),
  route("new_patient_booking", "resolved", true, [], 16),
  route("speak_to_staff", "transferred", true, [], 60),
  route("speak_to_staff", "transferred", true, ["transfer_policy_followed"], 5),
  route("speak_to_staff", "abandoned", true, [], 3),
  route("cancel_appointment", "abandoned", true, ["knowledge_available"], 2),
  route("speak_to_staff", "transferred", true, ["knowledge_available", "transfer_policy_followed"], 1),
  route(undefined, undefined, false, [], 41),
]
const labels = { transfer_policy_followed: "Transfer policy followed", knowledge_available: "Office knowledge had the answer" }

test("problems rank intent and failure pairs by calls, with rates against every intent", () => {
  const problems = rankProblems(paths, labels, 10)
  assert.deepEqual(problems.map((problem) => [problem.intent, problem.failure ?? problem.outcome, problem.calls, problem.intentCalls, problem.tooFew]), [
    ["new_patient_booking", "abandoned", 12, 28, false],
    ["speak_to_staff", "transfer_policy_followed", 6, 69, false],
    ["speak_to_staff", "abandoned", 3, 69, false],
    ["speak_to_staff", "knowledge_available", 1, 69, false],
    ["cancel_appointment", "abandoned", 2, 2, true],
    ["cancel_appointment", "knowledge_available", 2, 2, true],
  ])
  const abandoned = problems[0]
  assert.equal(abandoned.label, "Abandoned")
  assert.equal(Math.round(abandoned.rate * 100), 43)
  assert.equal(Math.round(abandoned.averageRate * 100), 17)
  assert.equal(problems[1].label, "Transfer policy followed")
  assert.equal(problems[1].kbGap, false)
  assert.equal(problems[3].kbGap, true)
})

test("flow keeps every call, sends only failures to reasons, and marks KB gaps apart from agent mistakes", () => {
  const flow = callFlow(paths, labels)
  const name = (index: number) => flow.nodes[index].name
  const total = (column: number) => flow.links.filter((link) => flow.nodes[link.source].column === column).reduce((sum, link) => sum + link.value, 0)
  assert.equal(flow.nodes[0].name, "All calls")
  assert.equal(total(0), 140)
  assert.equal(total(1), 140)
  assert.equal(total(2), 140)
  const reasons = flow.links.filter((link) => flow.nodes[link.source].name === "Fail").map((link) => [name(link.target), link.value])
  assert.deepEqual(reasons, [["Transfer policy followed", 5], ["Office knowledge had the answer", 2], ["Multiple issues", 1]])
  const tone = (label: string) => flow.nodes.find((node) => node.name === label)!.tone
  assert.equal(tone("Office knowledge had the answer"), "gap")
  assert.equal(tone("Transfer policy followed"), "fail")
  assert.equal(tone("Pass"), "pass")
  assert.equal(tone("Not judged"), "muted")
  assert.equal(tone("No intent"), "muted")
  const intents = flow.nodes.filter((node) => node.column === 1).map((node) => node.name)
  assert.deepEqual(intents, ["New patient booking", "Cancel appointment", "Speak to staff", "No intent"])
})

test("intents past the top seven share one unclickable bucket, apart from the real Other intent", () => {
  const many = ["other", "speak_to_staff", "office_information", "cancel_appointment", "reschedule_appointment", "billing_question", "insurance_question", "records_or_forms", "returning_call"]
    .map((intent, index) => route(intent, "resolved", true, [], 20 - index))
  const flow = callFlow(many, labels)
  const intents = flow.nodes.filter((node) => node.column === 1)
  const other = intents.find((node) => node.name === "Other")!
  const rest = intents.find((node) => node.name === "Other intents")!
  assert.deepEqual(other.selection, { intent: "other" })
  assert.equal(rest.selection, undefined)
  const into = (target: typeof rest) => flow.links.filter((link) => flow.nodes[link.target] === target).reduce((sum, link) => sum + link.value, 0)
  assert.equal(into(other), 20)
  assert.equal(into(rest), 12 + 13)
})

test("every flow box and band carries the selection it would apply", () => {
  const flow = callFlow(paths, labels)
  const selection = (name: string) => flow.nodes.find((node) => node.name === name)!.selection
  assert.deepEqual(selection("New patient booking"), { intent: "new_patient_booking" })
  assert.deepEqual(selection("Abandoned"), { outcome: "abandoned" })
  assert.deepEqual(selection("Pass"), { judge: "pass" })
  assert.deepEqual(selection("Not judged"), { judge: "unjudged" })
  assert.deepEqual(selection("Office knowledge had the answer"), { failure: "knowledge_available" })
  assert.equal(selection("Multiple issues"), undefined)
  assert.equal(selection("No intent"), undefined)
  assert.equal(selection("All calls"), undefined)
})

test("selections match routes, and merging drops contradictions", () => {
  const sum = (selection: Parameters<typeof matchesSelection>[1]) => paths.filter((path) => matchesSelection(path, selection)).reduce((total, path) => total + path.calls, 0)
  assert.equal(sum({}), 140)
  assert.equal(sum({ intent: "speak_to_staff", judge: "fail" }), 6)
  assert.equal(sum({ failure: "transfer_policy_followed" }), 6)
  assert.equal(sum({ judge: "pass" }), 91)
  assert.equal(sum({ judge: "unjudged" }), 41)
  assert.deepEqual(mergeSelection({ judge: "pass", intent: "cancel_appointment" }, { failure: "knowledge_available" }), { intent: "cancel_appointment", failure: "knowledge_available" })
  assert.deepEqual(mergeSelection({ failure: "knowledge_available" }, { judge: "unjudged" }), { judge: "unjudged" })
  assert.deepEqual(mergeSelection({ failure: "knowledge_available" }, { judge: "fail" }), { failure: "knowledge_available", judge: "fail" })
})

test("list view breaks the selected calls down by every column, keeping unlabeled calls", () => {
  const lists = breakdownLists(paths.filter((path) => matchesSelection(path, { outcome: "abandoned" })), labels)
  const rows = (title: string) => lists.find((list) => list.title === title)!.rows.map((row) => [row.label, row.calls])
  assert.equal(lists.find((list) => list.title === "Intents")!.total, 17)
  assert.deepEqual(rows("Intents"), [["New patient booking", 12], ["Speak to staff", 3], ["Cancel appointment", 2]])
  assert.deepEqual(rows("Judge results"), [["Pass", 15], ["Fail", 2]])
  assert.deepEqual(rows("Failure reasons"), [["Office knowledge had the answer", 2]])
  const all = breakdownLists(paths, labels)
  assert.deepEqual(all.find((list) => list.title === "Intents")!.rows.at(-1), { label: "Unlabeled", calls: 41 })
})
