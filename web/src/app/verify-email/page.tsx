import { AuthFrame } from "@/components/auth/auth-frame"
import { VerifyEmailAction } from "@/components/auth/verify-email-action"

export default function VerifyEmailPage() {
  return (
    <AuthFrame
      eyebrow="Email verification"
      title="Verifying your email"
      description="Acuity is validating the one-time email credential."
    >
      <VerifyEmailAction />
    </AuthFrame>
  )
}
