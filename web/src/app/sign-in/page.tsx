import { Suspense } from "react"

import { AuthFrame } from "@/components/auth/auth-frame"
import { SignInForm } from "@/components/auth/sign-in-form"
import { Skeleton } from "@/components/ui/skeleton"
import { googleAuthEnabled } from "@/lib/auth-providers"

export default function SignInPage() {
  return (
    <AuthFrame
      eyebrow="Acuity Portal"
      title="Welcome back"
      description="Continue with Google or use the verified email and password you created for your invitation."
    >
      <Suspense fallback={<Skeleton className="h-48 w-full" />}>
        <SignInForm googleEnabled={googleAuthEnabled()} />
      </Suspense>
    </AuthFrame>
  )
}
