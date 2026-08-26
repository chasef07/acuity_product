import { expect, test } from "@playwright/test"

test("enterprise story leads from medical voice to the Acuity Method", async ({ page }) => {
  await page.goto("/")

  await expect(
    page.getByRole("heading", { name: "Redesign patient access." }),
  ).toBeVisible()
  await expect(page.getByText("Voice agents", { exact: true })).toBeVisible()
  await expect(page.getByText("for medical enterprises", { exact: true })).toBeVisible()
  await expect(page.getByText("Built for enterprise")).toBeVisible()
})

test("marketing navigation exposes the enterprise pages", async ({ page }) => {
  await page.goto("/")

  await page.getByRole("link", { name: "The Acuity Method" }).first().click()
  await expect(page).toHaveURL(/\/method$/)
  await expect(
    page.getByRole("heading", { name: "Two capabilities make enterprise AI work." }),
  ).toBeVisible()
  await expect(page.getByText("Medical voice agents")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "A working operation creates measurable capacity." }),
  ).toBeVisible()

  await page.getByRole("link", { name: "Who We Are" }).first().click()
  await expect(page).toHaveURL(/\/who-we-are$/)
  await expect(page.getByText("Acuity began as a consulting relationship.")).toBeVisible()
})

test("work with us links land on the conversation", async ({ page }) => {
  await page.goto("/")

  await page.getByRole("link", { name: "Work with us" }).first().click()
  await expect(page).toHaveURL(/\/work-with-us#conversation$/)
  await expect(
    page.getByRole("heading", { name: "Tell us what needs to change." }),
  ).toBeVisible()
})

test("mobile homepage does not overflow horizontally", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/")

  const dimensions = await page.evaluate(() => {
    return {
      viewport: document.documentElement.clientWidth,
      content: document.documentElement.scrollWidth,
    }
  })

  expect(dimensions.content).toBeLessThanOrEqual(dimensions.viewport)
  await expect(page.getByRole("heading", { name: "Redesign patient access." })).toBeVisible()
})

test("public pages expose canonical metadata and browser identity assets", async ({
  page,
  request,
}) => {
  const publicPages = [
    { route: "/", canonical: "https://acuityhealth.io" },
    { route: "/method", canonical: "https://acuityhealth.io/method" },
    { route: "/who-we-are", canonical: "https://acuityhealth.io/who-we-are" },
    { route: "/work-with-us", canonical: "https://acuityhealth.io/work-with-us" },
  ]

  for (const { route, canonical } of publicPages) {
    await page.goto(route)
    await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", canonical)
    await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", "index, follow")
    await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
      "content",
      /\/opengraph-image/,
    )
  }

  await page.goto("/sign-in")
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute(
    "content",
    /noindex, nofollow/,
  )

  const robots = await request.get("/robots.txt")
  expect(robots.ok()).toBe(true)
  await expect(robots.text()).resolves.toContain(
    "Sitemap: https://acuityhealth.io/sitemap.xml",
  )

  const sitemap = await request.get("/sitemap.xml")
  expect(sitemap.ok()).toBe(true)
  const sitemapXml = await sitemap.text()
  for (const { route, canonical } of publicPages) {
    const sitemapUrl = route === "/" ? `${canonical}/` : canonical
    expect(sitemapXml).toContain(`<loc>${sitemapUrl}</loc>`)
  }

  for (const asset of [
    "/favicon.ico",
    "/icon.svg",
    "/apple-icon.png",
    "/manifest.webmanifest",
  ]) {
    const response = await request.get(asset)
    expect(response.ok()).toBe(true)
    expect((await response.body()).byteLength).toBeGreaterThan(0)
  }
})
