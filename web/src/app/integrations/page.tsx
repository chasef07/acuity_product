import { IntegrationsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/integrations",
  title: "EHR & PMS Integrations",
  description:
    "Explore Acuity Health’s AdvancedMD partnership and medical AI integrations with Nextech, Athenahealth, ModMed, Compulink, and custom EHR & PMS platforms.",
})

export default function IntegrationsPage() {
  return <IntegrationsPageContent />
}
