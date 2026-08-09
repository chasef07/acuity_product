"use client"

import { createAuthClient } from "better-auth/react"
import { oauthPopupClient } from "better-auth/client/plugins"

export const authClient = createAuthClient({
  plugins: [oauthPopupClient()],
  fetchOptions: {
    onSuccess(context) {
      if (new URL(context.request.url).pathname.endsWith("/sign-out")) {
        clearAccessToken()
      }
    },
  },
})

const accessTokenRefreshSkewMilliseconds = 60_000
const accessTokenRefreshRetryMilliseconds = 1_000
const accessTokenRefreshMaximumRetryMilliseconds = 60_000
const accessTokenInvalidationMessage = "clear-access-token"
const accessTokenChannel =
  typeof window !== "undefined" && typeof BroadcastChannel !== "undefined"
    ? new BroadcastChannel("acuity-auth-token")
    : undefined

let accessTokenGeneration = 0
let cachedAccessToken:
  | { token: string; expiresAtMilliseconds: number }
  | undefined
let pendingAccessToken: Promise<string | undefined> | undefined
let accessTokenRefreshRetryAtMilliseconds = 0
let accessTokenRefreshFailureCount = 0

accessTokenChannel?.addEventListener("message", (event) => {
  if (event.data === accessTokenInvalidationMessage) invalidateAccessToken()
})

export async function getAccessToken(): Promise<string | undefined> {
  const now = Date.now()
  if (
    cachedAccessToken &&
    cachedAccessToken.expiresAtMilliseconds - now >
      accessTokenRefreshSkewMilliseconds
  ) {
    return cachedAccessToken.token
  }
  if (pendingAccessToken) return pendingAccessToken
  if (accessTokenRefreshRetryAtMilliseconds > now) {
    return unexpiredCachedAccessToken()
  }

  const generation = accessTokenGeneration
  const request = requestAccessToken(generation)
  pendingAccessToken = request
  try {
    return await request
  } finally {
    if (pendingAccessToken === request) pendingAccessToken = undefined
  }
}

export function clearAccessToken() {
  invalidateAccessToken()
  accessTokenChannel?.postMessage(accessTokenInvalidationMessage)
}

function invalidateAccessToken() {
  accessTokenGeneration += 1
  cachedAccessToken = undefined
  pendingAccessToken = undefined
  accessTokenRefreshRetryAtMilliseconds = 0
  accessTokenRefreshFailureCount = 0
}

async function requestAccessToken(
  generation: number,
  retry = true,
): Promise<string | undefined> {
  const response = await fetch("/api/auth/token", {
    credentials: "same-origin",
    cache: "no-store",
  }).catch(() => undefined)
  if (generation !== accessTokenGeneration) return undefined
  if (!response) return recoverFromTransientFailure(generation, retry)
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) {
      clearAccessToken()
      return undefined
    }
    if (response.status === 429 || response.status >= 500) {
      return recoverFromTransientFailure(generation, retry, response)
    }
    return undefined
  }

  const body = (await response.json().catch(() => undefined)) as
    | { token?: unknown }
    | undefined
  if (typeof body?.token !== "string" || !body.token) return undefined

  const expiresAtMilliseconds = tokenExpirationMilliseconds(body.token)
  if (expiresAtMilliseconds && expiresAtMilliseconds > Date.now()) {
    cachedAccessToken = { token: body.token, expiresAtMilliseconds }
  }
  accessTokenRefreshRetryAtMilliseconds = 0
  accessTokenRefreshFailureCount = 0
  return body.token
}

async function recoverFromTransientFailure(
  generation: number,
  retry: boolean,
  response?: Response,
): Promise<string | undefined> {
  const delayMilliseconds = retryDelayMilliseconds(response)
  accessTokenRefreshRetryAtMilliseconds = Date.now() + delayMilliseconds
  accessTokenRefreshFailureCount += 1

  const cachedToken = unexpiredCachedAccessToken()
  if (cachedToken || !retry) return cachedToken

  await new Promise<void>((resolve) =>
    globalThis.setTimeout(resolve, delayMilliseconds),
  )
  if (generation !== accessTokenGeneration) return undefined
  accessTokenRefreshRetryAtMilliseconds = 0
  return requestAccessToken(generation, false)
}

function retryDelayMilliseconds(response?: Response) {
  const retryAfterSeconds = Number(response?.headers.get("X-Retry-After"))
  const retryAfterMilliseconds =
    Number.isFinite(retryAfterSeconds) && retryAfterSeconds > 0
      ? retryAfterSeconds * 1_000
      : 0
  const backoffMilliseconds =
    accessTokenRefreshRetryMilliseconds *
    2 ** Math.min(accessTokenRefreshFailureCount, 5)
  const jitterMilliseconds = Math.floor(Math.random() * 250)
  return Math.min(
    Math.max(retryAfterMilliseconds, backoffMilliseconds) +
      jitterMilliseconds,
    accessTokenRefreshMaximumRetryMilliseconds,
  )
}

function unexpiredCachedAccessToken() {
  if (
    !cachedAccessToken ||
    cachedAccessToken.expiresAtMilliseconds <= Date.now()
  ) {
    return undefined
  }
  return cachedAccessToken.token
}

function tokenExpirationMilliseconds(token: string) {
  const payload = token.split(".")[1]
  if (!payload) return undefined
  try {
    const normalized = payload.replaceAll("-", "+").replaceAll("_", "/")
    const padding = "=".repeat((4 - (normalized.length % 4)) % 4)
    const decoded = JSON.parse(atob(`${normalized}${padding}`)) as {
      exp?: unknown
    }
    return typeof decoded.exp === "number" && Number.isFinite(decoded.exp)
      ? decoded.exp * 1_000
      : undefined
  } catch {
    return undefined
  }
}
