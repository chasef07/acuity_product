import { connection } from "next/server"

import { MarketingPage } from "@/components/marketing/marketing-page"

export default async function SignInPage() {
  await connection()
  return <MarketingPage initiallyOpen />
}
