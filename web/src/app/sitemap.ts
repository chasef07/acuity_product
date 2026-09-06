import type { MetadataRoute } from "next"

import { siteConfig } from "@/lib/site"

const publicRoutes = [
  { path: "/", changeFrequency: "weekly", priority: 1, lastModified: "2026-09-06" },
  { path: "/method", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-08-30" },
  { path: "/integrations", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-09-06" },
  { path: "/integrations/advancedmd", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-09-06" },
  { path: "/security", changeFrequency: "monthly", priority: 0.6, lastModified: "2026-08-30" },
  {
    path: "/privacy-policy",
    changeFrequency: "yearly",
    priority: 0.4,
    lastModified: "2026-08-30",
  },
  {
    path: "/terms-of-service",
    changeFrequency: "yearly",
    priority: 0.4,
    lastModified: "2026-08-30",
  },
  { path: "/who-we-are", changeFrequency: "monthly", priority: 0.7, lastModified: "2026-09-06" },
  { path: "/work-with-us", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-09-06" },
] as const

export default function sitemap(): MetadataRoute.Sitemap {
  return publicRoutes.map(({ path, changeFrequency, priority, lastModified }) => ({
    url: new URL(path, siteConfig.url).toString(),
    changeFrequency,
    priority,
    lastModified,
    ...(path === "/who-we-are"
      ? { images: [`${siteConfig.url}/marketing/michael-venincasa-md.jpg`] }
      : {}),
  }))
}
