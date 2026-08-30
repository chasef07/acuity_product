import { expect, test } from "@playwright/test"

test("enterprise story leads from medical voice to the Acuity Health Method", async ({ page }) => {
  await page.goto("/")

  await expect(
    page.getByRole("heading", { name: "Redesign patient access." }),
  ).toBeVisible()
  await expect(page.getByText("AI agents", { exact: true })).toBeVisible()
  await expect(page.getByText("for medical enterprises", { exact: true })).toBeVisible()
  await expect(page.getByText("Built for enterprise")).toBeVisible()
})

test("marketing navigation exposes the enterprise pages", async ({ page }) => {
  await page.goto("/")

  await page.getByRole("link", { name: "The Acuity Health Method" }).first().click()
  await expect(page).toHaveURL(/\/method$/)
  await expect(
    page.getByRole("heading", { name: "Two capabilities make enterprise AI work." }),
  ).toBeVisible()
  await expect(page.getByText("Medical AI agents")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "A working operation creates measurable capacity." }),
  ).toBeVisible()

  await page.getByRole("link", { name: "Who We Are" }).first().click()
  await expect(page).toHaveURL(/\/who-we-are$/)
  await expect(
    page.getByRole("heading", { name: "Acuity Health began as a consulting relationship." }),
  ).toBeVisible()
  await expect(
    page.getByRole("heading", {
      name: "Free medical practices from administrative overload so every patient can be treated like a VIP.",
    }),
  ).toBeVisible()
  await expect(
    page.getByText(
      "A future where AI runs the administration, humans elevate the care, and no patient falls through the cracks.",
    ),
  ).toBeVisible()
  await expect(page.getByText("How we build")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "Continuous improvement" }),
  ).toBeVisible()
  await expect(page.getByText("KAIZEN · 改善")).toBeVisible()
})

test("work with us links land at the top with the full navigation visible", async ({
  page,
}) => {
  await page.goto("/")

  await page
    .getByRole("banner")
    .getByRole("link", { name: "Work with us" })
    .click()
  await expect(page).toHaveURL(/\/work-with-us$/)
  await expect(
    page.getByRole("heading", {
      name: "Let’s redesign one patient-access workflow together.",
    }),
  ).toBeVisible()

  expect(await page.evaluate(() => window.scrollY)).toBe(0)
  const navigation = await page.getByRole("banner").boundingBox()
  expect(navigation?.y).toBe(0)
  expect(navigation?.height).toBe(76)
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
    {
      route: "/",
      canonical: "https://acuityhealth.io",
      title: "Acuity Health | AI Agents for Medical Enterprises",
      description:
        "Acuity Health redesigns patient-access workflows, deploys medical AI agents across the systems you already use, and stays with your team until the new operating model works.",
    },
    {
      route: "/method",
      canonical: "https://acuityhealth.io/method",
      title: "The Acuity Health Method | Acuity Health",
      description:
        "How Acuity Health combines agentic system design with workflow transformation to deploy enterprise medical voice.",
    },
    {
      route: "/who-we-are",
      canonical: "https://acuityhealth.io/who-we-are",
      title: "Who We Are | Acuity Health",
      description:
        "Acuity Health is a founder-deployed medical voice company built from the consulting relationship required to transform patient access.",
    },
    {
      route: "/work-with-us",
      canonical: "https://acuityhealth.io/work-with-us",
      title: "Work With Us | Acuity Health",
      description:
        "Work with Acuity Health to baseline patient-access KPIs, redesign workflows, and test operational improvements before scaling.",
    },
  ]

  for (const { route, canonical, title, description } of publicPages) {
    await page.goto(route)
    await expect(page).toHaveTitle(title)
    await expect(page.locator('meta[name="description"]')).toHaveAttribute(
      "content",
      description,
    )
    await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", canonical)
    await expect(page.locator('meta[name="robots"]')).toHaveAttribute("content", "index, follow")
    await expect(page.locator('meta[property="og:title"]')).toHaveAttribute("content", /.+/)
    await expect(page.locator('meta[property="og:description"]')).toHaveAttribute(
      "content",
      description,
    )
    await expect(page.locator('meta[property="og:image"]')).toHaveAttribute(
      "content",
      /\/opengraph-image/,
    )
    await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute(
      "content",
      "summary_large_image",
    )
    await expect(page.locator('link[rel="manifest"]')).toHaveAttribute(
      "href",
      "/manifest.webmanifest",
    )
    await expect(page.locator('link[rel="apple-touch-icon"]')).toHaveAttribute(
      "href",
      "/apple-touch-icon.png",
    )
  }

  await page.goto("/sign-in")
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute(
    "content",
    /noindex, nofollow/,
  )

  const missingPage = await page.goto("/definitely-not-a-public-page")
  expect(missingPage?.status()).toBe(404)
  const missingPageRobots = page.locator('meta[name="robots"]')
  await expect(missingPageRobots).toHaveCount(1)
  await expect(missingPageRobots).toHaveAttribute("content", "noindex")

  const robots = await request.get("/robots.txt")
  expect(robots.ok()).toBe(true)
  const robotsText = await robots.text()
  expect(robotsText).toContain("Allow: /")
  expect(robotsText).toContain("Disallow: /api/")
  expect(robotsText).toContain("Disallow: /workspace")
  expect(robotsText).toContain("Sitemap: https://acuityhealth.io/sitemap.xml")

  const sitemap = await request.get("/sitemap.xml")
  expect(sitemap.ok()).toBe(true)
  expect(sitemap.headers()["content-type"]).toContain("application/xml")
  const sitemapXml = await sitemap.text()
  expect(sitemapXml).toContain('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">')
  for (const { route, canonical } of publicPages) {
    const sitemapUrl = route === "/" ? `${canonical}/` : canonical
    expect(sitemapXml).toContain(`<loc>${sitemapUrl}</loc>`)
  }
  expect(sitemapXml).not.toContain("/sign-in")
  expect(sitemapXml).not.toContain("/workspace")

  for (const asset of [
    { route: "/favicon.ico", contentType: "image/x-icon" },
    { route: "/icon.svg", contentType: "image/svg+xml" },
    { route: "/favicon-32x32.png", contentType: "image/png" },
    { route: "/favicon-16x16.png", contentType: "image/png" },
    { route: "/apple-touch-icon.png", contentType: "image/png" },
    { route: "/manifest.webmanifest", contentType: "application/manifest+json" },
    { route: "/opengraph-image", contentType: "image/png" },
  ]) {
    const response = await request.get(asset.route)
    expect(response.ok()).toBe(true)
    expect(response.headers()["content-type"]).toContain(asset.contentType)
    expect((await response.body()).byteLength).toBeGreaterThan(0)
  }

  const manifest = await request.get("/manifest.webmanifest")
  const manifestJson = await manifest.json()
  expect(manifestJson.icons).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ src: "/icon-192x192.png", sizes: "192x192" }),
      expect.objectContaining({ src: "/icon-512x512.png", sizes: "512x512" }),
      expect.objectContaining({ src: "/icon-maskable-512x512.png", purpose: "maskable" }),
    ]),
  )

  await page.goto("/")
  const structuredData = await page
    .locator('script[type="application/ld+json"]')
    .textContent()
  expect(structuredData).not.toBeNull()
  const schema = JSON.parse(structuredData ?? "{}")
  expect(schema["@context"]).toBe("https://schema.org")
  expect(schema["@graph"]).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        "@type": "Organization",
        name: "Acuity Health",
        url: "https://acuityhealth.io",
      }),
      expect.objectContaining({
        "@type": "WebSite",
        name: "Acuity Health",
        url: "https://acuityhealth.io",
      }),
    ]),
  )
})

test("working-session form submits to Formspree and confirms in place", async ({ page }) => {
  let submittedPayload = ""
  await page.route("https://formspree.io/f/xgaevpbr", async (route) => {
    const request = route.request()
    submittedPayload = request.postData() ?? ""
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ok: true }),
    })
  })

  await page.goto("/work-with-us")

  await page.getByLabel("Your name").fill("Taylor Example")
  await page.getByLabel("Work email").fill("taylor@example.com")
  await page.getByLabel("Role").fill("Practice administrator")
  await page.getByLabel("Number of locations").fill("4")
  await page.getByLabel("Practice").fill("Example Medical Group")
  await page
    .getByLabel("Which patient-access workflow should we focus on?")
    .fill("New-patient scheduling")
  await page
    .getByLabel("Which KPIs or operational impact should we improve?")
    .fill("Time to appointment and call abandonment")

  await page.getByRole("button", { name: "Request a working session" }).click()
  await expect(
    page.getByText("Thanks. We’ll review the workflow and follow up shortly."),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/work-with-us$/)
  for (const value of [
    "Taylor Example",
    "taylor@example.com",
    "Practice administrator",
    "4",
    "Example Medical Group",
    "New-patient scheduling",
    "Time to appointment and call abandonment",
  ]) {
    expect(submittedPayload).toContain(value)
  }
})
