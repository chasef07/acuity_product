"use client"

import { createAuthClient } from "better-auth/react"
import { oauthPopupClient } from "better-auth/client/plugins"

export const authClient = createAuthClient({
  plugins: [oauthPopupClient()],
})

export async function getAccessToken(): Promise<string | undefined> {
  const response = await fetch("/api/auth/token", {
    credentials: "same-origin",
    cache: "no-store",
  })
  if (!response.ok) {
    return undefined
  }
  const body = (await response.json()) as { token?: string }
  return body.token
}
