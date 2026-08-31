import { WhoWeArePageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/who-we-are",
  title: "Patient Access AI Company & Founders",
  description:
    "Acuity Health is a founder-deployed medical voice company built from the consulting relationship required to transform patient access.",
})

export default function WhoWeArePage() {
  return <WhoWeArePageContent />
}
