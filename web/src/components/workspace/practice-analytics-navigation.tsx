"use client"

import { CalendarCheck2Icon, ChartNoAxesCombinedIcon, Clock3Icon, UsersIcon } from "lucide-react"

import {
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"

export type PracticeAnalyticsPage = "bookings" | "conversion" | "duration" | "staff"

export const practiceAnalyticsPages: Array<{ value: PracticeAnalyticsPage; label: string; icon: typeof CalendarCheck2Icon }> = [
  { value: "bookings", label: "Bookings", icon: CalendarCheck2Icon },
  { value: "conversion", label: "Conversion", icon: ChartNoAxesCombinedIcon },
  { value: "duration", label: "Duration", icon: Clock3Icon },
  { value: "staff", label: "Staff", icon: UsersIcon },
]

export function PracticeAnalyticsNavigation({
  page,
  onPage,
}: {
  page: PracticeAnalyticsPage
  onPage: (page: PracticeAnalyticsPage) => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()
  return (
    <nav aria-label="Analytics" className="flex w-full min-w-0 flex-col">
      <p className="flex h-8 items-center px-2.5 text-xs font-medium text-muted-foreground">
        Analytics
      </p>
      <SidebarGroupContent>
        <SidebarMenu className="gap-1">
          {practiceAnalyticsPages.map((item) => (
            <SidebarMenuItem key={item.value}>
              <SidebarMenuButton
                isActive={page === item.value}
                aria-current={page === item.value ? "page" : undefined}
                className="h-9 rounded-lg px-2.5"
                onClick={() => {
                  onPage(item.value)
                  if (isMobile) setOpenMobile(false)
                }}
              >
                <item.icon aria-hidden="true" />
                <span>{item.label}</span>
              </SidebarMenuButton>
            </SidebarMenuItem>
          ))}
        </SidebarMenu>
      </SidebarGroupContent>
    </nav>
  )
}
