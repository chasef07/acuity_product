"use client"

import { useRouter, useSearchParams } from "next/navigation"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { FieldError, FieldGroup } from "@/components/ui/field"
import { Spinner } from "@/components/ui/spinner"
import { authClient } from "@/lib/auth-client"

export function SignInForm() {
  const router = useRouter()
  const searchParams = useSearchParams()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(
    searchParams.get("error") === "google"
      ? "Google sign-in didn’t finish. Try again."
      : null,
  )

  function nextDestination() {
    const requested = searchParams.get("next")
    return requested?.startsWith("/") ? requested : "/workspace"
  }

  async function signInWithGoogle() {
    const destination = nextDestination()
    setPending(true)
    setError(null)
    const result = await authClient.signIn.popup({
      provider: "google",
      callbackURL: destination,
      errorCallbackURL: `/sign-in?error=google&next=${encodeURIComponent(destination)}`,
    })
    if (result?.error) {
      setPending(false)
      setError(googleErrorMessage(result.error.code))
      return
    }
    setPending(false)
    router.replace(destination)
  }

  return (
    <FieldGroup>
      <Button
        type="button"
        size="lg"
        className="w-full rounded-full"
        disabled={pending}
        onClick={signInWithGoogle}
      >
        {pending ? (
          <Spinner data-icon="inline-start" />
        ) : (
          <GoogleMark data-icon="inline-start" />
        )}
        Continue with Google
      </Button>
      {error && <FieldError>{error}</FieldError>}
    </FieldGroup>
  )
}

function googleErrorMessage(code: string): string {
  if (code === "POPUP_BLOCKED") {
    return "Allow pop-ups for Acuity, then try again."
  }
  if (code === "POPUP_CLOSED") {
    return "Google sign-in was closed. Try again."
  }
  return "Google sign-in didn’t finish. Try again."
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
