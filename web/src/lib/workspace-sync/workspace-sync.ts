export type WorkspaceSyncScope = {
  practiceID: string
  locationID: string
}

export type WorkspaceSyncState = "connecting" | "connected" | "degraded"

type Reconciliation = {
  version: number
  apply: () => void
}

type WorkspaceSyncOptions = {
  realtimeURL: string
  fetch?: typeof fetch
  getToken: () => Promise<string | null | undefined>
  reconcile: (input: {
    scope: WorkspaceSyncScope
    token: string
    signal: AbortSignal
    minimumVersion: number
  }) => Promise<Reconciliation>
  onStateChange: (state: WorkspaceSyncState) => void
  onUnauthorized?: () => void
  isHidden?: () => boolean
  random?: () => number
  sleep?: (milliseconds: number, signal: AbortSignal) => Promise<void>
  timing?: Partial<WorkspaceSyncTiming>
}

type WorkspaceSyncTiming = {
  retryBaseMilliseconds: number
  retryCapMilliseconds: number
  degradedGraceMilliseconds: number
  pollMinimumMilliseconds: number
  pollMaximumMilliseconds: number
}

type DeferredCatchUp = "none" | "hint" | "force"

const defaultTiming: WorkspaceSyncTiming = {
  retryBaseMilliseconds: 500,
  retryCapMilliseconds: 30_000,
  degradedGraceMilliseconds: 3_000,
  pollMinimumMilliseconds: 15_000,
  pollMaximumMilliseconds: 30_000,
}

export type WorkspaceSync = {
  setScope: (scope?: WorkspaceSyncScope) => void
  refresh: () => void
  visibilityChanged: () => void
  stop: () => void
}

export class WorkspaceSyncUnauthorizedError extends Error {}

export function createWorkspaceSync(
  options: WorkspaceSyncOptions,
): WorkspaceSync {
  const timing = { ...defaultTiming, ...options.timing }
  const random = options.random ?? Math.random
  const sleep = options.sleep ?? wait
  let controller: AbortController | undefined
  let scopeKey = ""
  let activeScope: WorkspaceSyncScope | undefined
  let handleVisibility = () => {}

  function stop() {
    scopeKey = ""
    activeScope = undefined
    controller?.abort()
    controller = undefined
    handleVisibility = () => {}
  }

  function start(scope: WorkspaceSyncScope) {
    activeScope = scope
    scopeKey = `${scope.practiceID}:${scope.locationID}`
    controller = new AbortController()
    options.onStateChange("connecting")
    void run(scope, controller.signal)
  }

  function setScope(scope?: WorkspaceSyncScope) {
    const nextKey = scope ? `${scope.practiceID}:${scope.locationID}` : ""
    if (nextKey === scopeKey) return
    stop()
    if (!scope) return
    start(scope)
  }

  function refresh() {
    const scope = activeScope
    if (!scope) return
    controller?.abort()
    controller = undefined
    scopeKey = ""
    start(scope)
  }

  async function run(scope: WorkspaceSyncScope, signal: AbortSignal) {
    let appliedVersion = 0
    let highestHint = 0
    let reconciliation: Promise<void> | undefined
    let hintedReconciliationQueued = false
    let hintRetryFailures = 0
    let hintRetryScheduled = false
    let hintRetryGeneration = 0
    let consecutiveFailures = 0
    let outage = false
    let outageEpoch = 0
    let degraded = false
    let hasConnected = false
    let streamReady = false
    let deferredCatchUp: DeferredCatchUp = "none"

    handleVisibility = () => {
      if (
        signal.aborted ||
        options.isHidden?.() ||
        deferredCatchUp === "none"
      ) {
        return
      }
      resetHintRetry()
      queueDeferredCatchUp()
    }

    function deferCatchUp(force = false) {
      if (force || deferredCatchUp === "force") {
        deferredCatchUp = "force"
      } else {
        deferredCatchUp = "hint"
      }
    }

    function queueDeferredCatchUp() {
      if (
        signal.aborted ||
        options.isHidden?.() ||
        reconciliation ||
        hintedReconciliationQueued
      ) {
        return
      }
      const catchUp = deferredCatchUp
      deferredCatchUp = "none"
      if (catchUp === "force") queueReconciliation(true)
      else if (catchUp === "hint") queueHint(highestHint)
    }

    function beginOutage() {
      if (outage || signal.aborted) return
      outage = true
      const epoch = ++outageEpoch
      void (async () => {
        await sleep(timing.degradedGraceMilliseconds, signal)
        if (signal.aborted || !outage || epoch !== outageEpoch) return
        degraded = true
        options.onStateChange("degraded")
        while (!signal.aborted && outage && epoch === outageEpoch) {
          const pollingDelay =
            timing.pollMinimumMilliseconds +
            Math.floor(
              random() *
                (timing.pollMaximumMilliseconds -
                  timing.pollMinimumMilliseconds),
            )
          await sleep(pollingDelay, signal)
          if (signal.aborted || !outage || epoch !== outageEpoch) return
          if (options.isHidden?.()) {
            deferCatchUp(true)
            continue
          }
          try {
            await reconcile(0, true)
            restoreHealthyStream()
          } catch (error) {
            if (error instanceof WorkspaceSyncUnauthorizedError) {
              options.onUnauthorized?.()
              return
            }
            continue
          }
        }
      })()
    }

    function resetHintRetry() {
      hintRetryFailures = 0
      hintRetryScheduled = false
      hintRetryGeneration += 1
    }

    function markHealthy() {
      const wasDegraded = degraded
      outage = false
      outageEpoch += 1
      degraded = false
      consecutiveFailures = 0
      resetHintRetry()
      return wasDegraded
    }

    function restoreHealthyStream() {
      if (!streamReady) {
        resetHintRetry()
      } else if (markHealthy() || !hasConnected) {
        hasConnected = true
        options.onStateChange("connected")
      }
    }

    function queueHint(version: number) {
      if (signal.aborted) return
      highestHint = Math.max(highestHint, version)
      if (highestHint <= appliedVersion) return
      if (options.isHidden?.()) {
        deferCatchUp()
        return
      }
      if (reconciliation || hintRetryScheduled) {
        return
      }
      queueReconciliation()
    }

    function queueReconciliation(force = false) {
      if (signal.aborted || reconciliation || hintedReconciliationQueued) return
      hintedReconciliationQueued = true
      setTimeout(() => {
        hintedReconciliationQueued = false
        if (!signal.aborted) {
          void reconcile(highestHint, force)
            .then(() => {
              restoreHealthyStream()
            })
            .catch((error: unknown) => {
              if (error instanceof WorkspaceSyncUnauthorizedError) {
                options.onUnauthorized?.()
                return
              }
              beginOutage()
              scheduleHintRetry(force)
            })
        }
      }, 0)
    }

    function scheduleHintRetry(force = false) {
      if (
        signal.aborted ||
        !streamReady ||
        hintRetryScheduled ||
        (!force && highestHint <= appliedVersion)
      ) {
        return
      }
      const ceiling = Math.min(
        timing.retryBaseMilliseconds * 2 ** Math.max(0, hintRetryFailures - 1),
        timing.retryCapMilliseconds,
      )
      const delay =
        hintRetryFailures === 0 ? 0 : Math.floor(random() * ceiling)
      hintRetryFailures += 1
      hintRetryScheduled = true
      const generation = ++hintRetryGeneration
      void (async () => {
        await sleep(delay, signal)
        if (generation !== hintRetryGeneration) return
        hintRetryScheduled = false
        if (!signal.aborted && streamReady) {
          if (force) queueReconciliation(true)
          else queueHint(highestHint)
        }
      })()
    }

    async function reconcile(minimumVersion: number, force = false) {
      highestHint = Math.max(highestHint, minimumVersion)
      if (!force && highestHint <= appliedVersion) return
      if (reconciliation) {
        if (force) deferCatchUp(true)
        return reconciliation
      }

      reconciliation = (async () => {
        const targetVersion = highestHint
        const token = await options.getToken()
        if (!token) {
          throw new WorkspaceSyncUnauthorizedError()
        }
        const candidate = await options.reconcile({
          scope,
          token,
          signal,
          minimumVersion: targetVersion,
        })
        if (signal.aborted || candidate.version < appliedVersion) return
        candidate.apply()
        appliedVersion = candidate.version
      })()
      let succeeded = false
      try {
        await reconciliation
        succeeded = true
      } finally {
        reconciliation = undefined
        if (!signal.aborted && !options.isHidden?.()) {
          if (deferredCatchUp !== "none") queueDeferredCatchUp()
          else if (succeeded && highestHint > appliedVersion) {
            queueHint(highestHint)
          }
        }
      }
    }

    while (!signal.aborted) {
      streamReady = false
      try {
        const streamToken = await options.getToken()
        if (!streamToken) {
          options.onUnauthorized?.()
          return
        }
        const url = new URL("/v1/events", options.realtimeURL)
        url.searchParams.set("practiceId", scope.practiceID)
        url.searchParams.set("locationId", scope.locationID)
        const response = await (options.fetch ?? fetch)(url, {
          headers: {
            accept: "text/event-stream",
            authorization: `Bearer ${streamToken}`,
          },
          signal,
        })
        if (response.status === 401 || response.status === 403) {
          options.onUnauthorized?.()
          return
        }
        if (!response.ok || !response.body) {
          throw new Error("realtime stream is unavailable")
        }

        let ready = false
        for await (const event of readEvents(response.body, signal)) {
          if (
            event.practiceID !== scope.practiceID ||
            event.version < 1
          ) {
            continue
          }
          if (event.type === "hint" && ready) {
            queueHint(event.version)
            continue
          }
          if (event.type !== "ready" || ready) continue
          highestHint = Math.max(highestHint, event.version)
          if (options.isHidden?.()) {
            deferCatchUp(true)
            ready = true
            streamReady = true
            continue
          }
          await reconcile(event.version, true)
          if (signal.aborted) return
          ready = true
          streamReady = true
          markHealthy()
          hasConnected = true
          options.onStateChange("connected")
        }
        if (signal.aborted) return
        throw new Error("realtime stream ended")
      } catch (error) {
        streamReady = false
        if (signal.aborted) return
        if (error instanceof WorkspaceSyncUnauthorizedError) {
          options.onUnauthorized?.()
          return
        }
        beginOutage()
        if (consecutiveFailures > 0) {
          const ceiling = Math.min(
            timing.retryBaseMilliseconds * 2 ** (consecutiveFailures - 1),
            timing.retryCapMilliseconds,
          )
          await sleep(Math.floor(random() * ceiling), signal)
          if (signal.aborted) return
        }
        consecutiveFailures += 1
      }
    }
  }

  return {
    setScope,
    refresh,
    visibilityChanged: () => handleVisibility(),
    stop,
  }
}

function wait(milliseconds: number, signal: AbortSignal) {
  if (signal.aborted) return Promise.resolve()
  return new Promise<void>((resolve) => {
    const timeout = setTimeout(done, milliseconds)
    signal.addEventListener("abort", done, { once: true })
    function done() {
      clearTimeout(timeout)
      signal.removeEventListener("abort", done)
      resolve()
    }
  })
}

async function* readEvents(
  stream: ReadableStream<Uint8Array>,
  signal: AbortSignal,
) {
  const reader = stream.getReader()
  const cancel = () => void reader.cancel()
  signal.addEventListener("abort", cancel, { once: true })
  const decoder = new TextDecoder()
  let buffer = ""
  try {
    while (!signal.aborted) {
      const { value, done } = await reader.read()
      if (done) return
      buffer += decoder.decode(value, { stream: true })
      const blocks = buffer.split(/\r?\n\r?\n/)
      buffer = blocks.pop() ?? ""
      for (const block of blocks) {
        const lines = block.split(/\r?\n/)
        const type = lines
          .find((line) => line.startsWith("event:"))
          ?.slice("event:".length)
          .trim()
        const data = lines
          .filter((line) => line.startsWith("data:"))
          .map((line) => line.slice("data:".length).trimStart())
          .join("\n")
        if (!type || !data) continue
        try {
          const payload = JSON.parse(data) as {
            practiceId?: unknown
            version?: unknown
          }
          if (
            typeof payload.practiceId === "string" &&
            Number.isSafeInteger(payload.version)
          ) {
            yield {
              type,
              practiceID: payload.practiceId,
              version: payload.version as number,
            }
          }
        } catch {
          continue
        }
      }
    }
  } finally {
    signal.removeEventListener("abort", cancel)
    try {
      await reader.cancel()
    } catch {
      // The transport may already have closed or been aborted.
    }
    reader.releaseLock()
  }
}
