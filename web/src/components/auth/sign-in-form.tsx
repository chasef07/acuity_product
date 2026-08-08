"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useState } from "react"
import { ArrowRightIcon, LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { authClient } from "@/lib/auth-client"

export function SignInForm({ googleEnabled }: { googleEnabled: boolean }) {
  const router = useRouter()
  const searchParams = useSearchParams()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState(
    searchParams.get("error") === "google"
      ? "Google sign-in was not accepted."
      : "",
  )

  async function signInWithGoogle() {
    setPending(true)
    setError("")
    const result = await authClient.signIn.social({
      provider: "google",
      callbackURL: "/workspace",
      errorCallbackURL: "/sign-in?error=google",
    })
    if (result?.error) {
      setPending(false)
      setError("Google sign-in was not accepted.")
    }
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const result = await authClient.signIn.email({
      email: String(data.get("email") ?? ""),
      password: String(data.get("password") ?? ""),
      rememberMe: true,
    })
    setPending(false)
    if (result.error) {
      setError("Email or password was not accepted.")
      return
    }
    const requested = searchParams.get("next")
    router.replace(requested?.startsWith("/") ? requested : "/workspace")
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
              variant="outline"
              disabled={pending}
              onClick={signInWithGoogle}
            >
              Continue with Google
            </Button>
            <div className="flex items-center gap-3 text-xs text-muted-foreground">
              <span className="h-px flex-1 bg-border" />
              <span>or use email</span>
              <span className="h-px flex-1 bg-border" />
            </div>
          </>
        )}
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="email">Email</FieldLabel>
          <Input
            id="email"
            name="email"
            type="email"
            autoComplete="email"
            required
          />
        </Field>
        <Field data-invalid={Boolean(error)}>
          <div className="flex items-center justify-between">
            <FieldLabel htmlFor="password">Password</FieldLabel>
            <Link
              href="/forgot-password"
              className="text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              Forgot password?
            </Link>
          </div>
          <Input
            id="password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
          />
          <FieldError>{error}</FieldError>
        </Field>
        <Button type="submit" size="lg" disabled={pending}>
          {pending ? (
            <LoaderCircleIcon data-icon="inline-start" className="animate-spin" />
          ) : (
            <ArrowRightIcon data-icon="inline-end" />
          )}
          Sign in
        </Button>
      </FieldGroup>
    </form>
  )
}
