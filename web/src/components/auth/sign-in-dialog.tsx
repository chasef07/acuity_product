"use client"

import { useRouter } from "next/navigation"
import { createContext, Suspense, useContext, useEffect, useState } from "react"

import { AcuityMark } from "@/components/acuity-mark"
import { SignInForm } from "@/components/auth/sign-in-form"
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogTitle,
} from "@/components/ui/dialog"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"
import { authClient, clearAccessToken } from "@/lib/auth-client"

const SignInContext = createContext<{ pending: boolean; signIn: () => void } | null>(null)

export function PortalSignInTrigger({ className }: { className?: string }) {
  const signIn = useContext(SignInContext)
  if (!signIn) throw new Error("Sign-in trigger requires SignInDialog")
  return (
    <button
      type="button"
      className={className}
      disabled={signIn.pending}
      onClick={signIn.signIn}
    >
      Sign in
    </button>
  )
}

function nextDestination() {
  const requested = new URLSearchParams(window.location.search).get("next")
  if (
    !requested?.startsWith("/") ||
    requested.startsWith("//") ||
    requested.includes("\\")
  ) {
    return "/workspace"
  }
  return requested
}

export function SignInDialog({
  children,
  initiallyOpen = false,
}: {
  children: React.ReactNode
  initiallyOpen?: boolean
}) {
  const router = useRouter()
  const [open, setOpen] = useState(initiallyOpen)
  const session = authClient.useSession()
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [signedInDestination, setSignedInDestination] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    if (session.data) {
      router.replace(signedInDestination ?? nextDestination())
      return
    }
    if (!signedInDestination) return
    const timeout = window.setTimeout(() => {
      setSignedInDestination(null)
      setPending(false)
      setError("Google sign-in finished, but the session was not ready. Try again.")
    }, 5_000)
    return () => window.clearTimeout(timeout)
  }, [open, router, session.data, signedInDestination])

  async function signIn() {
    if (pending || session.isPending) return
    const destination = nextDestination()
    if (session.data) {
      router.push(destination)
      return
    }
    setOpen(true)
    setPending(true)
    setError(null)
    clearAccessToken()
    try {
      // Open synchronously from the click so the browser permits the popup.
      const result = await authClient.signIn.popup({
        provider: "google",
        callbackURL: destination,
        errorCallbackURL: `/sign-in?error=google&next=${encodeURIComponent(destination)}`,
      })
      if (result?.error) {
        setError(googleErrorMessage(result.error.code))
        setPending(false)
        return
      }
      setSignedInDestination(destination)
    } catch {
      setError("Google sign-in didn’t finish. Try again.")
      setPending(false)
    }
  }

  function updateOpen(nextOpen: boolean) {
    setOpen(nextOpen)
    if (!nextOpen && initiallyOpen) {
      router.replace("/")
    }
  }

  return (
    <SignInContext value={{ pending: pending || session.isPending, signIn }}>
      <Dialog open={open} onOpenChange={updateOpen}>
        {children}
        <DialogContent
          data-testid="sign-in-dialog"
          className="gap-0 overflow-hidden p-0 motion-reduce:animate-none motion-reduce:duration-0 sm:max-w-[24.5rem]"
          overlayClassName="motion-reduce:animate-none motion-reduce:duration-0"
          showCloseButton={false}
        >
          <Card
            data-testid="sign-in-card"
            className="relative gap-0 rounded-xl py-0 ring-0"
          >
            <AcuityMark className="pointer-events-none absolute -right-56 -bottom-56 size-[28rem] max-w-none opacity-[0.025] select-none" />
            <CardHeader className="relative justify-items-center gap-3 px-6 pt-8 pb-5 text-center sm:px-8">
              <AcuityMark className="size-10" />
              <CardTitle>
                <DialogTitle className="text-xl font-semibold tracking-tight">
                  Sign in to Acuity Health
                </DialogTitle>
              </CardTitle>
            </CardHeader>
            <CardContent className="relative px-6 pb-6 sm:px-8">
              <Suspense fallback={<Skeleton className="h-32 w-full" />}>
                <SignInForm
                  pending={pending || session.isPending}
                  error={error}
                  onSignIn={signIn}
                />
              </Suspense>
            </CardContent>
            <Separator />
            <CardFooter className="relative justify-center py-3 text-[0.6875rem] text-muted-foreground">
              Secure Google sign-in
            </CardFooter>
          </Card>
        </DialogContent>
      </Dialog>
    </SignInContext>
  )
}

function googleErrorMessage(code: string): string {
  if (code === "POPUP_BLOCKED") return "Allow pop-ups for Acuity Health, then try again."
  if (code === "POPUP_CLOSED") return "Google sign-in was closed. Try again."
  return "Google sign-in didn’t finish. Try again."
}
