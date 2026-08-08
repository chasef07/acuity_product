import { Geist, Newsreader } from "next/font/google"

import { cn } from "@/lib/utils"

import { LandingPage } from "./landing-page"

const sans = Geist({
  subsets: ["latin"],
  variable: "--font-geist",
})
const display = Newsreader({
  subsets: ["latin"],
  variable: "--font-newsreader",
})

export function MarketingPage({
  googleEnabled,
  initiallyOpen = false,
}: {
  googleEnabled: boolean
  initiallyOpen?: boolean
}) {
  return (
    <div className={cn(sans.variable, display.variable)}>
      <LandingPage
        googleEnabled={googleEnabled}
        initiallyOpen={initiallyOpen}
      />
    </div>
  )
}
