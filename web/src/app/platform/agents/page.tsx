import { AgentsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/platform/agents",
  title: "Acuity Agents: Voice AI for Medical Practices",
  description:
    "Acuity Agents answer patient calls, book in AdvancedMD, confirm accepted insurance, and hand unresolved requests to staff with context.",
})

export default function AgentsPage() {
  return <AgentsPageContent />
}
