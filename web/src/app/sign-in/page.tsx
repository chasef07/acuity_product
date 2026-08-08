import { connection } from "next/server"

import { MarketingPage } from "@/components/marketing/marketing-page"
import { googleAuthEnabled } from "@/lib/auth-providers"

export default async function SignInPage() {
  await connection()
  return <MarketingPage googleEnabled={googleAuthEnabled()} initiallyOpen />
}
