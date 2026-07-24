import { AuthFrame } from "@/components/auth/auth-frame"
import { InvitationForm } from "@/components/auth/invitation-form"

export default function InvitationPage() {
  return (
    <AuthFrame
      eyebrow="Invitation"
      title="Create your account"
      description="Confirm the access offered to your verified email, then choose a private password."
    >
      <InvitationForm />
    </AuthFrame>
  )
}
