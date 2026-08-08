"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useState } from "react"
import { LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { authClient } from "@/lib/auth-client"

export function SignInForm({ googleEnabled }: { googleEnabled: boolean }) {
  const router = useRouter()
  const searchParams = useSearchParams()
  const [emailExpanded, setEmailExpanded] = useState(!googleEnabled)
  const [pending, setPending] = useState<"google" | "email" | null>(null)
  const [error, setError] = useState<{
    source: "google" | "email"
    message: string
  } | null>(
    searchParams.get("error") === "google"
      ? {
          source: "google",
          message: "Google sign-in didn’t finish. Try again or use email.",
        }
      : null,
  )

  function nextDestination() {
    const requested = searchParams.get("next")
    return requested?.startsWith("/") ? requested : "/workspace"
  }

  async function signInWithGoogle() {
    const destination = nextDestination()
    setPending("google")
    setError(null)
    const result = await authClient.signIn.popup({
      provider: "google",
      callbackURL: destination,
      errorCallbackURL: `/sign-in?error=google&next=${encodeURIComponent(destination)}`,
    })
    if (result?.error) {
      setPending(null)
      setError({
        source: "google",
        message: googleErrorMessage(result.error.code),
      })
      return
    }
    setPending(null)
    router.replace(destination)
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending("email")
    setError(null)
    const data = new FormData(event.currentTarget)
    const result = await authClient.signIn.email({
      email: String(data.get("email") ?? ""),
      password: String(data.get("password") ?? ""),
      rememberMe: true,
    })
    setPending(null)
    if (result.error) {
      setError({
        source: "email",
        message: "Email or password didn’t match. Try again or reset it.",
      })
      return
    }
    router.replace(nextDestination())
  }

  return (
    <form onSubmit={submit}>
      <FieldGroup>
        {searchParams.get("verified") === "1" && (
          <Alert>
            <AlertTitle>Email verified</AlertTitle>
            <AlertDescription>
              Sign in to activate your Acuity invitation.
            </AlertDescription>
          </Alert>
        )}
        {googleEnabled && (
          <>
            <Button
              type="button"
              size="lg"
              className="w-full rounded-full"
              disabled={pending !== null}
              onClick={signInWithGoogle}
            >
              {pending === "google" ? (
                <LoaderCircleIcon
                  data-icon="inline-start"
                  className="motion-safe:animate-spin"
                />
              ) : (
                <GoogleMark data-icon="inline-start" />
              )}
              Continue with Google
            </Button>
            <Separator />
            <Button
              type="button"
              variant="ghost"
              className="w-full text-muted-foreground"
              aria-controls="email-sign-in-fields"
              aria-expanded={emailExpanded}
              disabled={pending !== null}
              onClick={() => setEmailExpanded(true)}
            >
              Use email instead
            </Button>
            {error?.source === "google" && (
              <FieldError>{error.message}</FieldError>
            )}
          </>
        )}
        {emailExpanded && (
          <FieldGroup id="email-sign-in-fields">
            <Field data-invalid={error?.source === "email"}>
              <FieldLabel htmlFor="email">Email</FieldLabel>
              <Input
                id="email"
                name="email"
                type="email"
                autoComplete="email"
                aria-invalid={error?.source === "email"}
                required
              />
            </Field>
            <Field data-invalid={error?.source === "email"}>
              <div className="flex items-center justify-between">
                <FieldLabel htmlFor="password">Password</FieldLabel>
                <Link
                  href="/forgot-password"
                  className="rounded-sm text-xs text-muted-foreground underline-offset-4 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring/30"
                >
                  Forgot password?
                </Link>
              </div>
              <Input
                id="password"
                name="password"
                type="password"
                autoComplete="current-password"
                aria-invalid={error?.source === "email"}
                required
              />
              {error?.source === "email" && (
                <FieldError>{error.message}</FieldError>
              )}
            </Field>
            <Button type="submit" size="lg" disabled={pending !== null}>
              {pending === "email" && (
                <LoaderCircleIcon
                  data-icon="inline-start"
                  className="motion-safe:animate-spin"
                />
              )}
              Sign in
            </Button>
          </FieldGroup>
        )}
      </FieldGroup>
    </form>
  )
}

function googleErrorMessage(code: string): string {
  if (code === "POPUP_BLOCKED") {
    return "Allow pop-ups for Acuity, then try again."
  }
  if (code === "POPUP_CLOSED") {
    return "Google sign-in was closed. Try again or use email."
  }
  return "Google sign-in didn’t finish. Try again or use email."
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
