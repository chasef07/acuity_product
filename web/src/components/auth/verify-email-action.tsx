"use client"

import { useEffect, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"

export function VerifyEmailAction() {
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    let active = true
    const timeout = window.setTimeout(() => {
      const fragment = new URLSearchParams(window.location.hash.slice(1))
      const token = fragment.get("token")
      const callbackURL = safeCallback(fragment.get("callbackURL"))
      window.history.replaceState(null, "", window.location.pathname)
      if (!token) {
        setFailed(true)
        return
      }
      void fetch("/api/verify-email", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token }),
      }).then((response) => {
        if (!active) return
        if (!response.ok) {
          setFailed(true)
          return
        }
        window.location.replace(callbackURL)
      }).catch(() => {
        if (active) setFailed(true)
      })
    }, 0)
    return () => {
      active = false
      window.clearTimeout(timeout)
    }
  }, [])

  if (failed) {
    return (
      <Alert variant="destructive">
        <AlertTitle>Verification link unavailable</AlertTitle>
        <AlertDescription>
          Request a new verification email or restart from your invitation.
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
      Verifying one-time credential
    </div>
  )
}

function safeCallback(value: string | null): string {
  if (!value) return "/sign-in"
  const target = new URL(value, window.location.origin)
  if (target.origin !== window.location.origin) return "/sign-in"
  return `${target.pathname}${target.search}${target.hash}`
}
