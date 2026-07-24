"use client"

import { createAuthClient } from "better-auth/react"

export const authClient = createAuthClient()

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
