import Link from "next/link"
import { ShieldCheckIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Separator } from "@/components/ui/separator"

export function AuthFrame({
  eyebrow,
  title,
  description,
  children,
}: {
  eyebrow: string
  title: string
  description: string
  children: React.ReactNode
}) {
  return (
    <main className="grid min-h-svh bg-muted/40 lg:grid-cols-[minmax(22rem,0.8fr)_minmax(34rem,1.2fr)]">
      <section className="hidden border-r bg-sidebar p-10 lg:flex lg:flex-col lg:justify-between">
        <Link href="/sign-in" className="flex items-center gap-2 font-medium">
          <span className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <ShieldCheckIcon aria-hidden="true" />
          </span>
          Acuity Health
        </Link>
        <div className="max-w-md">
          <Badge variant="outline">Secure Google workspace</Badge>
          <h2 className="mt-4 text-3xl font-semibold tracking-tight">
            One clear operating surface for every authorized interaction.
          </h2>
          <p className="mt-3 text-sm leading-6 text-muted-foreground">
            Practice and Location access is resolved from current Acuity
            authority on every request.
          </p>
        </div>
        <p className="text-xs text-muted-foreground">
          Credentials are private to each user. Acuity operators never create
          or know customer passwords.
        </p>
      </section>
      <section className="flex items-center justify-center p-5 sm:p-10">
        <div className="w-full max-w-sm rounded-xl border bg-background p-6 shadow-sm">
          <div className="flex flex-col gap-2">
            <p className="text-xs font-medium uppercase tracking-[0.18em] text-primary">
              {eyebrow}
            </p>
            <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
            <p className="text-sm leading-6 text-muted-foreground">
              {description}
            </p>
          </div>
          <Separator className="my-5" />
          {children}
        </div>
      </section>
    </main>
  )
}
