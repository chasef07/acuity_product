import {
  acquireSoftphone,
  confirmCallingMediaReady,
  getCallingCall,
  getCallingState,
  issueCallingMediaToken,
  recordCallingDisposition,
  requestCallingHangup,
  retryOutboundCall,
  setCallingReadiness,
  startOutboundCall,
} from "../api/generated/sdk.gen"
import { getAccessTokenResult } from "../auth-client"
import { portalClient } from "../api/client"
import {
  SoftphoneAdapterError,
  type SoftphoneBackend,
  type SoftphoneFailureKind,
} from "./softphone-runtime"

type FailedResponse = {
  response?: { status?: number }
  error?: { error?: { code?: string } }
}

export function createSoftphoneHTTPAdapter(): SoftphoneBackend {
  return {
    async acquireLease(input) {
      const client = await authenticatedClient()
      const result = await acquireSoftphone({
        client,
        body: { sessionId: input.sessionID, takeover: input.takeover },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "Calling ownership changed. Try again.",
        fallback: "The softphone lease is temporarily unavailable.",
      })
    },

    async writeReadiness(input) {
      const client = await authenticatedClient()
      const result = await setCallingReadiness({ client, body: input }).catch(
        networkFailure,
      )
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "Calling ownership moved to another browser.",
        fallback: input.available
          ? "Availability could not be confirmed."
          : "Pausing calls could not be confirmed.",
      })
    },

    async issueMediaToken(input) {
      const client = await authenticatedClient()
      const result = await issueCallingMediaToken({
        client,
        body: { sessionId: input.sessionID },
      }).catch(networkFailure)
      if (result.data) return result.data.token
      if (result.response?.status === 409) {
        throw new SoftphoneAdapterError(
          "temporary-request",
          "Calling credentials are still being prepared. Trying again shortly.",
          true,
        )
      }
      throw requestFailure(result, {
        conflict: "Calling ownership changed before audio connected.",
        fallback: "Calling credentials are still being prepared. Try again shortly.",
      })
    },

    async readState(input) {
      const client = await authenticatedClient()
      const result = await getCallingState({
        client,
        headers: input.etag ? { "If-None-Match": input.etag } : undefined,
      }).catch(networkFailure)
      const etag = result.response?.headers.get("ETag") ?? undefined
      if (result.response?.status === 304) {
        return { status: "not-modified", etag }
      }
      if (result.data) return { status: "modified", state: result.data, etag }
      throw requestFailure(result, {
        conflict: "Calling state changed. Refresh calling.",
        fallback: "Calling could not refresh. Check your connection and try again.",
      })
    },

    async readCall(callID) {
      const client = await authenticatedClient()
      const result = await getCallingCall({
        client,
        path: { callId: callID },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "The Call changed before it could be refreshed.",
        fallback: "The Call could not refresh. Check your connection and try again.",
      })
    },

    async confirmMedia(input) {
      const client = await authenticatedClient()
      const result = await confirmCallingMediaReady({
        client,
        path: { callId: input.callID },
        body: { sessionId: input.sessionID, mediaToken: input.mediaToken },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "This browser audio no longer matches the current Call.",
        fallback: "Call audio could not be confirmed. Reconnect calling.",
      })
    },

    async startOutbound(input) {
      const client = await authenticatedClient()
      const result = await startOutboundCall({ client, body: input }).catch(
        networkFailure,
      )
      if (result.data) return result.data
      if (result.response?.status === 400) {
        throw new SoftphoneAdapterError(
          "conflict",
          "The destination is not supported for outbound calling.",
        )
      }
      throw requestFailure(result, {
        conflict: "Another Call is pending or active.",
        fallback: "The outbound Call could not be started. Try again.",
      })
    },

    async hangup(input) {
      const client = await authenticatedClient()
      const result = await requestCallingHangup({
        client,
        path: { callId: input.callID },
        body: { sessionId: input.sessionID },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "Calling ownership or the Call state changed before End.",
        fallback: "End was not committed. Check your connection and try again.",
      })
    },

    async retry(input) {
      const client = await authenticatedClient()
      const result = await retryOutboundCall({
        client,
        path: { callId: input.callID },
        body: {
          sessionId: input.sessionID,
          idempotencyKey: input.idempotencyKey,
        },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "Another Call is pending or active.",
        fallback: "The retry could not be started. Try again.",
      })
    },

    async dispose(input) {
      const client = await authenticatedClient()
      const result = await recordCallingDisposition({
        client,
        path: { callId: input.callID },
        body: { sessionId: input.sessionID, outcome: input.outcome },
      }).catch(networkFailure)
      if (result.data) return result.data
      throw requestFailure(result, {
        conflict: "The Call outcome changed before it could be saved.",
        fallback: "The Call outcome could not be saved. Try again.",
      })
    },
  }
}

async function authenticatedClient() {
  const authentication = await getAccessTokenResult()
  if (authentication.status === "authenticated") {
    return portalClient(authentication.token)
  }
  if (authentication.status === "unauthenticated") {
    throw new SoftphoneAdapterError(
      "authentication",
      "Your authentication needs to be refreshed.",
    )
  }
  throw new SoftphoneAdapterError(
    "temporary-request",
    "Calling could not refresh. Check your connection and try again.",
    true,
  )
}

function networkFailure(): never {
  throw new SoftphoneAdapterError(
    "temporary-request",
    "Calling could not reach the service. Check your connection and try again.",
    true,
  )
}

function requestFailure(
  result: FailedResponse,
  copy: { conflict: string; fallback: string },
) {
  const status = result.response?.status
  if (status === 401) {
    return new SoftphoneAdapterError(
      "authentication",
      "Your authentication needs to be refreshed.",
    )
  }
  if (status === 403) {
    return new SoftphoneAdapterError(
      "access",
      "You do not have calling access for this practice.",
      false,
    )
  }
  if (status === 409) {
    return new SoftphoneAdapterError("conflict", copy.conflict)
  }
  const kind: SoftphoneFailureKind =
    !status || status === 429 || status >= 500
      ? "temporary-request"
      : "conflict"
  return new SoftphoneAdapterError(kind, copy.fallback, kind === "temporary-request")
}
