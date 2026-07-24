import { Suspense } from "react"

import { AuthFrame } from "@/components/auth/auth-frame"
import { AcceptInvitation } from "@/components/auth/accept-invitation"
import { Skeleton } from "@/components/ui/skeleton"

export default function AcceptInvitationPage() {
  return (
    <AuthFrame
      eyebrow="Access activation"
      title="Opening your workspace"
      description="Acuity is resolving current Practice and Location authority."
    >
      <Suspense fallback={<Skeleton className="h-12 w-full" />}>
        <AcceptInvitation />
      </Suspense>
    </AuthFrame>
  )
}
