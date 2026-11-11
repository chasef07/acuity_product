import { expect, test } from "@playwright/test"
import { patientRow, showPatientTask, signInAs } from "./support"

const portalURL = process.env.E2E_PORTAL_API_URL ?? "http://127.0.0.1:18080"

test("one patient row gathers a caller's requests and empties as staff close them from the panel", async ({ page }, testInfo) => {
  test.skip(!process.env.E2E_PROVISIONING_OUTPUT, "E2E fixture required")
  await signInAs(page, "selected@abita.test", "Synthetic Staff")
  let completedTaskID = ""
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
    if (key === "glasses") completedTaskID = (await response.json()).taskId
  }
  await page.reload()
  const row = patientRow(page, "+12025550148")
  const count = row.getByTestId("patient-row-count")
  await expect(page.getByTestId("patient-row").filter({ hasText: "Group request" })).toHaveCount(1)
  await expect(row.getByTestId("patient-row-name")).toHaveText("(202) 555-0148")
  await expect(count).toHaveText("3 open")
  await expect(row.getByTestId("patient-row-reason")).toHaveText("Group request medication")
  await showPatientTask(page, "+12025550148")
  const panel = page.getByRole("complementary", { name: "Task context" })
  await expect(panel.getByRole("heading", { name: "Group request medication", exact: true })).toBeVisible()
  await expect(panel.getByTestId("patient-task-row")).toHaveCount(2)
  await expect(panel.getByRole("button", { name: "Complete & next", exact: true })).toBeInViewport()
  await page.mouse.move(700, 50)
  await page.screenshot({ animations: "disabled", path: testInfo.outputPath("patient-row-open-tasks.png"), fullPage: true })
  const category = panel.getByRole("group", { name: "Task group" })
  await category.getByRole("button", { name: "Move task", exact: true }).click()
  await expect(page.getByRole("menuitemradio", { name: "Optical, frames & prescriptions", exact: true })).toHaveAttribute("aria-checked", "true")
  await page.getByRole("menuitemradio", { name: "Clinical, medication & pharmacy", exact: true }).click()
  await expect(category).toContainText("Clinical, medication & pharmacy")
  await expect(count).toHaveText("3 open")
  const newRequest = await page.request.post(`${portalURL}/v1/tasks`, {
    headers: { authorization: "Bearer synthetic-service-token" },
    data: { callId: "task-groups-frames", callerPhone: "+12025550148", category: "optical", idempotencyKey: "task-groups-frames", officeKey: "spring-hill", officePhone: "+17275550101", source: "agent", urgency: "normal", summary: "Group request frames", message: "Please check whether the frames are ready.", patient: { name: "Drew Example" } },
  })
  expect(newRequest.status()).toBe(201)
  await expect(count).toHaveText("4 open")
  await expect(row.getByTestId("patient-row-reason")).toHaveText("Group request frames")
  await expect(panel.getByRole("heading", { name: "Group request medication", exact: true })).toBeVisible()
  await expect(panel.getByTestId("patient-task-row")).toHaveCount(3)
  for (const [closed, left] of [["frames", "3 open"], ["glasses", "2 open"]] as const) {
    await panel.getByRole("button", { name: `Mark done: Group request ${closed}`, exact: true }).click()
    await expect(panel.getByRole("button", { name: `Mark done: Group request ${closed}`, exact: true })).toHaveCount(0)
    await expect(count).toHaveText(left)
  }
  await expect(page.getByTestId("patient-row").filter({ hasText: "Group request" })).toHaveCount(1)
  await expect(page.getByLabel("Task status")).toHaveCount(0)
  await expect(page.getByText(/matching Tasks.*rows loaded/)).toHaveCount(0)
  await panel.getByRole("button", { name: "Mark done: Group request contacts", exact: true }).click()
  await expect(count).toHaveCount(0)
  await expect(row.getByTestId("patient-row-reason")).toHaveText("Group request medication")
  await panel.getByRole("button", { name: "Complete & next", exact: true }).click()
  await expect(row).toHaveCount(0)
  await page.reload()
  await page.reload()
  const tokenResponse = await page.request.get("/api/auth/token")
  expect(tokenResponse.ok()).toBeTruthy()
  const { token } = await tokenResponse.json()
  const persistedResponse = await page.request.get(`${portalURL}/v1/tasks/${completedTaskID}`, {
    headers: { authorization: `Bearer ${token}` },
  })
  expect(persistedResponse.ok()).toBeTruthy()
  const persisted = await persistedResponse.json()
  expect(persisted.state).toBe("COMPLETED")
})
