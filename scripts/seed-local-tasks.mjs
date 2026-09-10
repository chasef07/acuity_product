const categories = ["appointments", "documentation", "medication", "optical", "referrals", "other", "insurance", "pre_op", "post_op"]
const examples = [
  ...categories.map((category) => ({ category, summary: `${category.replaceAll("_", " ")} follow-up`, message: `Synthetic ${category} request for staff follow-up. Details remain to be confirmed.` })),
  { category: "optical", summary: "Glasses prescription copy", message: "Alex Example asks for a glasses prescription copy. Delivery destination still needs confirmation." },
  { category: "optical", summary: "Contact lens prescription", message: "Blair Example shares this number and asks for a separate contact lens prescription." },
  { category: "optical", summary: "Medication refill (move to Clinical)", message: "Casey Example requests a medication refill at the pharmacy. This Task was misclassified for local correction practice." },
]
for (const [index, example] of examples.entries()) {
  const response = await fetch("http://127.0.0.1:18080/v1/tasks", {
    method: "POST", headers: { "content-type": "application/json", authorization: "Bearer synthetic-service-token" },
    body: JSON.stringify({ ...example, callId: `local-call-${index}`, callerPhone: "+12025550149", officeKey: "spring-hill", officePhone: "+17275550101", idempotencyKey: `local-task-${index}`, source: "agent", urgency: "normal", patient: { name: index === 11 ? "Casey Example" : index === 10 ? "Blair Example" : "Alex Example" } }),
  })
  if (!response.ok) throw new Error(`Synthetic Task ${index}: HTTP ${response.status}`)
}
