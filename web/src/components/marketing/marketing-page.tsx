import { EnterpriseHome } from "./enterprise-site"

export function MarketingPage({
  initiallyOpen = false,
}: {
  initiallyOpen?: boolean
}) {
  return <EnterpriseHome initiallyOpen={initiallyOpen} />
}
