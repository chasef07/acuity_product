import { expect, test, type Page } from "@playwright/test"

async function expectPinnedNavigation(page: Page) {
  await page.evaluate(() => window.scrollTo(0, 700))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0)
  await expect.poll(async () => (await page.getByRole("banner").boundingBox())?.y).toBe(0)
}

test("home leads with the hero and no longer carries the method sections", async ({ page }) => {
  await page.goto("/")

  await expect(
    page.getByRole("heading", {
      name: "Applied AI for medical enterprises.",
    }),
  ).toBeVisible()
  await expect(page.getByTestId("strategy-capabilities")).toHaveCount(0)
  await expect(
    page.getByText(
      "Acuity is the infrastructure platform and partner for medical practices to deploy, measure, and continuously improve AI agents they own, starting with the phone.",
    ),
  ).toBeVisible()
})

test("marketing navigation exposes the enterprise pages", async ({ page }) => {
  await page.goto("/")

  const primaryNavigation = page
    .getByRole("banner")
    .getByRole("navigation", { name: "Main navigation" })
  await expect(primaryNavigation).toHaveCount(1)

  const banner = page.getByRole("banner")
  await banner.getByRole("button", { name: "Platform" }).click()
  await expect(banner.getByRole("link", { name: /^Acuity Agents/ })).toBeVisible()
  await expect(banner.getByRole("link", { name: /^Acuity Systems/ })).toBeVisible()
  await expect(banner.getByRole("link", { name: /^Deployment/ })).toBeVisible()
  await banner.getByRole("link", { name: /^Strategy/ }).click()
  await expect(page).toHaveURL(/\/platform\/strategy$/)
  await expect(page.getByRole("heading", { name: "Two capabilities make it work" })).toBeVisible()
  await expect(page.getByText("Data security + HIPAA safeguards")).toBeVisible()
  const methodCapabilities = page.getByTestId("strategy-capabilities")
  await expect(methodCapabilities).toContainText("Agentic system design")
  await expect(methodCapabilities).toContainText("Workflow transformation")

  await banner.getByRole("button", { name: "Integrations" }).click()
  await banner.getByRole("link", { name: /^All integrations/ }).click()
  await expect(page).toHaveURL(/\/integrations$/)
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Integrations and partners built around the systems your practice already uses.")

  await banner.getByRole("button", { name: "Company" }).click()
  await banner.getByRole("link", { name: /^Who We Are/ }).click()
  await expect(page).toHaveURL(/\/who-we-are$/)
  await expect(
    page.getByRole("heading", {
      name: "From consulting to Acuity.",
    }),
  ).toBeVisible()
  await expect(page.getByRole("heading", { name: "Kyle Shechtman" })).toBeVisible()
  await expect(page.getByRole("heading", { name: "Chase Fagen" })).toBeVisible()
  await expect(
    page.getByRole("link", { name: "LinkedIn" }).filter({
      has: page.locator('svg'),
    }),
  ).toHaveCount(2)
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

  const footer = page.getByRole("contentinfo")
  await expect(footer.getByRole("navigation", { name: "Footer navigation" })).toBeVisible()
  for (const group of ["Platform", "Company", "Social", "Legal"]) {
    await expect(footer.getByRole("heading", { name: group })).toBeVisible()
  }
})

test("platform pages describe the agents and the staff systems", async ({ page }) => {
  await page.goto("/platform/agents")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("AI agents for the patient phone line.")
  for (const name of ["Answer the call", "Book in your system", "Hand off with context", "Your insurance list"]) {
    await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()
  }

  await page.goto("/platform/systems")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("The staff workspace behind every agent.")
  for (const name of ["Task inbox", "Calls and texts", "Call history", "Analytics", "Knowledge base"]) {
    await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()
  }

  await page.goto("/platform/deployment")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Deployment is where practice AI succeeds or fails.")
  await page.getByRole("tab", { name: "Phase 03" }).click()
  await expect(page.getByRole("tabpanel")).toContainText("Test it in real operations.")
  await expect(page.getByRole("link", { name: /^Strategy/ }).last()).toHaveAttribute("href", "/platform/strategy")

  await page.goto("/platform/strategy")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Start with one workflow. Prove it. Then scale.")
  await expect(page.getByRole("link", { name: "Build with us" }).last()).toHaveAttribute("href", "/work-with-us")

  const banner = page.getByRole("banner")
  await expect(banner.getByRole("link", { name: "Blog", exact: true })).toHaveAttribute("href", "/blog")
  await banner.getByRole("button", { name: "Platform" }).hover()
  await expect(banner.getByRole("link", { name: /^Acuity Agents/ })).toBeVisible()
  await expect(banner.getByRole("link", { name: /LiveKit case study/ })).toBeVisible()
  await banner.getByRole("button", { name: "Company" }).hover()
  await expect(banner.getByRole("link", { name: /^Who We Are/ })).toBeVisible()
  await expect(banner.getByRole("link", { name: /^Acuity Agents/ })).toBeHidden()
  await page.keyboard.press("Escape")
  await expect(banner.getByRole("link", { name: /^Who We Are/ })).toBeHidden()
})

test("home shows case-study cards that page sideways", async ({ page }) => {
  await page.goto("/")
  const stories = page.getByRole("region", { name: "Solving the problems that move patient access." })
  const cards = stories.getByRole("link")
  await expect(cards).toHaveCount(5)
  await expect(cards.first()).toContainText("11 patients on the phone at once")
  await expect(cards.first()).toHaveAttribute("href", "/blog/abita-eye-group")
  await expect(stories.getByRole("link", { name: /LiveKit/ })).toHaveAttribute(
    "href",
    "https://livekit.com/blog/how-acuity-health-transforms-patient-access-with-livekit-voice-agents",
  )
  await expect(stories.getByRole("link", { name: /AdvancedMD/ })).toHaveAttribute("href", "/integrations/advancedmd")

  const previous = stories.getByRole("button", { name: "Previous stories" })
  const next = stories.getByRole("button", { name: "Next stories" })
  await expect(previous).toBeDisabled()
  await next.click()
  await expect(previous).toBeEnabled()
  await expect.poll(() => stories.getByRole("list").evaluate((list) => list.scrollLeft)).toBeGreaterThan(0)
})

test("hero copy pins to the left edge on wide screens", async ({ page }) => {
  for (const [width, maxLeft] of [[1440, 64], [1920, 96]] as const) {
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/")
    const heading = await page.getByRole("heading", { level: 1 }).boundingBox()
    const brand = await page.getByRole("banner").getByRole("link", { name: "Acuity Health home" }).boundingBox()
    expect(heading?.x).toBeLessThanOrEqual(maxLeft)
    expect(Math.abs((heading?.x ?? 0) - (brand?.x ?? 0))).toBeLessThanOrEqual(1)
  }
})

test("mobile menu opens as a dropdown with grouped links", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/")

  const banner = page.getByRole("banner")
  await expect(banner.getByRole("navigation", { name: "Main navigation" })).toBeHidden()
  const menuButton = banner.getByRole("button", { name: "Open menu" })
  await menuButton.click()
  const menu = banner.getByRole("navigation", { name: "Mobile navigation" })
  await expect(menu).toBeVisible()
  for (const group of ["Platform", "Integrations", "Company"]) {
    await expect(menu.getByRole("button", { name: group })).toBeVisible()
  }
  await expect(menu.getByRole("link", { name: "Blog", exact: true })).toBeVisible()
  await expect(menu.getByRole("link", { name: "Build with us", exact: true })).toBeVisible()

  await menu.getByRole("button", { name: "Platform" }).click()
  await menu.getByRole("link", { name: /^Acuity Systems/ }).click()
  await expect(page).toHaveURL(/\/platform\/systems$/)
  await expect(menu).toBeHidden()

  await banner.getByRole("button", { name: "Open menu" }).click()
  await page.keyboard.press("Escape")
  await expect(menu).toBeHidden()
  await expect(banner.getByRole("button", { name: "Open menu" })).toBeFocused()
})

test("blog index features the latest post above a cover grid", async ({ page }) => {
  await page.goto("/blog")
  await expect(page.getByRole("heading", { level: 1, name: "All posts" })).toBeVisible()
  await expect(page.getByRole("link", { name: /Read the article/ })).toHaveAttribute("href", "/blog/abita-eye-group")
  await expect(page.getByText("Stories, announcements, and product updates.")).toBeVisible()
  const posts = page.getByRole("main").getByRole("listitem")
  await expect(posts).toHaveCount(4)
  await page.getByRole("button", { name: "Customer stories" }).click()
  await expect(posts).toHaveCount(2)
  await expect(posts.getByRole("link").first()).toHaveAttribute("href", "/blog/abita-eye-group")
  await expect(posts.getByRole("link").last()).toHaveAttribute(
    "href",
    "https://livekit.com/blog/how-acuity-health-transforms-patient-access-with-livekit-voice-agents",
  )
})

test("retired commercial routes and their legacy aliases return 404", async ({
  request,
}) => {
  for (const route of [
    "/ai-receptionist-for-ophthalmology",
    "/ai-receptionist-vs-medical-answering-service",
    "/case-studies/ophthalmology-patient-access",
    "/faq",
    "/specialties/ophthalmology",
    "/ophthalmology-answering-service",
    "/after-hours-answering-service-ophthalmology",
    "/insights/best-ai-answering-service-ophthalmology",
    "/insights/ai-receptionist-vs-traditional-answering-service",
  ]) {
    expect((await request.get(route, { maxRedirects: 0 })).status()).toBe(404)
  }
})

test("work with us links land at the top with navigation pinned while scrolling", async ({
  page,
}) => {
  await page.goto("/")

  await page
    .getByRole("banner")
    .getByRole("link", { name: "Build with us", exact: true })
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

  await expectPinnedNavigation(page)
})

test("trust and legal pages publish bounded evidence", async ({ page }) => {
  const pages = [
    {
      route: "/security",
      heading: "Security, privacy, and HIPAA at Acuity Health.",
    },
    {
      route: "/privacy-policy",
      heading: "How Acuity Health handles information.",
    },
    {
      route: "/terms-of-service",
      heading: "The terms for using Acuity Health.",
    },
  ]

  for (const { route, heading } of pages) {
    await page.goto(route)
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible()
    await expect(page.locator("h1")).toHaveCount(1)
  }

  for (const route of ["/security", "/privacy-policy", "/terms-of-service"]) {
    await page.goto(route)
    await expect(page.getByLabel("Document details")).toHaveCount(0)
    await expect(page.getByRole("navigation", { name: "On this page" })).toHaveCount(0)
    await expect(page.getByText("Last updated", { exact: false })).toBeVisible()
  }

  await page.goto("/security")
  await expect(page.getByText("A public overview, not a compliance badge.")).toBeVisible()
  await expect(page.getByText(/not a third-party certification/)).toBeVisible()
  await expect(page.getByText(/Data Buddies Solutions LLC d\/b\/a Acuity Health/)).toBeVisible()
})

test("legacy SEO routes redirect only to equivalent current pages", async ({ request }) => {
  const redirects = [
    ["/about", "/who-we-are"],
    ["/advancedmd-ai-receptionist", "/integrations/advancedmd"],
    ["/partners/advancedmd", "/integrations/advancedmd"],
    ["/partners", "/integrations"],
    ["/partnerships", "/integrations"],
    ["/method", "/platform/strategy"],
  ] as const

  for (const [source, destination] of redirects) {
    const response = await request.get(source, { maxRedirects: 0 })
    expect(response.status()).toBe(308)
    expect(response.headers().location).toBe(destination)
    expect((await request.get(destination)).status()).toBe(200)
  }

  const campaignRedirect = await request.get(
    "/advancedmd-ai-receptionist?utm_source=marketplace",
    { maxRedirects: 0 },
  )
  expect(campaignRedirect.status()).toBe(308)
  expect(campaignRedirect.headers().location).toBe(
    "/integrations/advancedmd?utm_source=marketplace",
  )

  for (const route of [
    "/insights",
    "/press",
    "/blog/database-is-your-brain",
    "/insights/how-ai-can-improve-front-desk-efficiency",
    "/insights/hidden-cost-of-missed-calls-ophthalmology",
    "/insights/after-hours-call-capture-ophthalmology",
    "/insights/ai-receptionists-first-layer-of-triage-eye-care",
  ]) {
    expect((await request.get(route, { maxRedirects: 0 })).status()).toBe(404)
  }
})

test("public responses include conservative browser security headers", async ({ request }) => {
  const response = await request.get("/")
  expect(response.headers()["strict-transport-security"]).toBe("max-age=31536000")
  expect(response.headers()["x-content-type-options"]).toBe("nosniff")
  expect(response.headers()["referrer-policy"]).toBe("strict-origin-when-cross-origin")
  expect(response.headers()["permissions-policy"]).toBe(
    "camera=(), geolocation=(), payment=()",
  )
})

test("new public pages and navigation remain usable on mobile", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })

  for (const route of [
    "/",
    "/integrations",
    "/integrations/stedi",
    "/security",
    "/privacy-policy",
    "/terms-of-service",
  ]) {
    await page.goto(route)
    const dimensions = await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      content: document.documentElement.scrollWidth,
    }))
    expect(dimensions.content).toBeLessThanOrEqual(dimensions.viewport)
  }

  await page.goto("/")
  const linkHeights = await page.locator("header a:visible, footer a:visible").evaluateAll(
    (links) =>
      links.map((link) => {
        const bounds = link.getBoundingClientRect()
        return { height: bounds.height, width: bounds.width }
      }),
  )
  expect(linkHeights.length).toBeGreaterThan(0)
  expect(
    linkHeights.every(({ height, width }) => height >= 44 && width >= 44),
  ).toBe(true)

  for (const target of [
    {
      route: "/security",
      locator: page.getByRole("link", { name: "chase@acuityhealth.io" }),
    },
    {
      route: "/work-with-us",
      locator: page.locator("#conversation").getByRole("link", { name: "Privacy Policy" }),
    },
  ]) {
    await page.goto(target.route)
    const bounds = await target.locator.boundingBox()
    expect(bounds?.height).toBeGreaterThanOrEqual(44)
    expect(bounds?.width).toBeGreaterThanOrEqual(44)
  }
})

test("public pages expose canonical metadata and browser identity assets", async ({
  page,
  request,
}) => {
  const publicPages = [
    {
      route: "/",
      canonical: "https://acuityhealth.io",
      title: "Applied AI for Medical Enterprises | Acuity Health",
      description:
        "Acuity is the infrastructure platform and partner for medical practices to deploy, measure, and continuously improve AI agents they own, starting with the phone.",
    },
    {
      route: "/integrations",
      canonical: "https://acuityhealth.io/integrations",
      title: "Partnerships & EHR Integrations | Acuity Health",
      description:
        "Acuity Health’s AI receptionist connects to AdvancedMD, is available on the ModMed SynapSYS Marketplace, checks insurance eligibility through Stedi, and runs on LiveKit voice AI.",
    },
    {
      route: "/integrations/stedi",
      canonical: "https://acuityhealth.io/integrations/stedi",
      title: "Medical, Vision & Medicare Eligibility Checks | Stedi Partner | Acuity Health",
      description: "Acuity Health partners with Stedi for quick, efficient medical, vision, and Medicare insurance eligibility checks within your patient-access workflow.",
    },
    {
      route: "/integrations/advancedmd",
      canonical: "https://acuityhealth.io/integrations/advancedmd",
      title: "AI Agents for AdvancedMD | Acuity Health",
      description:
        "Acuity Health is an AdvancedMD marketplace partner. Answer calls 24/7, schedule directly in AdvancedMD, and hand unresolved requests to staff with context.",
    },
    {
      route: "/platform/agents",
      canonical: "https://acuityhealth.io/platform/agents",
      title: "Acuity Agents: Voice AI for Medical Practices | Acuity Health",
      description:
        "Acuity Agents answer patient calls, book in AdvancedMD, confirm accepted insurance, and hand unresolved requests to staff with context.",
    },
    {
      route: "/platform/systems",
      canonical: "https://acuityhealth.io/platform/systems",
      title: "Acuity Systems: The Staff Workspace for AI Agents | Acuity Health",
      description:
        "Acuity Systems gives practice staff one workspace for agent handoffs, patient calls and texts, call history, analytics, and the agent's knowledge base.",
    },
    {
      route: "/platform/deployment",
      canonical: "https://acuityhealth.io/platform/deployment",
      title: "Deployment: On-Site Rollout of AI Agents | Acuity Health",
      description:
        "Acuity Health's founders go on-site, build the agent around your workflow, and stay through rollout until your team owns it.",
    },
    {
      route: "/platform/strategy",
      canonical: "https://acuityhealth.io/platform/strategy",
      title: "Strategy: Where Practice AI Pays Off First | Acuity Health",
      description:
        "Start with one patient-access workflow, agree on the KPIs that define success, and expand only when the evidence holds.",
    },
    {
      route: "/who-we-are",
      canonical: "https://acuityhealth.io/who-we-are",
      title: "Patient Access AI Company & Founders | Acuity Health",
      description:
        "Meet Acuity Health’s founders and Chief Medical Officer Michael Venincasa, MD, bringing operational and clinical expertise to medical AI.",
    },
    {
      route: "/work-with-us",
      canonical: "https://acuityhealth.io/work-with-us",
      title: "Patient Access AI Working Session | Acuity Health",
      description:
        "Work with Acuity Health to baseline patient-access KPIs, redesign workflows, and test operational improvements before scaling.",
    },
    {
      route: "/security",
      canonical: "https://acuityhealth.io/security",
      title: "Security, Privacy & HIPAA | Acuity Health",
      description:
        "Review Acuity Health's public security, privacy, and HIPAA posture, including safeguards, service boundaries, BAAs, and shared responsibilities.",
    },
    {
      route: "/privacy-policy",
      canonical: "https://acuityhealth.io/privacy-policy",
      title: "Privacy Policy | Acuity Health",
      description:
        "Read how Data Buddies Solutions LLC, doing business as Acuity Health, collects, uses, protects, and shares information through its website and services.",
    },
    {
      route: "/terms-of-service",
      canonical: "https://acuityhealth.io/terms-of-service",
      title: "Terms of Service | Acuity Health",
      description:
        "Read the terms governing use of the Acuity Health website and general services offered by Data Buddies Solutions LLC.",
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
    await expect(page.locator('meta[name="twitter:image"]')).toHaveAttribute(
      "content",
      /\/opengraph-image/,
    )
    await expect(page.locator('meta[name="twitter:image:alt"]')).toHaveAttribute(
      "content",
      "Acuity Health: Applied AI for medical enterprises",
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
  expect(sitemapXml).toContain('<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"')
  for (const { route, canonical } of publicPages) {
    const sitemapUrl = route === "/" ? `${canonical}/` : canonical
    expect(sitemapXml).toContain(`<loc>${sitemapUrl}</loc>`)
  }
  expect(sitemapXml).toContain("<lastmod>2026-08-30</lastmod>")
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
        legalName: "Data Buddies Solutions LLC",
        url: "https://acuityhealth.io",
        sameAs: expect.arrayContaining([
          "https://www.linkedin.com/company/acuityhealth/",
          "https://www.advancedmd.com/integrations/marketplace/acuity-health/",
        ]),
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

  await expect(page.getByText(/may use this information to respond/)).toBeVisible()
  await expect(
    page.locator("#conversation").getByRole("link", { name: "Privacy Policy" }),
  ).toHaveAttribute("href", "/privacy-policy")

  await page.getByLabel("Your name").fill("Taylor Example")
  await page.getByLabel("Work email").fill("taylor@example.com")
  await page.getByLabel("Role").fill("Practice administrator")
  await page.getByLabel("Number of locations").fill("4")
  await page.getByLabel("Practice").fill("Example Medical Group")
  await page.getByLabel("EMR / PM system").fill("AdvancedMD")
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
    "AdvancedMD",
    "New-patient scheduling",
    "Time to appointment and call abandonment",
  ]) {
    expect(submittedPayload).toContain(value)
  }
  expect(submittedPayload).toContain('name="emrPmSystem"')
})


test("integrations lead to Stedi eligibility details and the header contact link", async ({ page }) => {
  await page.goto("/integrations")
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Integrations and partners built around the systems your practice already uses.")
  await expect(page.getByRole("link", { name: "View Acuity on Stedi" })).toHaveAttribute("href", "https://www.stedi.com/platform-partners#acuity-health")
  await expect(page.getByRole("link", { name: "View our ModMed SynapSYS Marketplace listing" })).toHaveAttribute("href", "https://modmed.my.site.com/synapsysmarketplace/s/partner-app/a9YUW000000jfGj2AI/data-buddies-solutions-llc")
  await expect(page.getByRole("link", { name: "Read how Acuity Health transforms patient access with LiveKit" })).toHaveAttribute("href", "https://livekit.com/blog/how-acuity-health-transforms-patient-access-with-livekit-voice-agents")
  await expect(
    page.getByText("We scope new EHR and PM connections with each practice, including Nextech, athenahealth and Compulink."),
  ).toBeVisible()
  await expect(page.getByText("We also connect with these platforms", { exact: false })).toHaveCount(0)
  await page.getByRole("link", { name: "Explore insurance eligibility" }).click()
  await expect(page).toHaveURL(/\/integrations\/stedi$/)
  for (const name of ["Medical eligibility", "Vision eligibility", "Medicare eligibility"]) {
    await expect(page.getByRole("heading", { name, exact: true })).toBeVisible()
  }
  await expect(page.getByText("No. An eligibility response", { exact: false })).toBeVisible()
  await page.getByRole("banner").getByRole("link", { name: "Build with us", exact: true }).click()
  await expect(page).toHaveURL(/\/work-with-us$/)
})
