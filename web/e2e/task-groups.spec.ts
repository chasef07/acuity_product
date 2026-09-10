import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"

test("staff review grouped requests, correct one group, and retain feedback after Resolve group", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  for (const [key, name, message] of [
    ["glasses", "Alex Example", "Please send the glasses prescription copy."],
    ["contacts", "Blair Example", "Please send my contact lens prescription copy."],
    ["medication", "Casey Example", "Please refill the medication at the pharmacy."],
  ]) {
    const response = await page.request.post(`${portalURL}/v1/tasks`, {
      headers: { authorization: "Bearer synthetic-service-token" },
      data: { callId: `task-groups-${key}`, callerPhone: "+12025550148", category: "optical", idempotencyKey: `task-groups-${key}`, officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency: "normal", summary: `Group request ${key}`, message, patient: { name } },
    })
    expect(response.status()).toBe(201)
  }
  await page.reload()
  const tasksSection=page.getByRole("button",{name:/^Tasks/})
  if(await tasksSection.getAttribute("aria-expanded")==="false") await tasksSection.click()
  await expect(page.getByRole("button", { name: "My groups", exact: true })).toHaveAttribute("aria-pressed", "true")
  const group = page.getByTestId("task-group-row").filter({ hasText: "(202) 555-0148" })
  await expect(group).toContainText("3 Tasks")
  await group.getByRole("button", { expanded: false }).click()
  await expect(group.getByRole("article")).toHaveCount(3)
  await page.screenshot({path:testInfo.outputPath("grouped-tasks.png"),fullPage:true})
  const medication = group.getByRole("article", { name: "Task: Group request medication" })
  await medication.getByLabel("Move to group").selectOption("medication")
  await medication.getByRole("button", { name: "Move", exact: true }).click()
  await expect(group).toContainText("2 Tasks")
  await group.getByRole("button", { expanded: false }).click()
  const glasses = group.getByRole("article", { name: "Task: Group request glasses" })
  await glasses.getByLabel("Flag knowledge-base question").check()
  await glasses.getByLabel("Suggested answer (optional)").fill("Confirm the prescription-copy delivery process with the office.")
  const feedbackSaved = page.waitForResponse((response) => response.url().endsWith("/knowledge-feedback") && response.status() === 200)
  await glasses.getByRole("button", { name: "Save feedback" }).click()
  const savedTask = await (await feedbackSaved).json()
  await group.getByRole("button", { expanded: false }).click()
  await group.getByRole("button", { name: "Resolve group", exact: true }).click()
  await expect(group).toHaveCount(0)
  await expect(page.getByTestId("task-row").filter({ hasText: "Group request medication" })).toBeVisible()
  await expect(page.getByLabel("Task status")).toHaveCount(0)
  await expect(page.getByLabel("Knowledge flagged", { exact: true })).toHaveCount(0)
  await expect(page.getByText(/matching Tasks.*rows loaded/)).toHaveCount(0)
  await page.getByRole("button", { name: "All tasks", exact: true }).click()
  await page.reload()
  const tokenResponse = await page.request.get("/api/auth/token")
  expect(tokenResponse.ok()).toBeTruthy()
  const { token } = await tokenResponse.json()
  const persistedResponse = await page.request.get(`${portalURL}/v1/tasks/${savedTask.id}`, {
    headers: { authorization: `Bearer ${token}` },
  })
  expect(persistedResponse.ok()).toBeTruthy()
  const persisted = await persistedResponse.json()
  expect(persisted.state).toBe("COMPLETED")
  expect(persisted.knowledgeFlagged).toBe(true)
  expect(persisted.suggestedAnswer).toBe("Confirm the prescription-copy delivery process with the office.")
})
