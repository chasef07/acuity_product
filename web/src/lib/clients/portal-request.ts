// The one place browser code turns a generated SDK call into a typed outcome.
//
// Convention: components never import transport. Each backend feature has one
// module in `src/lib/clients/` that exports named hooks (`useReviewFolder`) and
// commands (`completeTask`). Those modules call the generated SDK only through
// `portalRequest` or `usePortalQuery`, which own the access token, the
// `portalClient`, aborts, and ErrorEnvelope normalization. Components choose
// copy from `PortalFailure.kind`; they never read tokens or HTTP statuses.
// `signedOut` means no session, so nothing was sent; `unauthenticated` means
// the backend answered 401. Views choose whether those read the same.
//
//   export function completeTask(task: Task) {
//     return portalRequest((transport) =>
//       completeTaskRequest({ ...transport, path: { taskId: task.id }, body: { expectedVersion: task.version } }))
//   }
//
// A read that should wait passes a `null` key to `usePortalQuery`; a read that
// refreshes in place passes `keepPrevious` so the last answer never blanks.
//
// `eslint.config.mjs` forbids transport imports under src/components and src/app.

import { useCallback, useEffect, useEffectEvent, useState } from "react"

import { portalClient } from "../api/client"
import type { ErrorEnvelope } from "../api/generated/types.gen"
import { getAccessTokenResult } from "../auth-client"

export type PortalFailureKind =
  | "signedOut" // no access token, so nothing was sent
  | "unauthenticated" // 401
  | "unauthorized" // 403
  | "missing" // 404
  | "conflict" // 409
  | "busy" // 429
  | "rejected" // any other 4xx
  | "unavailable" // network failure, 5xx, or the token service is unavailable

export type PortalFailure = {
  kind: PortalFailureKind
  retryable: boolean
  /** HTTP status, present only when the backend answered. */
  status?: number
  /** ErrorEnvelope fields, present only when the backend sent one. */
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

/** The `fields` result every generated SDK function resolves with. */
export type PortalResult<T> = {
  data?: T
  error?: unknown
  response?: Response
}

export type PortalSend<T> = (
  transport: PortalTransport,
) => Promise<PortalResult<T>>

/**
 * Acquires the access token, sends one generated SDK call, and normalizes the
 * result. It resolves with an outcome for every answer and failure, and
 * rejects only when `signal` aborts, so a superseded request never reports.
 */
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
  /** With `keepPrevious`, `data` is the last ready answer, if any. */
  | { status: "failed"; failure: PortalFailure; data?: T }

/**
 * Reads one resource for `key`; a `null` key reads no token and sends nothing.
 * A new key or `retry()` shows loading, aborts the superseded request, and
 * sends again; unmount aborts. `key` must change whenever an input to `send`
 * changes. With `keepPrevious`, the last answer stays shown instead of loading
 * (`refreshing` is true while a newer one is in flight), and a failure keeps
 * the last ready `data`.
 */
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
      // portalRequest rejects only after this effect aborted it.
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
