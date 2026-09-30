import { useCallback, useEffect, useEffectEvent, useState } from "react"

import { portalClient } from "../api/client"
import type { ErrorEnvelope } from "../api/generated/types.gen"
import { getAccessTokenResult } from "../auth-client"

export type PortalFailureKind =
  | "signedOut"
  | "unauthenticated"
  | "unauthorized"
  | "missing"
  | "conflict"
  | "busy"
  | "rejected"
  | "unavailable"

export type PortalFailure = {
  kind: PortalFailureKind
  retryable: boolean
  status?: number
  code?: string
  message?: string
  correlationId?: string
}

export type PortalOutcome<T> =
  | { ok: true; data: T }
  | { ok: false; failure: PortalFailure }

export type PortalTransport = {
  client: ReturnType<typeof portalClient>
  signal?: AbortSignal
}

export type PortalResult<T> = {
  data?: T
  error?: unknown
  response?: Response
}

export type PortalSend<T> = (
  transport: PortalTransport,
) => Promise<PortalResult<T>>

export async function portalRequest<T>(
  send: PortalSend<T>,
  signal?: AbortSignal,
): Promise<PortalOutcome<T>> {
  const authentication = await getAccessTokenResult()
  signal?.throwIfAborted()
  if (authentication.status !== "authenticated") {
    return failed(
      authentication.status === "unauthenticated"
        ? { kind: "signedOut", retryable: false }
        : { kind: "unavailable", retryable: true },
    )
  }
  let result: PortalResult<T> | undefined
  try {
    result = await send({ client: portalClient(authentication.token), signal })
  } catch {
    result = undefined
  }
  signal?.throwIfAborted()
  if (result?.data !== undefined) return { ok: true, data: result.data }
  return failed(portalFailure(result?.response?.status, result?.error))
}

export type PortalQuery<T> =
  | { status: "loading" }
  | { status: "ready"; data: T }
  | { status: "failed"; failure: PortalFailure; data?: T }

export function usePortalQuery<T>(
  key: string | null,
  send: PortalSend<T>,
  { keepPrevious = false }: { keepPrevious?: boolean } = {},
): PortalQuery<T> & { retry: () => void; refreshing: boolean } {
  const [attempt, setAttempt] = useState(0)
  const [settled, setSettled] = useState<{
    key: string
    attempt: number
    query: PortalQuery<T>
    ready?: T
  }>()
  const request = useEffectEvent((signal: AbortSignal) =>
    portalRequest(send, signal),
  )
  useEffect(() => {
    if (key === null) return
    const controller = new AbortController()
    request(controller.signal).then(
      (outcome) =>
        setSettled((previous) =>
          outcome.ok
            ? { key, attempt, query: { status: "ready", data: outcome.data }, ready: outcome.data }
            : {
                key,
                attempt,
                query: { status: "failed", failure: outcome.failure },
                ready: previous?.ready,
              },
        ),
      () => undefined,
    )
    return () => controller.abort()
  }, [key, attempt])
  const retry = useCallback(() => setAttempt((value) => value + 1), [])
  const current = settled?.key === key && settled.attempt === attempt
  const shown = current || keepPrevious ? settled : undefined
  const query: PortalQuery<T> = !shown
    ? { status: "loading" }
    : keepPrevious && shown.query.status === "failed" && shown.ready !== undefined
      ? { ...shown.query, data: shown.ready }
      : shown.query
  return { ...query, retry, refreshing: Boolean(shown) && !current && key !== null }
}

function failed(failure: PortalFailure): { ok: false; failure: PortalFailure } {
  return { ok: false, failure }
}

function portalFailure(status: number | undefined, error: unknown): PortalFailure {
  const kind = failureKind(status)
  const envelope = errorEnvelope(error)
  return {
    kind,
    retryable: envelope?.retryable ?? (kind === "busy" || kind === "unavailable"),
    ...(status === undefined ? {} : { status }),
    ...(envelope
      ? {
          code: envelope.code,
          message: envelope.message,
          correlationId: envelope.correlationId,
        }
      : {}),
  }
}

function failureKind(status: number | undefined): PortalFailureKind {
  if (status === 401) return "unauthenticated"
  if (status === 403) return "unauthorized"
  if (status === 404) return "missing"
  if (status === 409) return "conflict"
  if (status === 429) return "busy"
  if (status !== undefined && status >= 400 && status < 500) return "rejected"
  return "unavailable"
}

function errorEnvelope(value: unknown): ErrorEnvelope["error"] | undefined {
  if (typeof value !== "object" || value === null) return undefined
  const error = (value as { error?: unknown }).error
  if (typeof error !== "object" || error === null) return undefined
  const { code, message, correlationId, retryable } = error as Record<string, unknown>
  return typeof code === "string" &&
    typeof message === "string" &&
    typeof correlationId === "string" &&
    typeof retryable === "boolean"
    ? { code, message, correlationId, retryable }
    : undefined
}
