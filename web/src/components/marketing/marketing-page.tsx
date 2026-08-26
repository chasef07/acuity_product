import { SignInDialog } from "@/components/auth/sign-in-dialog"

import { EnterpriseHome } from "./enterprise-site"

export function MarketingPage({
  initiallyOpen = false,
}: {
  initiallyOpen?: boolean
}) {
  return (
    <SignInDialog initiallyOpen={initiallyOpen}>
      <EnterpriseHome />
    </SignInDialog>
  )
}
