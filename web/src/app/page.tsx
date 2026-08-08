import type { Metadata } from "next"
import { connection } from "next/server"

import { LandingPage } from "@/components/marketing/landing-page"
import { googleAuthEnabled } from "@/lib/auth-providers"

export const metadata: Metadata = {
  title: "Acuity Health | AI agents for patient management",
  description:
    "Acuity carries patient work across calls, texts, faxes, referrals, EHR updates, and spreadsheets through completion.",
}

export default async function Home() {
  await connection()
  return <LandingPage googleEnabled={googleAuthEnabled()} />
}
