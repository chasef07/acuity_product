import type { Metadata } from "next"

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

export default function WorkspacePage() {
  return <PortalWorkspace />
}
