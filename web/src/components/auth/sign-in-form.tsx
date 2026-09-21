"use client"

import { useSearchParams } from "next/navigation"

import { Button } from "@/components/ui/button"
import { FieldError, FieldGroup } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"

export function SignInForm({ pending, error, onSignIn }: {
  pending: boolean
  error: string | null
  onSignIn: () => void
}) {
  const searchParams = useSearchParams()
  const message = error ?? (searchParams.has("error") && !pending
    ? "Google sign-in didn’t finish. Try again."
    : null)

  return (
    <FieldGroup>
      <Button
        type="button"
        size="lg"
        className="w-full rounded-full"
        disabled={pending}
        onClick={onSignIn}
      >
        {pending ? (
          <Spinner data-icon="inline-start" />
        ) : (
          <GoogleMark data-icon="inline-start" />
        )}
        Continue with Google
      </Button>
      {message && <FieldError>{message}</FieldError>}
    </FieldGroup>
  )
}

function GoogleMark(props: React.ComponentProps<"svg">) {
  return (
    <svg viewBox="0 0 18 18" aria-hidden="true" {...props}>
      <path
        fill="#4285F4"
        d="M17.64 9.205c0-.638-.057-1.252-.164-1.841H9v3.481h4.844a4.14 4.14 0 0 1-1.797 2.715v2.259h2.909c1.702-1.567 2.684-3.874 2.684-6.614Z"
      />
      <path
        fill="#34A853"
        d="M9 18c2.43 0 4.468-.806 5.956-2.181l-2.909-2.259c-.806.54-1.835.859-3.047.859-2.344 0-4.328-1.585-5.037-3.714H.956v2.332A9 9 0 0 0 9 18Z"
      />
      <path
        fill="#FBBC05"
        d="M3.963 10.705A5.41 5.41 0 0 1 3.682 9c0-.592.102-1.168.281-1.705V4.963H.956A9 9 0 0 0 0 9c0 1.452.347 2.827.956 4.037l3.007-2.332Z"
      />
      <path
        fill="#EA4335"
        d="M9 3.58c1.321 0 2.507.454 3.441 1.346l2.581-2.581C13.464.892 11.426 0 9 0A9 9 0 0 0 .956 4.963l3.007 2.332C4.672 5.166 6.656 3.58 9 3.58Z"
      />
    </svg>
  )
}
