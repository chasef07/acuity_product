import { MethodPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/method",
  title: "The Acuity Method",
  description:
    "How Acuity combines agentic system design with workflow transformation to deploy enterprise medical voice.",
})

export default function MethodPage() {
  return <MethodPageContent />
}
