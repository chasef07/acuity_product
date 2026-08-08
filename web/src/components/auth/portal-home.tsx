"use client"

import Image from "next/image"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Suspense, useState } from "react"

import { SignInForm } from "@/components/auth/sign-in-form"
import { Button } from "@/components/ui/button"
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
  DialogTrigger,
} from "@/components/ui/dialog"
import { Separator } from "@/components/ui/separator"
import { Skeleton } from "@/components/ui/skeleton"

export function PortalHome({
  googleEnabled,
  initiallyOpen = false,
}: {
  googleEnabled: boolean
  initiallyOpen?: boolean
}) {
  const router = useRouter()
  const [open, setOpen] = useState(initiallyOpen)

  function updateOpen(nextOpen: boolean) {
    setOpen(nextOpen)
    if (!nextOpen && initiallyOpen) {
      router.replace("/")
    }
  }

  return (
    <Dialog open={open} onOpenChange={updateOpen}>
      <main className="min-h-svh bg-card text-card-foreground">
        <header className="flex h-16 items-center justify-between px-5 sm:px-8">
          <Link href="/" className="flex items-center gap-2 font-medium">
            <Image
              src="/acuity-health-mark.png"
              alt=""
              aria-hidden="true"
              width={28}
              height={28}
              className="size-7"
              priority
            />
            Acuity Health
          </Link>
          <DialogTrigger render={<Button variant="outline" size="sm" />}>
            Portal
          </DialogTrigger>
        </header>
      </main>

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
          <Image
            src="/acuity-health-mark.png"
            alt=""
            aria-hidden="true"
            width={448}
            height={448}
            className="pointer-events-none absolute -right-56 -bottom-56 size-[28rem] max-w-none opacity-[0.025] select-none"
            priority
          />
          <CardHeader className="relative justify-items-center gap-3 px-6 pt-8 pb-5 text-center sm:px-8">
            <Image
              src="/acuity-health-mark.png"
              alt=""
              aria-hidden="true"
              width={40}
              height={40}
              className="size-10"
              priority
            />
            <CardTitle>
              <DialogTitle className="text-xl font-semibold tracking-tight">
                Sign in to Acuity
              </DialogTitle>
            </CardTitle>
          </CardHeader>
          <CardContent className="relative px-6 pb-6 sm:px-8">
            <Suspense fallback={<Skeleton className="h-32 w-full" />}>
              <SignInForm googleEnabled={googleEnabled} />
            </Suspense>
          </CardContent>
          <Separator />
          <CardFooter className="relative justify-center py-3 text-[0.6875rem] text-muted-foreground">
            Invite-only access
          </CardFooter>
        </Card>
      </DialogContent>
    </Dialog>
  )
}
