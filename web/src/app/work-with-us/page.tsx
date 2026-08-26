import { WorkWithUsPageContent } from "@/components/marketing/enterprise-site"
import { createPublicPageMetadata } from "@/lib/site"

export const metadata = createPublicPageMetadata({
  path: "/work-with-us",
  title: "Work With Us",
  description:
    "Work with Acuity Health to redesign patient access, deploy medical voice, and make the new operating model succeed.",
})

export default function WorkWithUsPage() {
  return <WorkWithUsPageContent />
}
