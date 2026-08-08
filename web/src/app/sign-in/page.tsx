import { connection } from "next/server"

import { LandingPage } from "@/components/marketing/landing-page"
import { googleAuthEnabled } from "@/lib/auth-providers"

export default async function SignInPage() {
  await connection()
  return <LandingPage googleEnabled={googleAuthEnabled()} initiallyOpen />
}
