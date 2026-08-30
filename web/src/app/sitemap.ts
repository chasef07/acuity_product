import type { MetadataRoute } from "next"

import { siteConfig } from "@/lib/site"

const publicRoutes = [
  { path: "/", changeFrequency: "weekly", priority: 1, lastModified: "2026-08-30" },
  { path: "/method", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-08-30" },
  {
    path: "/advancedmd-ai-receptionist",
    changeFrequency: "monthly",
    priority: 0.9,
    lastModified: "2026-08-30",
  },
  {
    path: "/ai-receptionist-for-ophthalmology",
    changeFrequency: "monthly",
    priority: 0.9,
    lastModified: "2026-08-30",
  },
  {
    path: "/ai-receptionist-vs-medical-answering-service",
    changeFrequency: "monthly",
    priority: 0.8,
    lastModified: "2026-08-30",
  },
  {
    path: "/case-studies/ophthalmology-patient-access",
    changeFrequency: "monthly",
    priority: 0.8,
    lastModified: "2026-08-30",
  },
  { path: "/faq", changeFrequency: "monthly", priority: 0.7, lastModified: "2026-08-30" },
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
  { path: "/who-we-are", changeFrequency: "monthly", priority: 0.7, lastModified: "2026-08-30" },
  { path: "/work-with-us", changeFrequency: "monthly", priority: 0.8, lastModified: "2026-08-30" },
] as const

export default function sitemap(): MetadataRoute.Sitemap {
  return publicRoutes.map(({ path, changeFrequency, priority, lastModified }) => ({
    url: new URL(path, siteConfig.url).toString(),
    changeFrequency,
    priority,
    lastModified,
  }))
}
