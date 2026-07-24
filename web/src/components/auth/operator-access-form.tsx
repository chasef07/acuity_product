"use client"

import { useState } from "react"
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

export function OperatorAccessForm() {
  const [pending, setPending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState("")

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const password = String(data.get("password") ?? "")
    if (password !== String(data.get("confirmation") ?? "")) {
      setError("Passwords must match.")
      setPending(false)
      return
    }
    const result = await authClient.signUp.email({
      name: String(data.get("name") ?? ""),
      email: String(data.get("email") ?? ""),
      password,
      callbackURL: "/sign-in?verified=1&next=%2Fworkspace",
    })
    setPending(false)
    if (result.error) {
      setError("That email is not provisioned for Platform Operator access.")
      return
    }
    setSent(true)
  }

  if (sent) {
    return (
      <Alert>
        <CheckCircle2Icon aria-hidden="true" />
        <AlertTitle>Check your email</AlertTitle>
        <AlertDescription>
          Verify the provisioned operator email, then sign in.
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <form onSubmit={submit}>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="name">Your name</FieldLabel>
          <Input id="name" name="name" autoComplete="name" required />
        </Field>
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="email">Provisioned operator email</FieldLabel>
          <Input
            id="email"
            name="email"
            type="email"
            autoComplete="email"
            required
          />
        </Field>
        <Field data-invalid={Boolean(error)}>
          <FieldLabel htmlFor="password">Create password</FieldLabel>
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
          <FieldLabel htmlFor="confirmation">Confirm password</FieldLabel>
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
          Create operator account
        </Button>
      </FieldGroup>
    </form>
  )
}
