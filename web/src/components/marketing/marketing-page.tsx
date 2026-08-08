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
  initiallyOpen = false,
}: {
  initiallyOpen?: boolean
}) {
  return (
    <div className={cn(sans.variable, display.variable)}>
      <LandingPage initiallyOpen={initiallyOpen} />
    </div>
  )
}
