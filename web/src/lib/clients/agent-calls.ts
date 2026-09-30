import { useEffect, useEffectEvent, useState } from "react"

import {
  flagAgentCallIssue as flagAgentCallIssueRequest,
  getAgentCall,
  getOperatorAiCallTags,
  getOperatorAiInteractionAnalytics,
  queryAgentCalls,
  queryOperatorAiAnalytics,
  reviewOperatorAiCallIssue,
  setOperatorAiCallTag,
} from "../api/generated/sdk.gen"
import type {
  AgentCallIssueReason,
  AgentCallsPage,
  OperatorAiAnalyticsPage,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallIssueOutcome,
} from "../api/generated/types.gen"
import {
  type PortalFailure,
  type PortalQuery,
  type PortalResult,
  type PortalTransport,
  portalRequest,
  usePortalQuery,
} from "./portal-request"

// Manage agent: a Practice's recent AI calls, one call's transcript, and the
// staff flag that sends a call to Acuity review.

/** Recent calls for the filters; `revision` changes reload the first page. */
export function useAgentCalls({
  practiceID,
  locationID,
  range,
  phone,
  flaggedOnly,
  revision,
}: {
  practiceID: string
  /** Empty means every office in the Practice. */
  locationID: string
  range: OperatorAiAnalyticsRange
  phone: string
  flaggedOnly: boolean
  revision: number
}) {
  return useCallPages<AgentCallsPage>(
    JSON.stringify([practiceID, locationID, range, phone, flaggedOnly, revision]),
    true,
    (transport, cursor) =>
      queryAgentCalls({
        ...transport,
        body: {
          practiceId: practiceID,
          locationId: locationID || undefined,
          range,
          phone,
          flaggedOnly,
          limit: 50,
          cursor,
        },
      }),
    (shown, next) => ({
      calls: [...shown.calls, ...next.calls],
      nextCursor: next.nextCursor,
    }),
  )
}

export function useAgentCall(interactionID: string) {
  return usePortalQuery(`agent-call:${interactionID}`, (transport) =>
    getAgentCall({ ...transport, path: { interactionId: interactionID } }),
  )
}

/** A `conflict` failure means another staff member flagged the call first. */
export function flagAgentCallIssue(interactionID: string, reason: AgentCallIssueReason) {
  return portalRequest((transport) =>
    flagAgentCallIssueRequest({
      ...transport,
      path: { interactionId: interactionID },
      body: { reason },
    }),
  )
}

// AI diagnostics: the Platform Operator's evidence for AI calls, Acuity's
// review of staff flags, and manual tags shared within a Practice.

export type AiCallAnalytics = OperatorAiAnalyticsPage & {
  summary: OperatorAiAnalyticsSummary
}

export type AiCallLedger = {
  state: "loading" | "ready" | "denied" | "unavailable"
  data?: AiCallAnalytics
  nextPage: "idle" | "loading" | "unavailable"
  /** Resolves with the appended page, or undefined when it did not land. */
  loadNextPage: () => Promise<OperatorAiAnalyticsPage | undefined>
  /** Applies a saved tag or review to the calls already shown. */
  update: (change: (shown: OperatorAiAnalyticsPage) => OperatorAiAnalyticsPage) => void
}

/**
 * The call ledger and summary. `revision` changes reload the first page;
 * while `enabled` is false nothing loads and the last snapshot stays.
 * Denied covers a missing session and a 401 or 403 answer.
 */
export function useAiCallAnalytics({
  practiceID,
  locationID,
  range,
  manualTag,
  needsReviewOnly,
  revision,
  enabled,
}: {
  practiceID: string
  /** Empty means every office in the Practice. */
  locationID: string
  range: OperatorAiAnalyticsRange
  /** Empty means every call. */
  manualTag: string
  needsReviewOnly: boolean
  revision: number
  enabled: boolean
}): AiCallLedger {
  const pages = useCallPages<OperatorAiAnalyticsPage>(
    `${practiceID}:${locationID}:${range}:${manualTag}:${needsReviewOnly}:${revision}`,
    Boolean(practiceID) && enabled,
    (transport, cursor) =>
      queryOperatorAiAnalytics({
        ...transport,
        body: {
          practiceId: practiceID,
          locationId: locationID || undefined,
          range,
          manualTag: manualTag || undefined,
          needsReviewOnly,
          cursor,
          limit: 50,
        },
      }),
    (shown, next) => ({
      ...shown,
      calls: [...shown.calls, ...next.calls],
      nextCursor: next.nextCursor,
    }),
  )
  // A signed-out next page denies the whole ledger; other page failures retry.
  const denied =
    pages.status === "failed"
      ? pages.failure.kind === "signedOut" ||
        pages.failure.kind === "unauthenticated" ||
        pages.failure.kind === "unauthorized"
      : pages.moreFailure?.kind === "signedOut" ||
        pages.moreFailure?.kind === "unauthenticated"
  const data: AiCallAnalytics | undefined =
    pages.status === "ready" && pages.data.summary && !denied
      ? { ...pages.data, summary: pages.data.summary }
      : undefined
  return {
    state:
      pages.status === "loading"
        ? "loading"
        : denied
          ? "denied"
          : data
            ? "ready"
            : "unavailable",
    data,
    nextPage: pages.loadingMore
      ? "loading"
      : pages.moreFailure
        ? "unavailable"
        : "idle",
    loadNextPage: pages.loadMore,
    update: pages.update,
  }
}

/** One AI call's evidence; an empty `interactionID` sends nothing. */
export function useAiCallEvidence(interactionID: string) {
  return usePortalQuery(interactionID ? `ai-call:${interactionID}` : null, (transport) =>
    getOperatorAiInteractionAnalytics({ ...transport, path: { interactionId: interactionID } }),
  )
}

export function reviewCallIssue(interactionID: string, outcome: OperatorAiCallIssueOutcome) {
  return portalRequest((transport) =>
    reviewOperatorAiCallIssue({
      ...transport,
      path: { interactionId: interactionID },
      body: { outcome },
    }),
  )
}

export function useCallTags(interactionID: string) {
  return usePortalQuery(`call-tags:${interactionID}`, (transport) =>
    getOperatorAiCallTags({ ...transport, path: { interactionId: interactionID } }),
  )
}

/** Adds or removes one tag, creating it when new; resolves with every tag. */
export function setCallTag(interactionID: string, name: string, applied: boolean) {
  return portalRequest((transport) =>
    setOperatorAiCallTag({
      ...transport,
      path: { interactionId: interactionID },
      body: { name, applied },
    }),
  )
}

type CallPages<P> = PortalQuery<P> & {
  loadingMore: boolean
  moreFailure?: PortalFailure
  /** Appends the next page and resolves with it, or undefined when it did not land. */
  loadMore: () => Promise<P | undefined>
  /** Applies a saved change to the pages already shown. */
  update: (change: (shown: P) => P) => void
}

// Cursor paging over one snapshot per key. A new key shows loading and aborts
// the superseded first page and any next page begun from it; unmount aborts.
// usePortalQuery has no cursor paging, so this stays local.
function useCallPages<P extends { nextCursor: string }>(
  key: string,
  enabled: boolean,
  send: (transport: PortalTransport, cursor?: string) => Promise<PortalResult<P>>,
  append: (shown: P, next: P) => P,
): CallPages<P> {
  const [snapshot, setSnapshot] = useState<{
    key: string
    controller: AbortController
    query: PortalQuery<P>
  }>()
  const [more, setMore] = useState<{
    key: string
    loading: boolean
    failure?: PortalFailure
  }>()
  const request = useEffectEvent((signal: AbortSignal) =>
    portalRequest((transport) => send(transport), signal),
  )
  useEffect(() => {
    if (!enabled) return
    const controller = new AbortController()
    request(controller.signal).then(
      (outcome) => {
        setSnapshot({
          key,
          controller,
          query: outcome.ok
            ? { status: "ready", data: outcome.data }
            : { status: "failed", failure: outcome.failure },
        })
        setMore({ key, loading: false })
      },
      // portalRequest rejects only after this effect aborted it.
      () => undefined,
    )
    return () => controller.abort()
  }, [key, enabled])

  const current = snapshot?.key === key ? snapshot : undefined
  const currentMore = more?.key === key ? more : undefined

  async function loadMore() {
    if (current?.query.status !== "ready" || currentMore?.loading) return
    const { controller } = current
    const cursor = current.query.data.nextCursor
    if (!cursor || controller.signal.aborted) return
    setMore({ key, loading: true })
    const outcome = await portalRequest(
      (transport) => send(transport, cursor),
      controller.signal,
    ).catch(() => undefined)
    if (!outcome) return
    if (!outcome.ok) {
      setMore({ key, loading: false, failure: outcome.failure })
      return
    }
    const next = outcome.data
    setSnapshot((previous) =>
      previous?.controller === controller && previous.query.status === "ready"
        ? {
            ...previous,
            query: { status: "ready", data: append(previous.query.data, next) },
          }
        : previous,
    )
    setMore({ key, loading: false })
    return next
  }

  function update(change: (shown: P) => P) {
    setSnapshot((previous) =>
      previous?.query.status === "ready"
        ? {
            ...previous,
            query: { status: "ready", data: change(previous.query.data) },
          }
        : previous,
    )
  }

  return {
    ...(current?.query ?? { status: "loading" }),
    loadingMore: Boolean(currentMore?.loading),
    moreFailure: currentMore?.failure,
    loadMore,
    update,
  }
}
