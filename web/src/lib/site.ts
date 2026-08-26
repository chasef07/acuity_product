import type { Metadata } from "next"

export const siteConfig = {
  name: "Acuity Health",
  shortName: "Acuity Health",
  url: "https://acuityhealth.io",
  title: "Acuity Health | AI Agents for Medical Enterprises",
  description:
    "Acuity Health redesigns patient-access workflows, deploys medical AI agents across the systems you already use, and stays with your team until the new operating model works.",
} as const

export function createPublicPageMetadata({
  path,
  title,
  description,
}: {
  path: string
  title: string
  description: string
}): Metadata {
  return {
    title,
    description,
    alternates: {
      canonical: path,
    },
    openGraph: {
      title,
      description,
      url: path,
      siteName: siteConfig.name,
      locale: "en_US",
      type: "website",
      images: [
        {
          url: "/opengraph-image",
          width: 1200,
          height: 630,
          alt: "Acuity Health: AI agents for medical enterprises",
        },
      ],
    },
    twitter: {
      card: "summary_large_image",
      title,
      description,
      images: ["/opengraph-image"],
    },
  }
}
