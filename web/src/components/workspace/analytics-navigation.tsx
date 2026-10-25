"use client"

import {
  BookOpenIcon,
  CalendarCheck2Icon,
  ChartNoAxesCombinedIcon,
  Clock3Icon,
  CoinsIcon,
  HouseIcon,
  ListChecksIcon,
  LayoutDashboardIcon,
  PhoneIcon,
  ScaleIcon,
  ShieldCheckIcon,
  ChartNoAxesColumnIncreasingIcon,
  UsersIcon,
  WrenchIcon,
  type LucideIcon,
} from "lucide-react"

import {
  SidebarGroupContent,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar"
import { type ManageAgentPage } from "@/lib/workspace-projection"

export type CommandCenterPage = "calls" | "overview" | "breakdowns" | "tools" | "cost" | "review" | "judges"

export const commandCenterPages: Array<{ value: CommandCenterPage; label: string; icon: typeof PhoneIcon }> = [
  { value: "calls", label: "Calls", icon: PhoneIcon },
  { value: "overview", label: "Summary", icon: LayoutDashboardIcon },
  { value: "breakdowns", label: "Breakdowns", icon: ChartNoAxesColumnIncreasingIcon },
  { value: "tools", label: "Tools", icon: WrenchIcon },
  { value: "cost", label: "Cost", icon: CoinsIcon },
  { value: "judges", label: "Judges", icon: ScaleIcon },
  { value: "review", label: "Review", icon: ListChecksIcon },
]

export type PracticeAnalyticsPage = "bookings" | "conversion" | "duration" | "staff"

export const practiceAnalyticsPages: Array<{ value: PracticeAnalyticsPage; label: string; icon: typeof CalendarCheck2Icon }> = [
  { value: "bookings", label: "Bookings", icon: CalendarCheck2Icon },
  { value: "conversion", label: "Conversion", icon: ChartNoAxesCombinedIcon },
  { value: "duration", label: "Duration", icon: Clock3Icon },
  { value: "staff", label: "Staff", icon: UsersIcon },
]

export const manageAgentPages: Array<{ value: ManageAgentPage; label: string; icon: typeof BookOpenIcon }> = [
  { value: "home", label: "Home", icon: HouseIcon },
  { value: "transcripts", label: "Calls", icon: PhoneIcon },
  { value: "knowledge", label: "Knowledge base", icon: BookOpenIcon },
  { value: "insurance", label: "Insurance list", icon: ShieldCheckIcon },
]

export function AnalyticsNavigation<Page extends string>({
  label,
  pages,
  page,
  onPage,
}: {
  label: string
  pages: Array<{ value: Page; label: string; icon: LucideIcon }>
  page: Page
  onPage: (page: Page) => void
}) {
  const { isMobile, setOpenMobile } = useSidebar()
  return (
    <nav aria-label={label} className="flex w-full min-w-0 flex-col">
      <p className="flex h-8 items-center px-2.5 text-xs font-medium text-muted-foreground">{label}</p>
      <SidebarGroupContent>
        <SidebarMenu className="gap-1">
          {pages.map((item) => (
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
