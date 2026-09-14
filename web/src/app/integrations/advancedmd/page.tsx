import { AdvancedMDPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/integrations/advancedmd",
  title: "AI Agents for AdvancedMD",
  description:
    "Acuity Health is an AdvancedMD marketplace partner. Answer calls 24/7, schedule directly in AdvancedMD, and hand unresolved requests to staff with context.",
})

export default function AdvancedMDPage() {
  return <AdvancedMDPageContent />
}
