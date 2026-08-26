import type { Metadata } from "next"

import { WorkspaceShell } from "@/components/workspace/workspace-shell"

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
  return <WorkspaceShell />
}
