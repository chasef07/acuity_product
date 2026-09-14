import { StediPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/integrations/stedi",
  title: "Medical, Vision & Medicare Eligibility Checks | Stedi Partner",
  description:
    "Acuity Health partners with Stedi for quick, efficient medical, vision, and Medicare insurance eligibility checks within your patient-access workflow.",
})

export default function StediPage() {
  return <StediPageContent />
}
