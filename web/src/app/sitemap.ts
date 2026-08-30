import type { MetadataRoute } from "next"

import { siteConfig } from "@/lib/site"

const publicRoutes = [
  { path: "/", changeFrequency: "weekly", priority: 1 },
  { path: "/method", changeFrequency: "monthly", priority: 0.8 },
  { path: "/who-we-are", changeFrequency: "monthly", priority: 0.7 },
  { path: "/work-with-us", changeFrequency: "monthly", priority: 0.8 },
] as const

export default function sitemap(): MetadataRoute.Sitemap {
  return publicRoutes.map(({ path, changeFrequency, priority }) => ({
    url: new URL(path, siteConfig.url).toString(),
    changeFrequency,
    priority,
  }))
}
