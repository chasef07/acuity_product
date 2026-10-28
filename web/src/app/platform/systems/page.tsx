import { SystemsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/platform/systems",
  title: "Acuity Systems: The Staff Workspace for AI Agents",
  description:
    "Acuity Systems gives practice staff one workspace for agent handoffs, patient calls and texts, call history, analytics, and the agent's knowledge base.",
})

export default function SystemsPage() {
  return <SystemsPageContent />
}
