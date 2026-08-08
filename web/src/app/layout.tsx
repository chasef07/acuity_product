import type { Metadata } from "next"
import { Geist, Inter, JetBrains_Mono, Newsreader } from "next/font/google"

import { Providers } from "@/components/providers"
import { cn } from "@/lib/utils"

import "./globals.css"

const sans = Inter({
  subsets: ["latin"],
  variable: "--font-inter",
})
const mono = JetBrains_Mono({
  subsets: ["latin"],
  variable: "--font-jetbrains-mono",
})
const marketingSans = Geist({
  subsets: ["latin"],
  variable: "--font-geist",
})
const marketingDisplay = Newsreader({
  subsets: ["latin"],
  variable: "--font-newsreader",
})

export const metadata: Metadata = {
  title: "Acuity Portal",
  description: "Acuity Health operations workspace",
}

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      className={cn(
        "h-full",
        sans.variable,
        mono.variable,
        marketingSans.variable,
        marketingDisplay.variable,
      )}
    >
      <body className="flex min-h-full flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  )
}
