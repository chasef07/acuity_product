"use client"

import { useEffect, useState } from "react"
import { CheckCircle2Icon, LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Field,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { NewPasswordFields } from "@/components/auth/new-password-fields"
import { inspectInvitation } from "@/lib/api/generated/sdk.gen"
import type { InvitationPreview } from "@/lib/api/generated/types.gen"
import { portalClient } from "@/lib/api/client"
import { authClient } from "@/lib/auth-client"
import { confirmedPassword } from "@/lib/password-policy"
import {
  capturePendingInvitation,
  clearPendingInvitation,
} from "@/lib/pending-invitation"

export function InvitationForm() {
  const [token, setToken] = useState("")
  const [preview, setPreview] = useState<InvitationPreview>()
  const [loading, setLoading] = useState(true)
  const [pending, setPending] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    let active = true
    const timeout = window.setTimeout(() => {
      const captured = capturePendingInvitation()
      if (!captured) {
        setError("The activation token is missing.")
        setLoading(false)
        return
      }
      setToken(captured)
      void inspectInvitation({
        client: portalClient(),
        body: { token: captured },
      }).then((result) => {
        if (!active) return
        if (result.data) {
          setPreview(result.data)
        } else {
          const status = result.response?.status
          if (status === 403 || status === 409) {
            clearPendingInvitation()
          }
          setError("This invitation is no longer available.")
        }
        setLoading(false)
      }).catch(() => {
        if (!active) return
        setError("Invitation details are temporarily unavailable.")
        setLoading(false)
      })
    }, 0)
    return () => {
      active = false
      window.clearTimeout(timeout)
    }
  }, [])

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!preview) return
    setPending(true)
    setError("")
    const form = new FormData(event.currentTarget)
    const password = confirmedPassword(form)
    if (password === undefined) {
      setError("Passwords must match.")
      setPending(false)
      return
    }
    const next = "/accept-invitation"
    const result = await authClient.signUp.email(
      {
        name: String(form.get("name") ?? ""),
        email: preview.email,
        password,
        callbackURL: `/sign-in?verified=1&next=${encodeURIComponent(next)}`,
      },
      {
        headers: { "x-acuity-invitation-token": token },
      },
    )
    setPending(false)
    if (result.error) {
      setError(result.error.message ?? "Account creation was not accepted.")
      return
    }
    setSent(true)
  }

  if (loading) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
        Checking invitation
      </div>
    )
  }
  if (!preview || error && !sent && !preview) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Invitation unavailable</AlertTitle>
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    )
  }
  if (sent) {
    return (
      <Alert>
        <CheckCircle2Icon aria-hidden="true" />
        <AlertTitle>Check your email</AlertTitle>
        <AlertDescription>
          Verify {preview.email}, then sign in to activate your invitation.
        </AlertDescription>
      </Alert>
    )
  }

  return (
    <form onSubmit={submit}>
      <FieldGroup>
        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="flex items-center justify-between gap-3">
            <p className="font-medium">{preview.practiceName}</p>
            <Badge variant="secondary">{preview.role}</Badge>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {preview.locationScope === "ALL"
              ? "All current and future Locations"
              : `${preview.locations.length} selected Location${preview.locations.length === 1 ? "" : "s"}`}
          </p>
        </div>
        <Field>
          <FieldLabel htmlFor="name">Your name</FieldLabel>
          <Input id="name" name="name" autoComplete="name" required />
        </Field>
        <Field>
          <FieldLabel htmlFor="email">Verified email</FieldLabel>
          <Input id="email" value={preview.email} readOnly disabled />
        </Field>
        <NewPasswordFields error={error} />
        <Button type="submit" size="lg" disabled={pending}>
          {pending && (
            <LoaderCircleIcon data-icon="inline-start" className="animate-spin" />
          )}
          Create private account
        </Button>
      </FieldGroup>
    </form>
  )
}
