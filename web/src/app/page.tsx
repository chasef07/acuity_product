import { MarketingPage } from "@/components/marketing/marketing-page"
import { createPublicPageMetadata, siteConfig } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/",
  title: siteConfig.title,
  description: siteConfig.description,
})

export default function Home() {
  return <MarketingPage />
}
