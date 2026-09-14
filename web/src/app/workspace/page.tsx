import type { Metadata } from "next"
import { cookies } from "next/headers"

import { PortalWorkspace } from "@/components/workspace/portal-workspace"

export const metadata: Metadata = {
  title: "Operations Workspace",
  robots: {
    index: false,
    follow: false,
    noarchive: true,
    nocache: true,
  },
}

export default async function WorkspacePage() {
  const sidebarOpen = (await cookies()).get("sidebar_state")?.value !== "false"
  return <PortalWorkspace defaultSidebarOpen={sidebarOpen} />
}
