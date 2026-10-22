import {
  flagAgentCallIssue as flagAgentCallIssueRequest,
  getAgentCall,
  getOperatorAiInteractionAnalytics,
  queryAgentCalls,
  queryOperatorAiAnalytics,
  reviewOperatorAiCallIssue,
} from "../api/generated/sdk.gen"
import type {
  CallIntent,
  CallOutcome,
  AgentCallIssueReason,
  AgentCallsPage,
  OperatorAiAnalyticsPage,
  OperatorAiAnalyticsRange,
  OperatorAiAnalyticsSummary,
  OperatorAiCallIssueOutcome,
} from "../api/generated/types.gen"
import { readFailure, usePages, useRead } from "../data/queries"
import { portalRequest } from "./portal-request"

export function useAgentCalls({
  practiceID,
  locationID,
  range,
  phone,
  flaggedOnly,
  needsKnowledgeOnly = false,
  intent,
  outcome,
}: {
  practiceID: string
  locationID: string
  range: OperatorAiAnalyticsRange
  phone: string
  flaggedOnly: boolean
  needsKnowledgeOnly?: boolean
  intent?: CallIntent
  outcome?: CallOutcome
}) {
  return usePages({
    live: "workspace",
    name: "agent-calls",
    identity: { practiceID, locationID, range, phone, flaggedOnly, needsKnowledgeOnly, intent, outcome },
    read: (identity, cursor, transport) =>
      queryAgentCalls({
        ...transport,
        body: {
          practiceId: identity.practiceID,
          locationId: identity.locationID || undefined,
          range: identity.range,
          phone: identity.phone,
          flaggedOnly: identity.flaggedOnly,
          needsKnowledgeOnly: identity.needsKnowledgeOnly,
          intent: identity.intent,
          outcome: identity.outcome,
          limit: 50,
          cursor: cursor || undefined,
        },
      }),
    combine: combineCallPages<AgentCallsPage>,
  })
}

export function useAgentCall(interactionID: string) {
  return useRead({
    name: "agent-call",
    identity: { interactionID },
    read: (identity, transport) =>
      getAgentCall({ ...transport, path: { interactionId: identity.interactionID } }),
  })
}

export function flagAgentCallIssue(
  interactionID: string,
  reason: AgentCallIssueReason,
  note: string,
) {
  return portalRequest((transport) =>
    flagAgentCallIssueRequest({
      ...transport,
      path: { interactionId: interactionID },
      body: { reason, note },
    }),
  )
}

export type AiCallAnalytics = OperatorAiAnalyticsPage & {
  summary: OperatorAiAnalyticsSummary
}

export type AiCallLedger = {
  state: "loading" | "ready" | "denied" | "unavailable"
  data?: AiCallAnalytics
  refreshFailed: boolean
  nextPage: "idle" | "loading" | "unavailable"
  loadNextPage: () => Promise<OperatorAiAnalyticsPage | undefined>
  update: (change: (shown: OperatorAiAnalyticsPage) => OperatorAiAnalyticsPage) => void
  retry: () => void
}

export function useAiCallAnalytics({
  practiceID,
  locationID,
  range,
  enabled,
  intent,
  outcome,
  failuresOnly,
  failingJudge,
  judgeResult,
}: {
  practiceID: string
  locationID: string
  range: OperatorAiAnalyticsRange
  enabled: boolean
  intent?: CallIntent
  outcome?: CallOutcome
  failuresOnly?: boolean
  failingJudge?: string
  judgeResult?: "pass" | "unjudged"
}): AiCallLedger {
  const pages = usePages({
    live: "workspace",
    name: "ai-call-analytics",
    identity: { practiceID, locationID, range, intent, outcome, failuresOnly, failingJudge, judgeResult },
    enabled: Boolean(practiceID) && enabled,
    read: (identity, cursor, transport) =>
      queryOperatorAiAnalytics({
        ...transport,
        body: {
          practiceId: identity.practiceID,
          locationId: identity.locationID || undefined,
          range: identity.range,
          intent: identity.intent,
          outcome: identity.outcome,
          failuresOnly: identity.failuresOnly || undefined,
          failingJudge: identity.failingJudge || undefined,
          judgeResult: identity.judgeResult,
          cursor: cursor || undefined,
          limit: 50,
        },
      }),
    combine: combineCallPages<OperatorAiAnalyticsPage>,
  })
  const failure = readFailure(pages)
  const denied =
    failure?.kind === "signedOut" ||
    failure?.kind === "unauthenticated" ||
    failure?.kind === "unauthorized" ||
    pages.moreFailure?.kind === "signedOut" ||
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
    refreshFailed: Boolean(data) && pages.status === "ready" && Boolean(pages.refreshFailure),
    nextPage: pages.loadingMore
      ? "loading"
      : pages.moreFailure
        ? "unavailable"
        : "idle",
    loadNextPage: pages.loadMore,
    update: pages.update,
    retry: pages.retry,
  }
}

export function useAiCallEvidence(interactionID: string) {
  return useRead({
    name: "ai-call",
    identity: { interactionID },
    enabled: Boolean(interactionID),
    read: (identity, transport) =>
      getOperatorAiInteractionAnalytics({
        ...transport,
        path: { interactionId: identity.interactionID },
      }),
  })
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

function combineCallPages<P extends { calls: unknown[]; nextCursor: string }>(pages: P[]): P {
  return {
    ...pages[0],
    calls: pages.flatMap((page) => page.calls),
    nextCursor: pages.at(-1)!.nextCursor,
  }
}
