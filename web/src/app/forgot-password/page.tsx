import { AuthFrame } from "@/components/auth/auth-frame"
import { ForgotPasswordForm } from "@/components/auth/recovery-forms"

export default function ForgotPasswordPage() {
  return (
    <AuthFrame
      eyebrow="Account recovery"
      title="Reset your password"
      description="Acuity will send a private recovery link if the verified email has an account."
    >
      <ForgotPasswordForm />
    </AuthFrame>
  )
}
