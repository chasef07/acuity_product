import { portalClient } from "../api/client"
import type { ErrorEnvelope, InsuranceValidationError } from "../api/generated/types.gen"
import { getAccessTokenResult } from "../auth-client"

type PortalFailureKind =
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
  entryId?: string
  errors?: InsuranceValidationError[]
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

export function accessDenied(failure?: PortalFailure): boolean {
  return (
    failure?.kind === "signedOut" ||
    failure?.kind === "unauthenticated" ||
    failure?.kind === "unauthorized"
  )
}

function failed(failure: PortalFailure): { ok: false; failure: PortalFailure } {
  return { ok: false, failure }
}

export function portalFailure(status: number | undefined, error: unknown): PortalFailure {
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
          ...(envelope.entryId === undefined ? {} : { entryId: envelope.entryId }),
          ...(envelope.errors === undefined ? {} : { errors: envelope.errors }),
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
  const { code, message, correlationId, retryable, entryId, errors } = error as Record<string, unknown>
  return typeof code === "string" &&
    typeof message === "string" &&
    typeof correlationId === "string" &&
    typeof retryable === "boolean"
    ? {
        code,
        message,
        correlationId,
        retryable,
        ...(typeof entryId === "string" ? { entryId } : {}),
        ...(Array.isArray(errors) && errors.every(validationError) ? { errors } : {}),
      }
    : undefined
}

function validationError(value: unknown): value is InsuranceValidationError {
  if (typeof value !== "object" || value === null) return false
  const fields = value as Record<string, unknown>
  return ["region", "table", "planId", "rule", "message"].every((field) => typeof fields[field] === "string")
}
