import { Suspense } from "react"

import { AuthFrame } from "@/components/auth/auth-frame"
import { ResetPasswordForm } from "@/components/auth/recovery-forms"
import { Skeleton } from "@/components/ui/skeleton"

export default function ResetPasswordPage() {
  return (
    <AuthFrame
      eyebrow="Account recovery"
      title="Choose a new password"
      description="Your new password remains private to you."
    >
      <Suspense fallback={<Skeleton className="h-44 w-full" />}>
        <ResetPasswordForm />
      </Suspense>
    </AuthFrame>
  )
}
