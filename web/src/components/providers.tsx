"use client"

import { ThemeProvider } from "next-themes"

import { TooltipProvider } from "@/components/ui/tooltip"
import { DataProvider } from "@/lib/data/provider"

export function Providers({ children }: { children: React.ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem>
      <DataProvider>
        <TooltipProvider>{children}</TooltipProvider>
      </DataProvider>
    </ThemeProvider>
  )
}
