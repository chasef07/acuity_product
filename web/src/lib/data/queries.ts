import {
  type InfiniteData,
  type QueryKey,
  type QueryState,
  hashKey,
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query"
import { useCallback, useEffect, useRef, useState } from "react"

import {
  type PortalFailure,
  type PortalResult,
  type PortalSend,
  type PortalTransport,
  accessDenied,
  portalRequest,
} from "../clients/portal-request"

type Live = "workspace" | "review-folders" | "task-detail" | "analytics"

const analyticsRefreshMs = 5_000

type Identity = { readonly [field: string]: string | number | boolean | undefined }

type RefreshFieldName =
  | "revision"
  | "version"
  | "hint"
  | "generation"
  | `${string}Revision`
  | `${string}Version`
  | `${string}Hint`
  | `${string}Generation`

type IdentityOnly<I> = [Extract<keyof I, RefreshFieldName>] extends [never]
  ? unknown
  : { readonly identityMustNotCarryRefreshFields: Extract<keyof I, RefreshFieldName> }

export type PortalQuery<T> =
  | { status: "loading" }
  | { status: "ready"; data: T; refreshing: boolean; refreshFailure?: PortalFailure }
  | { status: "failed"; failure: PortalFailure }

type PortalRead<T> = PortalQuery<T> & { retry: () => Promise<void> }

type PortalPages<T, P> = PortalRead<T> & {
  behind: boolean
  loadingMore: boolean
  moreFailure?: PortalFailure
  loadMore: () => Promise<P | undefined>
  update: (change: (page: P) => P) => void
}

type ReadOptions<I extends Identity> = {
  live?: Live
  name: string
  identity: I & IdentityOnly<I>
  enabled?: boolean
}

export function useRead<const I extends Identity, T>({
  live,
  name,
  identity,
  enabled = true,
  read,
}: ReadOptions<I> & {
  read: (identity: I, transport: PortalTransport) => Promise<PortalResult<T>>
}): PortalRead<T> {
  const queryKey = [live ?? "manual", name, identity]
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) => settle((transport) => read(identity, transport), signal),
    enabled,
  })
  const retry = useRetry(query.refetch, queryKey, enabled)
  return { ...portalQuery(query, query.isFetching, query.isRefetchError), retry }
}

export function usePages<const I extends Identity, P extends { nextCursor: string }, T>({
  live,
  name,
  identity,
  enabled = true,
  hold,
  read,
  combine,
}: ReadOptions<I> & {
  hold?: boolean
  read: (identity: I, cursor: string, transport: PortalTransport) => Promise<PortalResult<P>>
  combine: (pages: P[]) => T
}): PortalPages<T, P> {
  const client = useQueryClient()
  const queryKey = [live ?? "manual", name, identity]
  const queryHash = hashKey(queryKey)
  const select = useCallback((data: InfiniteData<P, string>) => combine(data.pages), [combine])
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) =>
      settle((transport) => read(identity, pageParam, transport), signal),
    initialPageParam: "",
    getNextPageParam: (page: P) => page.nextCursor || undefined,
    enabled: (cached) => enabled && !(hold && cached.state.data !== undefined),
    ...(hold === undefined ? {} : { staleTime: Infinity }),
    select,
  })
  const refetch = useRetry(query.refetch, queryKey, enabled)
  const pages = () => client.getQueryData<InfiniteData<P, string>>(queryKey)?.pages
  const holding = Boolean(hold) && query.data !== undefined
  const [behindHash, setBehindHash] = useState<string>()
  const behind = behindHash === queryHash
  const markBehind = useCallback(async () => setBehindHash(queryHash), [queryHash])

  useEffect(() => {
    if (!holding) return
    const cache = client.getQueryCache()
    const held = cache.get(queryHash)
    if (held && refreshInFlight(held.state)) void held.cancel({ revert: true }).then(markBehind)
    return cache.subscribe((event) => {
      if (event.type === "updated" && event.action.type === "invalidate" && event.query.queryHash === queryHash) {
        void markBehind()
      }
    })
  }, [client, holding, markBehind, queryHash])

  useEffect(() => {
    if (holding || !behind) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- releasing the hold ends this behind mark
    setBehindHash(undefined)
    if (refreshInFlight(client.getQueryCache().get(queryHash)?.state)) return
    void client.invalidateQueries({ predicate: (cached) => cached.queryHash === queryHash })
  }, [behind, client, holding, queryHash])

  async function loadMore() {
    if (query.isFetchingNextPage) return undefined
    if (query.isFetching) await query.refetch({ cancelRefetch: false })
    const loaded = pages()
    if (!loaded?.at(-1)?.nextCursor) return undefined
    await query.fetchNextPage()
    const after = pages()
    return after?.length === loaded.length + 1 ? after[loaded.length] : undefined
  }

  return {
    ...portalQuery(query, query.isFetching && !query.isFetchingNextPage, query.isRefetchError),
    retry: holding ? markBehind : refetch,
    behind: holding && behind,
    loadingMore: query.isFetchingNextPage,
    ...(query.isFetchNextPageError ? { moreFailure: failureOf(query.error) } : {}),
    loadMore,
    update: (change) =>
      client.setQueryData<InfiniteData<P, string>>(
        queryKey,
        (data) => data && { ...data, pages: data.pages.map(change) },
      ),
  }
}

export function readFailure(query: PortalQuery<unknown>) {
  return query.status === "failed"
    ? query.failure
    : query.status === "ready" ? query.refreshFailure : undefined
}

export function useLiveRefresh(signals: {
  workspaceVersion: number | undefined
  taskCounts: string
  detailRevision: number
}) {
  const client = useQueryClient()
  const seen = useRef(signals)
  const analytics = useRef<{ refreshedAt: number; trailing?: ReturnType<typeof setTimeout> }>({ refreshedAt: 0 })
  const { workspaceVersion, taskCounts, detailRevision } = signals
  useEffect(() => () => clearTimeout(analytics.current.trailing), [])
  useEffect(() => {
    const previous = seen.current
    seen.current = { workspaceVersion, taskCounts, detailRevision }
    const stale = new Set<Live>()
    if (previous.workspaceVersion !== workspaceVersion) {
      stale.add("workspace")
      stale.add("review-folders")
      const throttle = analytics.current
      const refreshAnalytics = () => {
        throttle.trailing = undefined
        throttle.refreshedAt = Date.now()
        void client.invalidateQueries({ queryKey: ["analytics"] })
      }
      const wait = throttle.refreshedAt + analyticsRefreshMs - Date.now()
      if (wait <= 0) refreshAnalytics()
      else throttle.trailing ??= setTimeout(refreshAnalytics, wait)
    }
    if (previous.taskCounts !== taskCounts) stale.add("review-folders")
    if (previous.detailRevision !== detailRevision) stale.add("task-detail")
    for (const live of stale) void client.invalidateQueries({ queryKey: [live] })
  }, [client, workspaceVersion, taskCounts, detailRevision])
}

function useRetry(refetch: () => Promise<unknown>, queryKey: QueryKey, enabled: boolean) {
  const client = useQueryClient()
  const current = useRef({ queryKey, enabled })
  useEffect(() => {
    current.current = { queryKey, enabled }
  })
  return useCallback(async () => {
    if (!current.current.enabled) return
    await client.cancelQueries({ queryKey: current.current.queryKey, exact: true })
    await refetch()
  }, [client, refetch])
}

function refreshInFlight(state: QueryState | undefined) {
  return state?.fetchStatus === "fetching" && !state.fetchMeta?.fetchMore
}

class PortalReadError extends Error {
  readonly failure: PortalFailure

  constructor(failure: PortalFailure) {
    super(failure.kind)
    this.failure = failure
  }
}

async function settle<T>(send: PortalSend<T>, signal: AbortSignal) {
  const outcome = await portalRequest(send, signal)
  if (!outcome.ok) throw new PortalReadError(outcome.failure)
  return outcome.data
}

function failureOf(error: unknown): PortalFailure {
  return error instanceof PortalReadError
    ? error.failure
    : { kind: "unavailable", retryable: true }
}

function portalQuery<T>(
  query: { data: T | undefined; status: "pending" | "error" | "success"; error: unknown },
  refreshing: boolean,
  refreshFailed: boolean,
): PortalQuery<T> {
  const failure = query.status === "error" ? failureOf(query.error) : undefined
  if (query.data === undefined || (failure && refreshFailed && accessDenied(failure))) {
    return failure ? { status: "failed", failure } : { status: "loading" }
  }
  return {
    status: "ready",
    data: query.data,
    refreshing,
    ...(failure && refreshFailed ? { refreshFailure: failure } : {}),
  }
}
