"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { useTheme } from "next-themes"
import {
  ActivityIcon,
  Building2Icon,
  ClipboardListIcon,
  HeadphonesIcon,
  InboxIcon,
  ListFilterIcon,
  LogOutIcon,
  MapPinIcon,
  MessageSquareTextIcon,
  Mic2Icon,
  MoonIcon,
  PanelRightIcon,
  RefreshCwIcon,
  SearchIcon,
  SettingsIcon,
  ShieldCheckIcon,
  SunIcon,
  WifiOffIcon,
} from "lucide-react"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import {
  NativeSelect,
  NativeSelectOption,
} from "@/components/ui/native-select"
import { Separator } from "@/components/ui/separator"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarInput,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
} from "@/components/ui/sidebar"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { portalClient, realtimeURL } from "@/lib/api/client"
import {
  discoverAccess,
  enterSupportMode,
  getWorkspace,
  revokeSupportMode,
} from "@/lib/api/generated/sdk.gen"
import type {
  AccessDiscovery,
  PracticeAccess,
  WorkspaceSnapshot,
} from "@/lib/api/generated/types.gen"
import { authClient, getAccessToken } from "@/lib/auth-client"

type LoadState = "loading" | "ready" | "unauthorized" | "unavailable"
type ConnectionState = "connecting" | "connected" | "disconnected"

const practiceStorageKey = "acuity.selectedPractice"
const locationStorageKey = "acuity.selectedLocation"

export function WorkspaceShell() {
  const router = useRouter()
  const session = authClient.useSession()
  const [loadState, setLoadState] = useState<LoadState>("loading")
  const [connection, setConnection] =
    useState<ConnectionState>("connecting")
  const [discovery, setDiscovery] = useState<AccessDiscovery>()
  const [workspace, setWorkspace] = useState<WorkspaceSnapshot>()
  const [practiceID, setPracticeID] = useState("")
  const [locationID, setLocationID] = useState("")

  const loadSnapshot = useCallback(
    async (
      selectedPractice: string,
      selectedLocation: string,
      showLoading = true,
    ) => {
      if (showLoading) setLoadState("loading")
      const accessToken = await getAccessToken()
      if (!accessToken) {
        setLoadState("unauthorized")
        return false
      }
      const result = await getWorkspace({
        client: portalClient(accessToken),
        query: {
          practiceId: selectedPractice,
          locationId: selectedLocation,
        },
      }).catch(() => undefined)
      if (!result?.data) {
        const status = result?.response?.status
        setLoadState(
          status === 401 || status === 403 ? "unauthorized" : "unavailable",
        )
        return false
      }
      setWorkspace(result.data)
      setLoadState("ready")
      return true
    },
    [],
  )
  const snapshotRef = useRef(loadSnapshot)
  useEffect(() => {
    snapshotRef.current = loadSnapshot
  }, [loadSnapshot])

  const loadAuthority = useCallback(async () => {
    if (!session.data) return
    const accessToken = await getAccessToken()
    if (!accessToken) {
      setLoadState("unauthorized")
      return
    }
    const result = await discoverAccess({
      client: portalClient(accessToken),
    }).catch(() => undefined)
    if (!result?.data) {
      const status = result?.response?.status
      setLoadState(
        status === 401 || status === 403 ? "unauthorized" : "unavailable",
      )
      return
    }
    const storedPractice = window.localStorage.getItem(practiceStorageKey)
    const selectedPractice =
      result.data.practices.find((item) => item.id === storedPractice) ??
      result.data.practices[0]
    const storedLocation = window.localStorage.getItem(locationStorageKey)
    const selectedLocation =
      selectedPractice?.locations.find((item) => item.id === storedLocation) ??
      selectedPractice?.locations[0]
    if (!selectedPractice || !selectedLocation) {
      setLoadState("unauthorized")
      return
    }
    setDiscovery(result.data)
    setPracticeID(selectedPractice.id)
    setLocationID(selectedLocation.id)
    window.localStorage.setItem(practiceStorageKey, selectedPractice.id)
    window.localStorage.setItem(locationStorageKey, selectedLocation.id)
    await loadSnapshot(selectedPractice.id, selectedLocation.id)
  }, [loadSnapshot, session.data])

  useEffect(() => {
    if (session.isPending) return
    if (!session.data) {
      router.replace("/sign-in?next=%2Fworkspace")
      return
    }
    const timeout = window.setTimeout(() => void loadAuthority(), 0)
    return () => window.clearTimeout(timeout)
  }, [loadAuthority, router, session.data, session.isPending])

  useEffect(() => {
    if (!practiceID || !locationID || loadState !== "ready") return
    const controller = new AbortController()
    let stopped = false

    async function connect() {
      while (!stopped) {
        setConnection("connecting")
        const accessToken = await getAccessToken()
        if (!accessToken) {
          setLoadState("unauthorized")
          return
        }
        if (!(await snapshotRef.current(practiceID, locationID, false))) {
          return
        }
        try {
          const url = new URL("/v1/events", realtimeURL())
          url.searchParams.set("practiceId", practiceID)
          url.searchParams.set("locationId", locationID)
          const response = await fetch(url, {
            headers: {
              accept: "text/event-stream",
              authorization: `Bearer ${accessToken}`,
            },
            signal: controller.signal,
          })
          if (response.status === 401 || response.status === 403) {
            setLoadState("unauthorized")
            return
          }
          if (!response.ok || !response.body) {
            throw new Error("realtime unavailable")
          }
          setConnection("connected")
          const reader = response.body
            .pipeThrough(new TextDecoderStream())
            .getReader()
          let buffer = ""
          while (!stopped) {
            const { value, done } = await reader.read()
            if (done) break
            buffer += value
            const events = buffer.split("\n\n")
            buffer = events.pop() ?? ""
            if (events.some((event) => event.includes("data:"))) {
              await snapshotRef.current(practiceID, locationID, false)
            }
          }
        } catch {
          if (controller.signal.aborted) return
        }
        setConnection("disconnected")
        await new Promise((resolve) =>
          window.setTimeout(resolve, 500 + Math.random() * 750),
        )
      }
    }
    void connect()
    return () => {
      stopped = true
      controller.abort()
    }
  }, [loadState, locationID, practiceID])

  function selectPractice(nextPracticeID: string) {
    const practice = discovery?.practices.find(
      (item) => item.id === nextPracticeID,
    )
    const location = practice?.locations[0]
    if (!practice || !location) return
    setPracticeID(practice.id)
    setLocationID(location.id)
    window.localStorage.setItem(practiceStorageKey, practice.id)
    window.localStorage.setItem(locationStorageKey, location.id)
    void loadSnapshot(practice.id, location.id)
  }

  function selectLocation(nextLocationID: string) {
    setLocationID(nextLocationID)
    window.localStorage.setItem(locationStorageKey, nextLocationID)
    void loadSnapshot(practiceID, nextLocationID)
  }

  if (session.isPending || loadState === "loading" && !discovery) {
    return <WorkspaceLoading />
  }
  if (loadState === "unauthorized") {
    return (
      <WorkspaceFailure
        title="Workspace access unavailable"
        description="Your identity is valid, but current Practice or Location authority is not available."
        action="Return to sign in"
        onAction={() => void authClient.signOut().then(() => router.push("/sign-in"))}
      />
    )
  }
  if (loadState === "unavailable" || !discovery || !workspace) {
    return (
      <WorkspaceFailure
        title="Workspace temporarily disconnected"
        description="No data was reconstructed. Retry the authoritative request when the service is available."
        action="Retry"
        onAction={() => void loadAuthority()}
      />
    )
  }

  const practice = discovery.practices.find(
    (item) => item.id === practiceID,
  ) ?? discovery.practices[0]
  return (
    <SidebarProvider>
      <WorkspaceSidebar
        discovery={discovery}
        practice={practice}
        practiceID={practiceID}
        locationID={locationID}
        onPracticeChange={selectPractice}
        onLocationChange={selectLocation}
      />
      <SidebarInset
        data-testid="mounted-workspace"
        data-workspace-version={workspace.version}
      >
        {workspace.supportMode && (
          <SupportBanner
            supportMode={workspace.supportMode}
            onChanged={() =>
              void loadSnapshot(practiceID, locationID, false)
            }
          />
        )}
        <header className="flex h-14 shrink-0 items-center gap-3 border-b px-4">
          <SidebarTrigger className="-ml-1" />
          <Separator orientation="vertical" className="h-5" />
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-sm font-medium">Tasks</h1>
            <p className="truncate text-xs text-muted-foreground">
              {workspace.practice.name} · {workspace.location.name}
            </p>
          </div>
          <ConnectionBadge state={connection} />
          {workspace.platformOperator && !workspace.supportMode && (
            <SupportDialog
              practiceID={practiceID}
              onEntered={() =>
                void loadSnapshot(practiceID, locationID, false)
              }
            />
          )}
          <WorkspaceContextSheet workspace={workspace} />
        </header>
        <section className="flex min-h-0 flex-1 items-center justify-center p-6">
          {loadState === "loading" ? (
            <WorkspaceCenterLoading />
          ) : (
            <Empty className="max-w-lg border">
              <EmptyMedia variant="icon">
                <InboxIcon aria-hidden="true" />
              </EmptyMedia>
              <EmptyHeader>
                <EmptyTitle>No tasks yet</EmptyTitle>
                <EmptyDescription>
                  This is the authorized empty workspace. New work will appear
                  here after later product slices add task behavior.
                </EmptyDescription>
              </EmptyHeader>
              <Badge variant="outline">Current state · EMPTY</Badge>
            </Empty>
          )}
        </section>
        <footer className="border-t bg-background p-3">
          <div
            aria-label="Task composer"
            className="flex items-center gap-3 rounded-lg border bg-muted/20 px-3 py-2 text-sm text-muted-foreground"
          >
            <MessageSquareTextIcon aria-hidden="true" className="size-4" />
            <span className="flex-1">
              Composer available when a task is selected
            </span>
            <Button size="sm" disabled>
              Compose
            </Button>
          </div>
        </footer>
      </SidebarInset>
      <SidebarRail />
    </SidebarProvider>
  )
}

function WorkspaceSidebar({
  discovery,
  practice,
  practiceID,
  locationID,
  onPracticeChange,
  onLocationChange,
}: {
  discovery: AccessDiscovery
  practice: PracticeAccess
  practiceID: string
  locationID: string
  onPracticeChange: (value: string) => void
  onLocationChange: (value: string) => void
}) {
  const router = useRouter()
  const { resolvedTheme, setTheme } = useTheme()
  return (
    <Sidebar collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" tooltip="Acuity Health">
              <span className="flex size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
                <ActivityIcon aria-hidden="true" />
              </span>
              <span className="flex min-w-0 flex-col">
                <span className="font-semibold">Acuity Health</span>
                <span className="text-[0.625rem] text-muted-foreground">
                  Operations workspace
                </span>
              </span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
        <div className="grid gap-2 px-1 group-data-[collapsible=icon]:hidden">
          <label className="flex items-center gap-1 text-[0.625rem] font-medium uppercase tracking-wider text-muted-foreground">
            <Building2Icon aria-hidden="true" />
            Practice
          </label>
          <NativeSelect
            className="w-full"
            aria-label="Practice"
            value={practiceID}
            onChange={(event) => onPracticeChange(event.target.value)}
          >
            {discovery.practices.map((item) => (
              <NativeSelectOption value={item.id} key={item.id}>
                {item.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <label className="flex items-center gap-1 text-[0.625rem] font-medium uppercase tracking-wider text-muted-foreground">
            <MapPinIcon aria-hidden="true" />
            Location
          </label>
          <NativeSelect
            className="w-full"
            aria-label="Location"
            value={locationID}
            onChange={(event) => onLocationChange(event.target.value)}
          >
            {practice.locations.map((location) => (
              <NativeSelectOption value={location.id} key={location.id}>
                {location.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </div>
        <div className="relative mt-1 group-data-[collapsible=icon]:hidden">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <SidebarInput
            aria-label="Search"
            placeholder="Search"
            disabled
            className="pl-7"
          />
        </div>
      </SidebarHeader>
      <SidebarContent>
        <SidebarGroup>
          <SidebarGroupLabel>Workspace</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton isActive tooltip="Tasks">
                  <ClipboardListIcon aria-hidden="true" />
                  <span>Tasks</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton disabled tooltip="Call Center">
                  <HeadphonesIcon aria-hidden="true" />
                  <span>Call Center</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton disabled tooltip="Recordings">
                  <Mic2Icon aria-hidden="true" />
                  <span>Recordings</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton disabled tooltip="Settings">
                  <SettingsIcon aria-hidden="true" />
                  <span>Settings</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
        <SidebarGroup>
          <SidebarGroupLabel>Current view</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton disabled tooltip="All tasks">
                  <ListFilterIcon aria-hidden="true" />
                  <span>All tasks</span>
                  <Badge className="ml-auto" variant="outline">
                    0
                  </Badge>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton disabled tooltip="No tasks">
                  <InboxIcon aria-hidden="true" />
                  <span>No tasks in this Location</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip={resolvedTheme === "dark" ? "Use light mode" : "Use dark mode"}
              onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
            >
              {resolvedTheme === "dark" ? (
                <SunIcon aria-hidden="true" />
              ) : (
                <MoonIcon aria-hidden="true" />
              )}
              <span>{resolvedTheme === "dark" ? "Light mode" : "Dark mode"}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
          <SidebarMenuItem>
            <SidebarMenuButton
              tooltip="Sign out"
              onClick={() =>
                void authClient.signOut().then(() => router.push("/sign-in"))
              }
            >
              <LogOutIcon aria-hidden="true" />
              <span>{discovery.actor.email}</span>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}

function SupportDialog({
  practiceID,
  onEntered,
}: {
  practiceID: string
  onEntered: () => void
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending(true)
    setError("")
    const data = new FormData(event.currentTarget)
    const accessToken = await getAccessToken()
    if (!accessToken) {
      setPending(false)
      setError("Your authentication needs to be refreshed.")
      return
    }
    const result = await enterSupportMode({
      client: portalClient(accessToken),
      body: {
        practiceId: practiceID,
        reason: String(data.get("reason") ?? ""),
        durationMinutes: Number(data.get("duration") ?? 30),
      },
    }).catch(() => undefined)
    setPending(false)
    if (!result?.data) {
      setError("Support Mode could not be entered.")
      return
    }
    setOpen(false)
    onEntered()
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger render={<Button variant="outline" />}>
        <ShieldCheckIcon data-icon="inline-start" />
        Enter Support Mode
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Enter Practice-scoped Support Mode</DialogTitle>
          <DialogDescription>
            Your real Platform Operator identity remains the actor. This
            session never impersonates a Practice member.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit}>
          <FieldGroup>
            <Field data-invalid={Boolean(error)}>
              <FieldLabel htmlFor="reason">Reason</FieldLabel>
              <Input
                id="reason"
                name="reason"
                minLength={8}
                maxLength={240}
                required
              />
              <FieldDescription>
                The reason is stored with every supported mutation.
              </FieldDescription>
              <FieldError>{error}</FieldError>
            </Field>
            <Field>
              <FieldLabel htmlFor="duration">Duration</FieldLabel>
              <NativeSelect id="duration" name="duration" defaultValue="30">
                <NativeSelectOption value="15">15 minutes</NativeSelectOption>
                <NativeSelectOption value="30">30 minutes</NativeSelectOption>
                <NativeSelectOption value="60">1 hour</NativeSelectOption>
              </NativeSelect>
            </Field>
            <DialogFooter>
              <Button type="submit" disabled={pending}>
                {pending && <Spinner />}
                Enter Support Mode
              </Button>
            </DialogFooter>
          </FieldGroup>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SupportBanner({
  supportMode,
  onChanged,
}: {
  supportMode: NonNullable<WorkspaceSnapshot["supportMode"]>
  onChanged: () => void
}) {
  const [pending, setPending] = useState(false)
  async function exitSupport() {
    setPending(true)
    const accessToken = await getAccessToken()
    if (!accessToken) {
      setPending(false)
      return
    }
    await revokeSupportMode({
      client: portalClient(accessToken),
      path: { supportSessionId: supportMode.id },
    })
    setPending(false)
    onChanged()
  }
  return (
    <div
      role="status"
      className="flex items-center gap-3 border-b border-primary/30 bg-primary/10 px-4 py-2 text-xs"
    >
      <ShieldCheckIcon aria-hidden="true" className="text-primary" />
      <div className="min-w-0 flex-1">
        <span className="font-medium">Support Mode active</span>
        <span className="ml-2 text-muted-foreground">
          {supportMode.reason} · ends{" "}
          {new Date(supportMode.expiresAt).toLocaleTimeString([], {
            hour: "numeric",
            minute: "2-digit",
          })}
        </span>
      </div>
      <Button variant="outline" size="sm" onClick={exitSupport} disabled={pending}>
        {pending && <Spinner />}
        Exit
      </Button>
    </div>
  )
}

function WorkspaceContextSheet({
  workspace,
}: {
  workspace: WorkspaceSnapshot
}) {
  return (
    <Sheet>
      <SheetTrigger
        render={
          <Button variant="ghost" size="icon" aria-label="Open workspace context" />
        }
      >
        <PanelRightIcon />
      </SheetTrigger>
      <SheetContent>
        <SheetHeader>
          <SheetTitle>Workspace context</SheetTitle>
          <SheetDescription>
            Current authority resolved from PostgreSQL.
          </SheetDescription>
        </SheetHeader>
        <div className="grid gap-5 px-6">
          <ContextRow label="Practice" value={workspace.practice.name} />
          <ContextRow label="Location" value={workspace.location.name} />
          <ContextRow
            label="Actor"
            value={
              workspace.platformOperator
                ? "Platform Operator"
                : workspace.membership?.role ?? "Member"
            }
          />
          <ContextRow
            label="Location scope"
            value={workspace.membership?.locationScope ?? "Global discovery"}
          />
          <Separator />
          <ContextRow
            label="Snapshot"
            value={`v${workspace.version} · ${workspace.schemaVersion}`}
            mono
          />
        </div>
      </SheetContent>
    </Sheet>
  )
}

function ContextRow({
  label,
  value,
  mono = false,
}: {
  label: string
  value: string
  mono?: boolean
}) {
  return (
    <div className="grid gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className={mono ? "font-mono text-xs" : "text-sm font-medium"}>
        {value}
      </span>
    </div>
  )
}

function ConnectionBadge({ state }: { state: ConnectionState }) {
  if (state === "connected") {
    return (
      <Badge variant="secondary">
        <ActivityIcon data-icon="inline-start" />
        Live
      </Badge>
    )
  }
  if (state === "connecting") {
    return (
      <Badge variant="outline">
        <Spinner />
        Connecting
      </Badge>
    )
  }
  return (
    <Badge variant="destructive">
      <WifiOffIcon data-icon="inline-start" />
      Disconnected
    </Badge>
  )
}

function WorkspaceLoading() {
  return (
    <div className="flex min-h-svh w-full">
      <aside className="hidden w-64 border-r bg-sidebar p-4 md:block">
        <Skeleton className="h-10 w-40" />
        <Skeleton className="mt-8 h-8 w-full" />
        <Skeleton className="mt-2 h-8 w-full" />
        <Skeleton className="mt-8 h-32 w-full" />
      </aside>
      <main className="flex flex-1 flex-col">
        <div className="flex h-14 items-center border-b px-4">
          <Skeleton className="h-6 w-52" />
        </div>
        <WorkspaceCenterLoading />
      </main>
    </div>
  )
}

function WorkspaceCenterLoading() {
  return (
    <div
      aria-label="Loading workspace"
      className="flex flex-1 items-center justify-center"
    >
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner />
        Reconstructing authorized workspace
      </div>
    </div>
  )
}

function WorkspaceFailure({
  title,
  description,
  action,
  onAction,
}: {
  title: string
  description: string
  action: string
  onAction: () => void
}) {
  return (
    <main className="flex min-h-svh items-center justify-center bg-muted/40 p-6">
      <Alert className="max-w-md" variant="destructive">
        <WifiOffIcon aria-hidden="true" />
        <AlertTitle>{title}</AlertTitle>
        <AlertDescription>
          <p>{description}</p>
          <Button className="mt-4" variant="outline" onClick={onAction}>
            <RefreshCwIcon data-icon="inline-start" />
            {action}
          </Button>
        </AlertDescription>
      </Alert>
    </main>
  )
}
