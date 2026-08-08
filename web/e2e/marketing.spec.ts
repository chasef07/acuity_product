import { expect, test } from "@playwright/test"

test("short tablet view keeps active work-stack details scrollable", async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 600 })
  await page.goto("/")

  const section = page.locator("#how-it-works")
  await section.evaluate((element) => {
    const sectionElement = element as HTMLElement
    const scrollRange = sectionElement.clientHeight - window.innerHeight
    window.scrollTo(0, sectionElement.offsetTop + scrollRange * 0.65)
  })

  const activeDetail = page.getByTestId("work-stack-detail").filter({
    has: page.getByText("Acuity completes the work behind every signal."),
  })

  await expect(activeDetail).toHaveAttribute("tabindex", "0")
  await expect(activeDetail).toHaveCSS("overflow-y", "auto")

  const scrollPosition = await activeDetail.evaluate((element) => {
    element.scrollTop = element.scrollHeight
    return element.scrollTop
  })
  expect(scrollPosition).toBeGreaterThan(0)
})
