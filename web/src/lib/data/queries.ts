import {
  type InfiniteData,
  type QueryKey,
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query"
import { useCallback, useEffect, useRef } from "react"

import {
  type PortalFailure,
  type PortalResult,
  type PortalSend,
  type PortalTransport,
  portalRequest,
} from "../clients/portal-request"

type Live = "workspace" | "review-folders" | "task-detail"

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
  read,
  combine,
}: ReadOptions<I> & {
  read: (identity: I, cursor: string, transport: PortalTransport) => Promise<PortalResult<P>>
  combine: (pages: P[]) => T
}): PortalPages<T, P> {
  const client = useQueryClient()
  const queryKey = [live ?? "manual", name, identity]
  const select = useCallback((data: InfiniteData<P, string>) => combine(data.pages), [combine])
  const query = useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) =>
      settle((transport) => read(identity, pageParam, transport), signal),
    initialPageParam: "",
    getNextPageParam: (page: P) => page.nextCursor || undefined,
    enabled,
    select,
  })
  const retry = useRetry(query.refetch, queryKey, enabled)
  const pages = () => client.getQueryData<InfiniteData<P, string>>(queryKey)?.pages

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
    retry,
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
  const { workspaceVersion, taskCounts, detailRevision } = signals
  useEffect(() => {
    const previous = seen.current
    seen.current = { workspaceVersion, taskCounts, detailRevision }
    const stale = new Set<Live>()
    if (previous.workspaceVersion !== workspaceVersion) {
      stale.add("workspace")
      stale.add("review-folders")
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
  if (query.data === undefined || (failure && refreshFailed && accessLost(failure))) {
    return failure ? { status: "failed", failure } : { status: "loading" }
  }
  return {
    status: "ready",
    data: query.data,
    refreshing,
    ...(failure && refreshFailed ? { refreshFailure: failure } : {}),
  }
}

function accessLost(failure: PortalFailure) {
  return (
    failure.kind === "signedOut" ||
    failure.kind === "unauthenticated" ||
    failure.kind === "unauthorized"
  )
}
