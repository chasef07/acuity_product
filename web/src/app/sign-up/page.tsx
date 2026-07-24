import Link from "next/link"
import { LockKeyholeIcon } from "lucide-react"

import { AuthFrame } from "@/components/auth/auth-frame"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"

export default function PublicSignUpPage() {
  return (
    <AuthFrame
      eyebrow="Access controlled"
      title="Public sign-up is unavailable"
      description="Acuity Portal accounts begin with an email-bound Practice invitation."
    >
      <div className="flex flex-col gap-4">
        <Alert>
          <LockKeyholeIcon aria-hidden="true" />
          <AlertTitle>Invitation required</AlertTitle>
          <AlertDescription>
            Ask your Practice administrator for an invitation. Acuity will
            never send you a shared password.
          </AlertDescription>
        </Alert>
        <Button
          variant="outline"
          nativeButton={false}
          render={<Link href="/sign-in" />}
        >
          Return to sign in
        </Button>
      </div>
    </AuthFrame>
  )
}
