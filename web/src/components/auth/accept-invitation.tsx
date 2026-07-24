"use client"

import { useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { LoaderCircleIcon } from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { acceptInvitation } from "@/lib/api/generated/sdk.gen"
import { portalClient } from "@/lib/api/client"
import { authClient, getAccessToken } from "@/lib/auth-client"
import {
  clearPendingInvitation,
  pendingInvitation,
} from "@/lib/pending-invitation"

type ActivationState = "loading" | "denied" | "unavailable"

export function AcceptInvitation() {
  const router = useRouter()
  const session = authClient.useSession()
  const [state, setState] = useState<ActivationState>("loading")
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    if (session.isPending) return
    if (!session.data) {
      router.replace("/sign-in?next=%2Faccept-invitation")
      return
    }
    let active = true
    const timeout = window.setTimeout(() => {
      const token = pendingInvitation()
      if (!token) {
        setState("denied")
        return
      }
      setState("loading")
      void getAccessToken().then(async (accessToken) => {
        if (!active) return
        if (!accessToken) {
          router.replace("/sign-in")
          return
        }
        const result = await acceptInvitation({
          client: portalClient(accessToken),
          body: { token },
        })
        if (!active) return
        if (result.data) {
          clearPendingInvitation()
          router.replace("/workspace")
          return
        }
        const status = result.response?.status
        if (status === 403 || status === 409) {
          clearPendingInvitation()
          setState("denied")
          return
        }
        setState("unavailable")
      }).catch(() => {
        if (active) setState("unavailable")
      })
    }, 0)
    return () => {
      active = false
      window.clearTimeout(timeout)
    }
  }, [attempt, router, session.data, session.isPending])

  if (state === "denied") {
    return (
      <Alert variant="destructive">
        <AlertTitle>Invitation unavailable</AlertTitle>
        <AlertDescription>
          This invitation is missing, expired, revoked, or already used.
        </AlertDescription>
      </Alert>
    )
  }
  if (state === "unavailable") {
    return (
      <Alert variant="destructive">
        <AlertTitle>Access service unavailable</AlertTitle>
        <AlertDescription>
          <p>No access change was assumed. Retry the authoritative request.</p>
          <Button
            className="mt-4"
            variant="outline"
            onClick={() => setAttempt((value) => value + 1)}
          >
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  }
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      <LoaderCircleIcon aria-hidden="true" className="animate-spin" />
      Resolving current Practice access
    </div>
  )
}
