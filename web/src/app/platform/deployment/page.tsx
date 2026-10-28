import { DeploymentPageContent } from "@/components/marketing/platform-pages"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/platform/deployment",
  title: "Deployment: On-Site Rollout of AI Agents",
  description:
    "Acuity Health's founders go on-site, build the agent around your workflow, and stay through rollout until your team owns it.",
})

export default function DeploymentPage() {
  return <DeploymentPageContent />
}
