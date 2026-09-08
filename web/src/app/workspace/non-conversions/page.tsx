import type { Metadata } from "next"
import { NonConversionCallList } from "@/components/analytics/non-conversion-call-list"

export const metadata: Metadata = {
  title: "Non-converting calls",
  robots: { index: false, follow: false, noarchive: true, nocache: true },
}

export default function NonConversionsPage() {
  return <NonConversionCallList />
}
