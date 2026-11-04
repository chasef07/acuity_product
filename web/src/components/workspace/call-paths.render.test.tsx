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

test("problems rank intent and failure pairs by calls, leaving out intents with too few calls", () => {
  const problems = rankProblems(paths, labels, 10)
  assert.deepEqual(problems.map((problem) => [problem.intent, problem.failure ?? problem.outcome, problem.calls, Math.round(problem.rate * 100)]), [
    ["new_patient_booking", "abandoned", 12, 43],
    ["speak_to_staff", "transfer_policy_followed", 6, 9],
    ["speak_to_staff", "abandoned", 3, 4],
    ["speak_to_staff", "knowledge_available", 1, 1],
  ])
  assert.equal(problems[0].label, "Abandoned")
  assert.equal(problems[1].label, "Transfer policy followed")
})

test("flow leaves out unlabeled calls, sends only failures to reasons, and marks KB gaps apart from agent mistakes", () => {
  const flow = callFlow([...paths, route("other", undefined, true, [], 6), route(undefined, "transferred", true, [], 7), route("other", "resolved", false, [], 1)], labels)
  const name = (index: number) => flow.nodes[index].name
  const total = (column: number) => flow.links.filter((link) => flow.nodes[link.source].column === column).reduce((sum, link) => sum + link.value, 0)
  assert.equal(flow.nodes[0].name, "Labeled calls")
  assert.equal(total(0), 100)
  assert.equal(total(1), 100)
  assert.equal(total(2), 100)
  const reasons = flow.links.filter((link) => flow.nodes[link.source].name === "Fail").map((link) => [name(link.target), link.value])
  assert.deepEqual(reasons, [["Transfer policy followed", 5], ["Office knowledge had the answer", 2], ["Multiple issues", 1]])
  const tone = (label: string) => flow.nodes.find((node) => node.name === label)!.tone
  assert.equal(tone("Office knowledge had the answer"), "gap")
  assert.equal(tone("Transfer policy followed"), "fail")
  assert.equal(tone("Pass"), "pass")
  assert.equal(tone("Not judged"), "muted")
  assert.equal(flow.nodes.some((node) => node.name === "No intent" || node.name === "No outcome"), false)
  const intents = flow.nodes.filter((node) => node.column === 1).map((node) => node.name)
  assert.deepEqual(intents, ["Other", "New patient booking", "Cancel appointment", "Speak to staff"])
})

test("flow marks an insurance missing from the list as a gap, not an agent mistake", () => {
  const flow = callFlow([route("new_patient_booking", "transferred", true, ["insurance_listed"], 3)], { insurance_listed: "Insurance list had the plan" })
  assert.equal(flow.nodes.find((node) => node.name === "Insurance list had the plan")?.tone, "gap")
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
  const flow = callFlow([...paths, route("other", "resolved", false, [], 1)], labels)
  const selection = (name: string) => flow.nodes.find((node) => node.name === name)!.selection
  assert.deepEqual(selection("New patient booking"), { intent: "new_patient_booking" })
  assert.deepEqual(selection("Abandoned"), { outcome: "abandoned" })
  assert.deepEqual(selection("Pass"), { judge: "pass" })
  assert.deepEqual(selection("Not judged"), { judge: "unjudged" })
  assert.deepEqual(selection("Office knowledge had the answer"), { failure: "knowledge_available" })
  assert.equal(selection("Multiple issues"), undefined)
  assert.equal(selection("Labeled calls"), undefined)
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

test("list view breaks the selected calls down by every column, leaving unlabeled calls out of label shares", () => {
  const lists = breakdownLists(paths.filter((path) => matchesSelection(path, { outcome: "abandoned" })), labels)
  const rows = (title: string) => lists.find((list) => list.title === title)!.rows.map((row) => [row.label, row.calls])
  assert.equal(lists.find((list) => list.title === "Intents")!.total, 17)
  assert.deepEqual(rows("Intents"), [["New patient booking", 12], ["Speak to staff", 3], ["Cancel appointment", 2]])
  assert.deepEqual(rows("Judge results"), [["Pass", 15], ["Fail", 2]])
  assert.deepEqual(rows("Failure reasons"), [["Office knowledge had the answer", 2]])
  const all = breakdownLists(paths, labels)
  const list = (title: string) => all.find((list) => list.title === title)!
  assert.equal(list("Intents").rows.some((row) => !row.selection), false)
  assert.deepEqual([list("Intents").total, list("Intents").unlabeled], [99, 41])
  assert.deepEqual([list("Outcomes").total, list("Outcomes").unlabeled], [99, 41])
  assert.deepEqual([list("Judge results").total, list("Judge results").unlabeled], [140, 0])
})

test("bands select only what they show, and the highlight keeps the full chart's buckets", () => {
  const many = ["other", "speak_to_staff", "office_information", "cancel_appointment", "reschedule_appointment", "billing_question", "insurance_question", "records_or_forms", "returning_call"]
    .map((intent, index) => route(intent, "resolved", true, [], 20 - index))
  const flow = callFlow([...many, route(undefined, "resolved", false, [], 4), route("other", "resolved", true, ["a", "b"], 1)], labels, { intent: "returning_call" })
  const band = (from: string, to: string) => flow.links.find((link) => flow.nodes[link.source].name === from && flow.nodes[link.target].name === to)!
  assert.deepEqual(band("Labeled calls", "Other intents").selection, undefined)
  assert.deepEqual(band("Labeled calls", "Other").selection, { intent: "other" })
  assert.deepEqual(band("Other", "Resolved").selection, { intent: "other", outcome: "resolved" })
  assert.equal(band("Other intents", "Resolved").selection, undefined)
  assert.equal(flow.links.some((link) => flow.nodes[link.source].name === "No intent"), false)
  assert.equal(band("Fail", "Multiple issues").selection, undefined)
  assert.equal(band("Labeled calls", "Other intents").selected, 12)
  assert.equal(band("Other intents", "Resolved").selected, 12)
  assert.equal(band("Labeled calls", "Other").selected, 0)
})
