"use client"

import {
  ChartNoAxesCombinedIcon,
  CoinsIcon,
  ListChecksIcon,
  PhoneIcon,
  ShieldCheckIcon,
  WrenchIcon,
} from "lucide-react"

import {
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"

export type ControlCenterPage = "calls" | "overview" | "quality" | "tools" | "cost" | "review"

export const controlCenterPages: Array<{ value: ControlCenterPage; label: string; icon: typeof PhoneIcon }> = [
  { value: "calls", label: "Calls", icon: PhoneIcon },
  { value: "overview", label: "Overview", icon: ChartNoAxesCombinedIcon },
  { value: "quality", label: "Quality", icon: ShieldCheckIcon },
  { value: "tools", label: "Tools", icon: WrenchIcon },
  { value: "cost", label: "Cost", icon: CoinsIcon },
  { value: "review", label: "Review", icon: ListChecksIcon },
]

export function ControlCenterNavigation({
  page,
  onPage,
}: {
  page: ControlCenterPage
  onPage: (page: ControlCenterPage) => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()
  return (
    <nav aria-label="Control Center" className="flex w-full min-w-0 flex-col">
      <p className="flex h-8 items-center px-2.5 text-xs font-medium text-muted-foreground">
        Control Center
      </p>
      <SidebarGroupContent>
        <SidebarMenu className="gap-1">
          {controlCenterPages.map((item) => (
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
