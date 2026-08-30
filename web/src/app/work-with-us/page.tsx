import { WorkWithUsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/work-with-us",
  title: "Patient Access AI Working Session",
  description:
    "Work with Acuity Health to baseline patient-access KPIs, redesign workflows, and test operational improvements before scaling.",
})

export default function WorkWithUsPage() {
  return <WorkWithUsPageContent />
}
