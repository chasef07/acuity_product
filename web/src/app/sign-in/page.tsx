import { connection } from "next/server"

import { PortalHome } from "@/components/auth/portal-home"
import { googleAuthEnabled } from "@/lib/auth-providers"

export default async function SignInPage() {
  await connection()
  return <PortalHome googleEnabled={googleAuthEnabled()} initiallyOpen />
}
