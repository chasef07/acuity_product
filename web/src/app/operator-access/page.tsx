import { AuthFrame } from "@/components/auth/auth-frame"
import { OperatorAccessForm } from "@/components/auth/operator-access-form"

export default function OperatorAccessPage() {
  return (
    <AuthFrame
      eyebrow="Platform Operator"
      title="Activate internal access"
      description="Only a pre-provisioned Acuity founder email can continue. Customer Practice membership is never created here."
    >
      <OperatorAccessForm />
    </AuthFrame>
  )
}
