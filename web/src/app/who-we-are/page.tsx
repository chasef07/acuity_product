import { WhoWeArePageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/who-we-are",
  title: "Patient Access AI Company & Founders",
  description:
    "Meet Acuity Health’s founders and Chief Medical Officer Michael Venincasa, MD, bringing operational and clinical expertise to medical AI.",
})

export default function WhoWeArePage() {
  return <WhoWeArePageContent />
}
