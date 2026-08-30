import { expect, test } from "@playwright/test"

test("enterprise story leads from medical voice to the Acuity Health Method", async ({ page }) => {
  await page.goto("/")

  await expect(
    page.getByRole("heading", {
      name: "Redesign patient access with medical AI agents.",
    }),
  ).toBeVisible()
  await expect(page.getByText("AI agents", { exact: true })).toBeVisible()
  await expect(page.getByText("for patient access", { exact: true })).toBeVisible()
  await expect(page.getByText("Built for enterprise")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "Find where patient demand stops moving." }),
  ).toBeVisible()
  await expect(
    page.getByText(
      "Acuity answers calls, completes approved work in the systems your team already uses, and brings staff in when judgment or ownership is required. Then we stay until the new operating model works.",
    ),
  ).toBeVisible()

  const commercialPaths = page.locator("section").filter({
    has: page.getByRole("heading", {
      name: "Find where patient demand stops moving.",
    }),
  })
  for (const link of [
    "/advancedmd-ai-receptionist",
    "/ai-receptionist-for-ophthalmology",
    "/ai-receptionist-vs-medical-answering-service",
  ]) {
    await expect(commercialPaths.locator(`a[href="${link}"]`)).toBeVisible()
  }

  const proof = page.locator("section").filter({
    has: page.getByRole("heading", {
      name: "A deployed operation created measurable capacity.",
    }),
  })
  for (const metric of ["500+", "0", "500", "$100K+"]) {
    await expect(proof.getByText(metric, { exact: true })).toBeVisible()
  }
  await expect(
    proof.getByRole("link", { name: "Read the ophthalmology case study" }),
  ).toHaveAttribute("href", "/case-studies/ophthalmology-patient-access")
})

test("marketing navigation exposes the enterprise pages", async ({ page }) => {
  await page.goto("/")

  await page.getByRole("banner").getByRole("link", { name: "Our Method" }).click()
  await expect(page).toHaveURL(/\/method$/)
  await expect(
    page.getByRole("heading", { name: "Two capabilities make enterprise AI work." }),
  ).toBeVisible()
  await expect(page.getByText("Medical AI agents")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "A deployed operation created measurable capacity." }),
  ).toBeVisible()

  await page.getByRole("link", { name: "Who We Are" }).first().click()
  await expect(page).toHaveURL(/\/who-we-are$/)
  await expect(
    page.getByRole("heading", {
      name: "Acuity Health began close to the patient-access work.",
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
})

test("commercial pages connect search intent to accountable work", async ({ page }) => {
  const commercialPages = [
    {
      route: "/advancedmd-ai-receptionist",
      heading: "An AdvancedMD AI receptionist built to complete the work.",
      schemaType: "Service",
    },
    {
      route: "/ai-receptionist-for-ophthalmology",
      heading: "An AI receptionist built for ophthalmology patient access.",
      schemaType: "Service",
    },
    {
      route: "/ai-receptionist-vs-medical-answering-service",
      heading:
        "AI receptionist vs. medical answering service: choose by the work you need done.",
      schemaType: "WebPage",
    },
  ]

  for (const { route, heading, schemaType } of commercialPages) {
    await page.goto(route)
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible()
    await expect(page.getByText("Operational trace", { exact: true })).toBeVisible()
    await expect(page.getByRole("link", { name: "Map the workflow" })).toBeVisible()
    await expect(page.locator("h1")).toHaveCount(1)

    const structuredData = page.locator('script[type="application/ld+json"]')
    await expect(structuredData).toHaveCount(2)
    const pageSchema = JSON.parse((await structuredData.nth(1).textContent()) ?? "{}")
    expect(pageSchema["@type"]).toBe(schemaType)
    expect(pageSchema.url).toBe(`https://acuityhealth.io${route}`)
  }

  await page.goto("/advancedmd-ai-receptionist")
  await expect(
    page.getByRole("link", { name: "View the AdvancedMD listing" }),
  ).toHaveAttribute(
    "href",
    "https://www.advancedmd.com/integrations/marketplace/acuity-health/",
  )
  for (const stage of [
    "Inbound signal",
    "Practice rules",
    "AdvancedMD action",
    "Evidence + owner",
  ]) {
    await expect(page.getByText(stage, { exact: true })).toHaveCount(2)
  }
  await expect(
    page.getByText("Commit supported work", { exact: true }).last(),
  ).toBeVisible()

  await page.goto("/ai-receptionist-for-ophthalmology")
  await expect(page.getByText("Patient need", { exact: true })).toHaveCount(2)
  await expect(
    page.getByText("Practice-defined medical versus vision routing", {
      exact: true,
    }),
  ).toBeVisible()
  for (const metric of ["500+", "0", "500", "$100K+"]) {
    await expect(page.getByText(metric, { exact: true })).toBeVisible()
  }

  await page.goto("/ai-receptionist-vs-medical-answering-service")
  const fitQuestion = page.locator("details").filter({
    hasText: "Is an AI receptionist always better than a medical answering service?",
  })
  await fitQuestion.locator("summary").click()
  await expect(fitQuestion).toContainText(
    "A traditional service may be the better fit when human-only interaction or message capture is the actual requirement.",
  )
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
  await expect(
    page.getByRole("heading", {
      name: "Redesign patient access with medical AI agents.",
    }),
  ).toBeVisible()
})

test("commercial pages remain usable without document overflow on mobile", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })

  for (const route of [
    "/advancedmd-ai-receptionist",
    "/ai-receptionist-for-ophthalmology",
    "/ai-receptionist-vs-medical-answering-service",
  ]) {
    await page.goto(route)
    const dimensions = await page.evaluate(() => ({
      viewport: document.documentElement.clientWidth,
      content: document.documentElement.scrollWidth,
    }))

    expect(dimensions.content).toBeLessThanOrEqual(dimensions.viewport)
    await expect(page.locator("h1")).toBeVisible()
    await expect(page.getByRole("link", { name: "Map the workflow" })).toBeVisible()
  }

  await page.goto("/ai-receptionist-vs-medical-answering-service")
  const comparison = page.getByTestId("comparison-table")
  const tableDimensions = await comparison.evaluate((element) => ({
    viewport: element.clientWidth,
    content: element.scrollWidth,
  }))
  expect(tableDimensions.content).toBeGreaterThan(tableDimensions.viewport)
  await comparison.evaluate((element) => {
    element.scrollLeft = 120
  })
  expect(await comparison.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0)
})

test("trust, FAQ, and case-study pages publish bounded evidence", async ({ page }) => {
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
    {
      route: "/faq",
      heading: "Questions to answer before changing patient access.",
    },
    {
      route: "/case-studies/ophthalmology-patient-access",
      heading: "From roughly 200 dropped calls a month to zero reported.",
    },
  ]

  for (const { route, heading } of pages) {
    await page.goto(route)
    await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible()
    await expect(page.locator("h1")).toHaveCount(1)
  }

  await page.goto("/security")
  await expect(page.getByText("A public overview, not a compliance badge.")).toBeVisible()
  await expect(page.getByText(/not a third-party certification/)).toBeVisible()
  await expect(page.getByText("Data Buddies Solutions LLC", { exact: true })).toBeVisible()

  await page.goto("/faq")
  const faqSchemas = await page
    .locator('script[type="application/ld+json"]')
    .allTextContents()
  expect(faqSchemas.map((value) => JSON.parse(value)["@type"])).toContain("FAQPage")

  await page.goto("/case-studies/ophthalmology-patient-access")
  await expect(page.getByText("not independently audited", { exact: false })).toBeVisible()
  await expect(page.getByText("not a universal promise", { exact: false })).toBeVisible()
})

test("legacy SEO routes redirect only to equivalent current pages", async ({ request }) => {
  const redirects = [
    ["/about", "/who-we-are"],
    ["/specialties/ophthalmology", "/ai-receptionist-for-ophthalmology"],
    ["/ophthalmology-answering-service", "/ai-receptionist-for-ophthalmology"],
    ["/after-hours-answering-service-ophthalmology", "/ai-receptionist-for-ophthalmology"],
    [
      "/insights/best-ai-answering-service-ophthalmology",
      "/ai-receptionist-for-ophthalmology",
    ],
    [
      "/insights/ai-receptionist-vs-traditional-answering-service",
      "/ai-receptionist-vs-medical-answering-service",
    ],
    ["/partners/advancedmd", "/advancedmd-ai-receptionist"],
  ] as const

  for (const [source, destination] of redirects) {
    const response = await request.get(source, { maxRedirects: 0 })
    expect(response.status()).toBe(308)
    expect(response.headers().location).toBe(destination)
  }

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
    "/faq",
    "/case-studies/ophthalmology-patient-access",
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
      route: "/advancedmd-ai-receptionist",
      locator: page.getByRole("link", { name: "See how Acuity deploys" }),
    },
    {
      route: "/security",
      locator: page.getByRole("navigation", { name: "On this page" }).getByRole("link").first(),
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
      title: "AI Agents for Patient Access | Acuity Health",
      description:
        "Acuity Health deploys medical AI agents that answer calls, complete patient-access workflows, and bring staff in when judgment or ownership is required.",
    },
    {
      route: "/method",
      canonical: "https://acuityhealth.io/method",
      title: "The Acuity Health Method | Acuity Health",
      description:
        "How Acuity Health combines agentic system design with workflow transformation to deploy enterprise medical voice.",
    },
    {
      route: "/advancedmd-ai-receptionist",
      canonical: "https://acuityhealth.io/advancedmd-ai-receptionist",
      title: "AdvancedMD AI Receptionist for Practices | Acuity Health",
      description:
        "Acuity Health helps AdvancedMD practices answer calls, book supported appointments, apply practice rules, and route exceptions with evidence and ownership.",
    },
    {
      route: "/ai-receptionist-for-ophthalmology",
      canonical: "https://acuityhealth.io/ai-receptionist-for-ophthalmology",
      title: "AI Receptionist for Ophthalmology Practices | Acuity Health",
      description:
        "Acuity Health deploys an AI receptionist for ophthalmology that completes approved scheduling calls, follows practice rules, and routes exceptions to staff.",
    },
    {
      route: "/ai-receptionist-vs-medical-answering-service",
      canonical:
        "https://acuityhealth.io/ai-receptionist-vs-medical-answering-service",
      title: "AI Receptionist vs Answering Service | Acuity Health",
      description:
        "Compare AI receptionists and medical answering services across workflow completion, system integration, handoffs, oversight, and verified outcome evidence.",
    },
    {
      route: "/who-we-are",
      canonical: "https://acuityhealth.io/who-we-are",
      title: "Patient Access AI Company & Founders | Acuity Health",
      description:
        "Acuity Health is a founder-deployed medical voice company built from the consulting relationship required to transform patient access.",
    },
    {
      route: "/work-with-us",
      canonical: "https://acuityhealth.io/work-with-us",
      title: "Patient Access AI Working Session | Acuity Health",
      description:
        "Work with Acuity Health to baseline patient-access KPIs, redesign workflows, and test operational improvements before scaling.",
    },
    {
      route: "/case-studies/ophthalmology-patient-access",
      canonical: "https://acuityhealth.io/case-studies/ophthalmology-patient-access",
      title: "Ophthalmology Patient Access Case Study | Acuity Health",
      description:
        "See how a six-location eye-care group reports moving from roughly 200 monthly dropped calls to zero in an operating snapshot with Acuity.",
    },
    {
      route: "/faq",
      canonical: "https://acuityhealth.io/faq",
      title: "Medical AI Receptionist FAQ | Acuity Health",
      description:
        "Answers about Acuity Health AI agents, patient-access workflows, AdvancedMD scheduling, staff handoffs, implementation, security review, and measurement.",
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
