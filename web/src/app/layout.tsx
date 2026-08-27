import type { Metadata } from "next"

import { Providers } from "@/components/providers"

import "./globals.css"

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
      className="h-full"
    >
      <body className="flex min-h-full flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  )
}
