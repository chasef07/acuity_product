"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { CheckCircle2Icon, LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { authClient } from "@/lib/auth-client"

export function ForgotPasswordForm() {
  const [pending, setPending] = useState(false)
  const [sent, setSent] = useState(false)

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    const data = new FormData(event.currentTarget)
    await authClient.requestPasswordReset({
      email: String(data.get("email") ?? ""),
      redirectTo: "/reset-password",
    })
    setPending(false)
    setSent(true)
  }

  if (sent) {
    return (
      <Alert>
        <CheckCircle2Icon aria-hidden="true" />
        <AlertTitle>Check your email</AlertTitle>
        <AlertDescription>
          If that address has an account, a private reset link is on its way.
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <form onSubmit={submit}>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="email">Verified email</FieldLabel>
          <Input
            id="email"
            name="email"
            type="email"
            autoComplete="email"
            required
          />
        </Field>
        <Button type="submit" size="lg" disabled={pending}>
          {pending && (
            <LoaderCircleIcon data-icon="inline-start" className="animate-spin" />
          )}
          Send recovery link
        </Button>
        <Button
          variant="link"
          nativeButton={false}
          render={<Link href="/sign-in" />}
        >
          Return to sign in
        </Button>
      </FieldGroup>
    </form>
  )
}

export function ResetPasswordForm() {
  const [token, setToken] = useState<string>()
  const [resolved, setResolved] = useState(false)
  const [pending, setPending] = useState(false)
  const [complete, setComplete] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    const timeout = window.setTimeout(() => {
      const fragment = new URLSearchParams(window.location.hash.slice(1))
      setToken(fragment.get("token") ?? undefined)
      setResolved(true)
      window.history.replaceState(null, "", window.location.pathname)
    }, 0)
    return () => window.clearTimeout(timeout)
  }, [])

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!token) return
    setPending(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const password = String(data.get("password") ?? "")
    if (password !== String(data.get("confirmation") ?? "")) {
      setError("Passwords must match.")
      setPending(false)
      return
    }
    const result = await authClient.resetPassword({
      newPassword: password,
      token,
    })
    setPending(false)
    if (result.error) {
      setError("This reset link is invalid or expired.")
      return
    }
    setComplete(true)
  }

  if (!resolved) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
        Checking reset credential
      </div>
    )
  }
  if (!token) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Reset link unavailable</AlertTitle>
        <AlertDescription>
          Request a new recovery link to continue.
        </AlertDescription>
      </Alert>
    )
  }
  if (complete) {
    return (
      <Alert>
        <CheckCircle2Icon aria-hidden="true" />
        <AlertTitle>Password updated</AlertTitle>
        <AlertDescription>
          <Link href="/sign-in">Sign in with your new password.</Link>
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <form onSubmit={submit}>
      <FieldGroup>
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="password">New password</FieldLabel>
          <Input
            id="password"
            name="password"
            type="password"
            minLength={12}
            maxLength={128}
            autoComplete="new-password"
            required
          />
          <FieldDescription>Use at least 12 characters.</FieldDescription>
        </Field>
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="confirmation">Confirm new password</FieldLabel>
          <Input
            id="confirmation"
            name="confirmation"
            type="password"
            minLength={12}
            maxLength={128}
            autoComplete="new-password"
            required
          />
          <FieldError>{error}</FieldError>
        </Field>
        <Button type="submit" size="lg" disabled={pending}>
          {pending && (
            <LoaderCircleIcon data-icon="inline-start" className="animate-spin" />
          )}
          Update password
        </Button>
      </FieldGroup>
    </form>
  )
}
