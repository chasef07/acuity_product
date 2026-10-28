import { StrategyPageContent } from "@/components/marketing/platform-pages"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/platform/strategy",
  title: "Strategy: Where Practice AI Pays Off First",
  description:
    "Start with one patient-access workflow, agree on the KPIs that define success, and expand only when the evidence holds.",
})

export default function StrategyPage() {
  return <StrategyPageContent />
}
