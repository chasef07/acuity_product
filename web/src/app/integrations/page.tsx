import { IntegrationsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/integrations",
  title: "EHR, PMS & Insurance Eligibility Integrations",
  description:
    "Connect patient access with your EHR, PMS, and insurance eligibility workflows through Acuity Health’s AdvancedMD and Stedi partnerships.",
})

export default function IntegrationsPage() {
  return <IntegrationsPageContent />
}
