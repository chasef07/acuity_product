"use client"

import { useId } from "react"
import {
  BookOpenIcon,
  HouseIcon,
  PhoneIcon,
  ShieldCheckIcon,
} from "lucide-react"

import {
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { type ManageAgentPage } from "@/lib/workspace-projection"

export const manageAgentPages: Array<{ value: ManageAgentPage; label: string; icon: typeof BookOpenIcon }> = [
  { value: "home", label: "Home", icon: HouseIcon },
  { value: "transcripts", label: "Calls", icon: PhoneIcon },
  { value: "knowledge", label: "Knowledge base", icon: BookOpenIcon },
  { value: "insurance", label: "Insurance list", icon: ShieldCheckIcon },
]

export function ManageAgentNavigation({
  page,
  onPage,
}: {
  page: ManageAgentPage
  onPage: (page: ManageAgentPage) => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()
  const labelID = useId()
  return (
    <nav aria-labelledby={labelID} className="flex w-full min-w-0 flex-col">
      <p id={labelID} className="flex h-8 items-center px-2.5 text-xs font-medium text-muted-foreground">
        Manage agent
      </p>
      <SidebarGroupContent>
        <SidebarMenu className="gap-1">
          {manageAgentPages.map((item) => (
            <SidebarMenuItem key={item.value}>
              <SidebarMenuButton
                isActive={page === item.value}
                aria-current={page === item.value ? "page" : undefined}
                className="h-9 rounded-lg px-2.5 font-medium text-sidebar-foreground/90 [&>svg]:text-sidebar-foreground/90"
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
