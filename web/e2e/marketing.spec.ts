import { expect, test, type Page } from "@playwright/test"

async function expectPinnedGlassNavigation(page: Page, expectedTop: number) {
  const navigation = page.getByRole("banner")
  const glass = await navigation.evaluate((element) => {
    const styles = getComputedStyle(element)
    return {
      backdropFilter: styles.backdropFilter,
      position: styles.position,
      top: styles.top,
    }
  })

  expect(glass.position).toBe("sticky")
  expect(glass.top).toBe(`${expectedTop}px`)
  expect(glass.backdropFilter).toContain("blur(24px)")

  await page.evaluate(() => window.scrollTo(0, 700))
  expect((await navigation.boundingBox())?.y).toBe(expectedTop)
}

test("enterprise story leads from medical voice to the Acuity Health Method", async ({ page }) => {
  await page.goto("/")

  await expect(
    page.getByRole("heading", {
      name: "Redesign patient access with medical AI agents.",
    }),
  ).toBeVisible()
  await expect(page.getByText("AI agents", { exact: true })).toBeVisible()
  await expect(page.getByText("for medical enterprises", { exact: true })).toBeVisible()
  await expect(page.locator("canvas")).toHaveCSS("cursor", "auto")
  await expect(
    page.getByRole("heading", { name: "Two capabilities make enterprise AI work." }),
  ).toBeVisible()
  const methodVenn = page.getByTestId("method-venn")
  await expect(methodVenn).toContainText("Agentic")
  await expect(methodVenn).toContainText("system design")
  await expect(methodVenn).toContainText("Workflow")
  await expect(methodVenn).toContainText("transformation")
  await expect(page.getByText("Built for enterprise")).toHaveCount(0)
  await expect(
    page.getByRole("link", { name: "Work with us" }).first().locator("svg"),
  ).toHaveCount(0)
  await expect(
    page.getByRole("link", { name: "See the Acuity Health Method" }),
  ).toHaveCSS("border-bottom-width", "0px")
  await expect(
    page.getByText(
      "Acuity answers calls, completes approved work in the systems your team already uses, and brings staff in when judgment or ownership is required. Then we stay until the new operating model works.",
    ),
  ).toBeVisible()

  await expect(
    page.getByRole("heading", { name: "Find where patient demand stops moving." }),
  ).toHaveCount(0)
  await expect(
    page.getByRole("heading", {
      name: "A deployed operation created measurable capacity.",
    }),
  ).toHaveCount(0)
})

test("marketing navigation exposes the enterprise pages", async ({ page }) => {
  await page.goto("/")

  const primaryNavigation = page
    .getByRole("banner")
    .getByRole("navigation", { name: "Main navigation" })
  await expect(
    primaryNavigation.getByRole("link", { name: "Ophthalmology" }),
  ).toHaveCount(0)

  await page.getByRole("banner").getByRole("link", { name: "Our Method" }).click()
  await expect(page).toHaveURL(/\/method$/)
  await expect(
    page.getByRole("heading", { name: "Two capabilities make enterprise AI work." }),
  ).toBeVisible()
  await expect(page.getByText("Medical AI agents")).toBeVisible()
  await expect(
    page.getByRole("heading", { name: "A deployed operation created measurable capacity." }),
  ).toHaveCount(0)

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

test("marketing shell uses one navigation surface and an organized footer", async ({
  page,
}) => {
  await page.goto("/")

  const banner = page.getByRole("banner")
  const navigation = banner.getByRole("navigation", { name: "Main navigation" })
  const shell = await banner.evaluate((element) => {
    const styles = getComputedStyle(element)
    return {
      borderRadius: styles.borderRadius,
      top: styles.top,
      width: element.getBoundingClientRect().width,
    }
  })
  const navigationSurface = await navigation.evaluate((element) => {
    const styles = getComputedStyle(element)
    return {
      backgroundColor: styles.backgroundColor,
      borderTopWidth: styles.borderTopWidth,
      boxShadow: styles.boxShadow,
    }
  })

  expect(shell.borderRadius).toBe("0px")
  expect(shell.top).toBe("0px")
  expect(shell.width).toBe(await page.evaluate(() => document.documentElement.clientWidth))
  expect(navigationSurface.backgroundColor).toBe("rgba(0, 0, 0, 0)")
  expect(navigationSurface.borderTopWidth).toBe("0px")
  expect(navigationSurface.boxShadow).toBe("none")
  await expect(navigation.getByRole("link", { name: "Home" })).toHaveCSS(
    "box-shadow",
    "none",
  )

  const footer = page.getByRole("contentinfo")
  const footerNavigation = footer.getByRole("navigation", { name: "Footer navigation" })
  await expect(footerNavigation).toBeVisible()
  for (const group of ["Product", "Company", "Resources"]) {
    await expect(footer.getByRole("heading", { name: group })).toBeVisible()
  }
  for (const label of [
    "AdvancedMD integration",
    "Ophthalmology patient access",
    "Compare operating models",
    "Case study",
    "FAQ",
  ]) {
    await expect(footerNavigation.getByRole("link", { name: label })).toHaveCount(0)
  }
  await expect(footer.getByRole("separator")).toHaveCount(0)
  await expect(footer.getByText("Acuity Health", { exact: true }).last()).toBeVisible()
})

test("retired commercial routes and their legacy aliases return 404", async ({
  request,
}) => {
  for (const route of [
    "/advancedmd-ai-receptionist",
    "/ai-receptionist-for-ophthalmology",
    "/ai-receptionist-vs-medical-answering-service",
    "/case-studies/ophthalmology-patient-access",
    "/faq",
    "/specialties/ophthalmology",
    "/ophthalmology-answering-service",
    "/after-hours-answering-service-ophthalmology",
    "/insights/best-ai-answering-service-ophthalmology",
    "/insights/ai-receptionist-vs-traditional-answering-service",
    "/partners/advancedmd",
  ]) {
    expect((await request.get(route, { maxRedirects: 0 })).status()).toBe(404)
  }
})

test("work with us links land at the top with the pinned glass navigation visible", async ({
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

  await expect(page.getByRole("banner")).toHaveCSS("border-radius", "0px")
  await expectPinnedGlassNavigation(page, 0)
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
  await expectPinnedGlassNavigation(page, 0)
  await expect(
    page.getByRole("heading", {
      name: "Redesign patient access with medical AI agents.",
    }),
  ).toBeVisible()
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
  const redirects = [["/about", "/who-we-are"]] as const

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
