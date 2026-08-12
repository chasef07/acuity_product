export type OwnerHeartbeatResult = "owner" | "lost" | "retry"

type TimerID = number

type Clock = {
  setTimeout: (callback: () => void, milliseconds: number) => TimerID
  clearTimeout: (id: TimerID) => void
}

type CallingOwnerLoopOptions = {
  heartbeat: () => Promise<OwnerHeartbeatResult>
  refresh: () => Promise<void>
  onOwnershipLost: () => Promise<void>
  isHidden: () => boolean
  random?: () => number
  clock?: Clock
  timing?: Partial<CallingOwnerLoopTiming>
}

type CallingOwnerLoopTiming = {
  heartbeatMinimumMilliseconds: number
  heartbeatMaximumMilliseconds: number
  heartbeatRetryMinimumMilliseconds: number
  heartbeatRetryMaximumMilliseconds: number
  visibleRefreshMinimumMilliseconds: number
  visibleRefreshMaximumMilliseconds: number
  hiddenRefreshMinimumMilliseconds: number
  hiddenRefreshMaximumMilliseconds: number
}

const defaultTiming: CallingOwnerLoopTiming = {
  heartbeatMinimumMilliseconds: 6_000,
  heartbeatMaximumMilliseconds: 7_000,
  heartbeatRetryMinimumMilliseconds: 500,
  heartbeatRetryMaximumMilliseconds: 1_000,
  visibleRefreshMinimumMilliseconds: 4_000,
  visibleRefreshMaximumMilliseconds: 6_000,
  hiddenRefreshMinimumMilliseconds: 8_000,
  hiddenRefreshMaximumMilliseconds: 12_000,
}

export type CallingOwnerLoop = {
  start: () => void
  stop: () => void
  incomingMedia: () => Promise<void>
  visibilityChanged: () => Promise<void>
}

export function createCallingOwnerLoop(
  options: CallingOwnerLoopOptions,
): CallingOwnerLoop {
  const timing = { ...defaultTiming, ...options.timing }
  const random = options.random ?? Math.random
  const clock = options.clock ?? {
    setTimeout: (callback, milliseconds) =>
      window.setTimeout(callback, milliseconds),
    clearTimeout: (id) => window.clearTimeout(id),
  }
  let running = false
  let heartbeatTimer: TimerID | undefined
  let refreshTimer: TimerID | undefined
  let heartbeat: Promise<void> | undefined
  let refresh: Promise<void> | undefined

  function stop() {
    running = false
    if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    heartbeatTimer = undefined
    refreshTimer = undefined
  }

  function scheduleHeartbeat(retry = false) {
    if (heartbeatTimer !== undefined) clock.clearTimeout(heartbeatTimer)
    const delay = retry
      ? jitter(
          timing.heartbeatRetryMinimumMilliseconds,
          timing.heartbeatRetryMaximumMilliseconds,
          random,
        )
      : jitter(
          timing.heartbeatMinimumMilliseconds,
          timing.heartbeatMaximumMilliseconds,
          random,
        )
    heartbeatTimer = clock.setTimeout(() => {
      heartbeatTimer = undefined
      void runHeartbeat()
    }, delay)
  }

  function scheduleRefresh() {
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    const delay = options.isHidden()
      ? jitter(
          timing.hiddenRefreshMinimumMilliseconds,
          timing.hiddenRefreshMaximumMilliseconds,
          random,
        )
      : jitter(
          timing.visibleRefreshMinimumMilliseconds,
          timing.visibleRefreshMaximumMilliseconds,
          random,
        )
    refreshTimer = clock.setTimeout(() => {
      refreshTimer = undefined
      void runRefresh().finally(() => {
        if (running) scheduleRefresh()
      })
    }, delay)
  }

  function runHeartbeat() {
    heartbeat ??= (async () => {
      const result = await options.heartbeat()
      if (!running) return
      if (result === "lost") {
        stop()
        await options.onOwnershipLost()
        return
      }
      scheduleHeartbeat(result === "retry")
    })().finally(() => {
      heartbeat = undefined
    })
    return heartbeat
  }

  function runRefresh() {
    refresh ??= options.refresh().finally(() => {
      refresh = undefined
    })
    return refresh
  }

  async function refreshFromSignal() {
    if (!running) return
    if (refreshTimer !== undefined) clock.clearTimeout(refreshTimer)
    refreshTimer = undefined
    await runRefresh()
    if (running) scheduleRefresh()
  }

  function start() {
    if (running) return
    running = true
    scheduleHeartbeat()
    scheduleRefresh()
  }

  return {
    start,
    stop,
    incomingMedia: refreshFromSignal,
    visibilityChanged: refreshFromSignal,
  }
}

function jitter(minimum: number, maximum: number, random: () => number) {
  return Math.min(
    maximum,
    minimum + Math.floor(random() * (maximum - minimum + 1)),
  )
}
