import { IntegrationsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/integrations",
  title: "Partnerships & EHR Integrations",
  description:
    "Acuity Health’s AI receptionist connects to AdvancedMD, is available on the ModMed SynapSYS Marketplace, checks insurance eligibility through Stedi, and runs on LiveKit voice AI.",
})

export default function IntegrationsPage() {
  return <IntegrationsPageContent />
}
