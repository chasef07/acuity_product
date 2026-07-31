import { createClient } from "@/lib/api/generated/client"

export function portalAPIURL(): string {
  const value = process.env.NEXT_PUBLIC_PORTAL_API_URL
  if (!value) {
    throw new Error("NEXT_PUBLIC_PORTAL_API_URL is required")
  }
  return value
}

export function realtimeURL(): string {
  const value = process.env.NEXT_PUBLIC_REALTIME_URL
  if (!value) {
    throw new Error("NEXT_PUBLIC_REALTIME_URL is required")
  }
  return value
}

export function portalClient(token?: string) {
  return createClient({
    baseUrl: portalAPIURL(),
    auth: token,
  })
}
