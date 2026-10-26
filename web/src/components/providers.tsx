"use client"

import { LucideProvider } from "lucide-react"
import { ThemeProvider } from "next-themes"

import { TooltipProvider } from "@/components/ui/tooltip"

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
      <LucideProvider strokeWidth={1.75}>
        <TooltipProvider>{children}</TooltipProvider>
      </LucideProvider>
    </ThemeProvider>
  )
}
