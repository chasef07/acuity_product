import { expect, type Locator, type Page } from "@playwright/test"

const webURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:13000"

export async function signInAs(page: Page, email: string, name: string) {
  const response = await page.request.post(`${webURL}/api/test/session`, {
    data: { email, name },
  })
  expect(response.ok()).toBeTruthy()
  await page.goto("/workspace")
}

// These fixtures start with inactive days. Each curve must start at zero and
// remain one SVG path segment across later inactive days.
export async function expectConnectedZeroBaseline(chart: Locator, series: number) {
  const curves = chart.locator(".recharts-line-curve")
  await expect(curves).toHaveCount(series)
  await expect(async () => {
    const connected = await curves.evaluateAll((paths) =>
      paths.every((path) => {
        const d = path.getAttribute("d") ?? ""
        const grid = path.closest("svg")?.querySelectorAll(
          ".recharts-cartesian-grid-horizontal line",
        )
        const baseline = Math.max(
          ...Array.from(grid ?? [], (line) => Number(line.getAttribute("y1"))),
        )
        const startY = Number(d.match(/^M[^,]+,([\d.-]+)/)?.[1])
        return (
          (d.match(/M/g) ?? []).length === 1 &&
          Math.abs(startY - baseline) < 0.01
        )
      }),
    )
    expect(connected).toBe(true)
  }).toPass()
}
