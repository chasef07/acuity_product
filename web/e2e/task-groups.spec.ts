import { expect, test } from "@playwright/test"
import { signInAs } from "./support"

const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"

test("staff review grouped requests, correct one group, and retain feedback after Resolve group", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  let feedbackTaskID = ""
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
    if (key === "glasses") feedbackTaskID = (await response.json()).taskId
  }
  // Previously submitted feedback remains durable even though its staff control is removed.
  const initialTokenResponse = await page.request.get("/api/auth/token")
  const { token: initialToken } = await initialTokenResponse.json()
  const headers = { authorization: `Bearer ${initialToken}` }
  const feedbackTask = await (await page.request.get(`${portalURL}/v1/tasks/${feedbackTaskID}`, { headers })).json()
  const feedbackResponse = await page.request.post(`${portalURL}/v1/tasks/${feedbackTaskID}/knowledge-feedback`, {
    headers,
    data: { expectedVersion: feedbackTask.version, flagged: true, suggestedAnswer: "Confirm the prescription-copy delivery process with the office." },
  })
  expect(feedbackResponse.ok()).toBeTruthy()
  await page.reload()
  const tasksSection=page.getByRole("button",{name:/^Tasks/})
  if(await tasksSection.getAttribute("aria-expanded")==="false") await tasksSection.click()
  await expect(page.getByRole("button", { name: "My groups", exact: true })).toHaveAttribute("aria-pressed", "true")
  const group = page.getByTestId("task-group-row").filter({ hasText: "Group request" })
  await expect(group.getByLabel("3 Tasks")).toBeVisible()
  await group.getByRole("button").click()
  const panel = page.getByRole("region", { name: "Related Tasks", exact: true })
  await expect(panel.getByRole("article")).toHaveCount(3)
  await expect(group.getByRole("article")).toHaveCount(0)
  await page.mouse.move(700, 50)
  await expect(panel.getByRole("button", { name: "Resolve group", exact: true })).toBeInViewport()
  await page.screenshot({path:testInfo.outputPath("grouped-tasks.png"),fullPage:true})
  const medication = panel.getByRole("article", { name: "Task: Group request medication" })
  await expect(medication.getByLabel("Move to group")).toHaveCount(0)
  await expect(medication.getByLabel("Suggested answer (optional)")).toHaveCount(0)
  await expect(medication.getByRole("button", { name: "Save feedback" })).toHaveCount(0)
  await medication.getByRole("button", { name: "Move task", exact: true }).click()
  await expect(page.getByRole("menuitemradio", { name: "Optical, frames & prescriptions", exact: true })).toHaveAttribute("aria-checked", "true")
  await page.screenshot({ path: testInfo.outputPath("move-task-menu.png"), fullPage: true })
  await page.getByRole("menuitemradio", { name: "Clinical, medication & pharmacy", exact: true }).click()
  await expect(group.getByLabel("2 Tasks")).toBeVisible()
  // A newly arrived request must be reviewed before it can join Resolve group.
  const newRequest = await page.request.post(`${portalURL}/v1/tasks`, {
    headers: { authorization: "Bearer synthetic-service-token" },
    data: { callId: "task-groups-frames", callerPhone: "+12025550148", category: "optical", idempotencyKey: "task-groups-frames", officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency: "normal", summary: "Group request frames", message: "Please check whether the frames are ready.", patient: { name: "Drew Example" } },
  })
  expect(newRequest.status()).toBe(201)
  await expect(group.getByLabel("3 Tasks")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Resolve group", exact: true })).toBeDisabled()
  await panel.getByRole("button", { name: "Review latest", exact: true }).click()
  await expect(panel.getByRole("article")).toHaveCount(3)
  const frames = panel.getByRole("article", { name: "Task: Group request frames" })
  await frames.getByRole("button", { name: "Resolve", exact: true }).click()
  await expect(frames).toHaveCount(0)
  await expect(group.getByLabel("2 Tasks")).toBeVisible()
  await expect(panel.getByLabel("Flag knowledge-base question")).toHaveCount(0)
  await expect(panel.getByLabel("Suggested answer (optional)")).toHaveCount(0)
  await expect(panel.getByRole("button", { name: "Save feedback" })).toHaveCount(0)
  await panel.getByRole("button", { name: "Resolve group", exact: true }).click()
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
  const persistedResponse = await page.request.get(`${portalURL}/v1/tasks/${feedbackTaskID}`, {
    headers: { authorization: `Bearer ${token}` },
  })
  expect(persistedResponse.ok()).toBeTruthy()
  const persisted = await persistedResponse.json()
  expect(persisted.state).toBe("COMPLETED")
  expect(persisted.knowledgeFlagged).toBe(true)
  expect(persisted.suggestedAnswer).toBe("Confirm the prescription-copy delivery process with the office.")
})
