import { DataProvider } from "@/lib/data/provider"

export default function WorkspaceLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return <DataProvider>{children}</DataProvider>
}
