import type { Metadata } from "next"
import { connection } from "next/server"

import { MarketingPage } from "@/components/marketing/marketing-page"

export const metadata: Metadata = {
  title: "Portal Sign In",
  robots: {
    index: false,
    follow: false,
    noarchive: true,
    nocache: true,
  },
}

export default async function SignInPage() {
  await connection()
  return <MarketingPage initiallyOpen />
}
