type TimerID = number

type Clock = {
  setTimeout: (callback: () => void, milliseconds: number) => TimerID
  clearTimeout: (id: TimerID) => void
}

type WorkspaceRequestBudgetOptions = {
  refreshDetails?: () => void
  isHidden?: () => boolean
  clock?: Clock
  aiIntervalMilliseconds?: number
  detailDelayMilliseconds?: number
}

export type WorkspaceRequestBudget = {
  setAIRefresh: (
    scopeKey: string,
    refresh: () => Promise<void> | void,
  ) => void
  setDetailRefreshMounted: (mounted: boolean) => void
  signalDetailRefresh: () => void
  visibilityChanged: () => void
  stop: () => void
}

export function createWorkspaceRequestBudget(
  options: WorkspaceRequestBudgetOptions = {},
): WorkspaceRequestBudget {
  const clock = options.clock ?? {
    setTimeout: (callback, milliseconds) =>
      window.setTimeout(callback, milliseconds),
    clearTimeout: (id) => window.clearTimeout(id),
  }
  const aiIntervalMilliseconds = options.aiIntervalMilliseconds ?? 30_000
  const detailDelayMilliseconds = options.detailDelayMilliseconds ?? 500
  let aiScopeKey = ""
  let aiRefresh: (() => Promise<void> | void) | undefined
  let aiTimer: TimerID | undefined
  let detailTimer: TimerID | undefined
  let detailRefreshMounted = false
  let aiGeneration = 0
  let hiddenAIRefreshPending = false

  function clearAI() {
    if (aiTimer !== undefined) clock.clearTimeout(aiTimer)
    aiTimer = undefined
  }

  function scheduleAI(delay: number, generation: number) {
    clearAI()
    aiTimer = clock.setTimeout(() => {
      aiTimer = undefined
      if (options.isHidden?.()) {
        hiddenAIRefreshPending = true
        return
      }
      hiddenAIRefreshPending = false
      void Promise.resolve(aiRefresh?.()).finally(() => {
        if (generation === aiGeneration && aiScopeKey) {
          scheduleAI(aiIntervalMilliseconds, generation)
        }
      })
    }, delay)
  }

  function setAIRefresh(
    scopeKey: string,
    refresh: () => Promise<void> | void,
  ) {
    aiRefresh = refresh
    if (scopeKey === aiScopeKey) return
    aiScopeKey = scopeKey
    aiGeneration += 1
    hiddenAIRefreshPending = false
    clearAI()
    if (scopeKey) scheduleAI(0, aiGeneration)
  }

  function signalDetailRefresh() {
    if (!detailRefreshMounted || detailTimer !== undefined) return
    detailTimer = clock.setTimeout(() => {
      detailTimer = undefined
      options.refreshDetails?.()
    }, detailDelayMilliseconds)
  }

  function setDetailRefreshMounted(mounted: boolean) {
    detailRefreshMounted = mounted
  }

  function stop() {
    aiScopeKey = ""
    aiGeneration += 1
    clearAI()
    if (detailTimer !== undefined) clock.clearTimeout(detailTimer)
    detailTimer = undefined
    hiddenAIRefreshPending = false
  }

  function visibilityChanged() {
    if (
      options.isHidden?.() ||
      !hiddenAIRefreshPending ||
      !aiScopeKey
    ) {
      return
    }
    hiddenAIRefreshPending = false
    scheduleAI(0, aiGeneration)
  }

  return {
    setAIRefresh,
    setDetailRefreshMounted,
    signalDetailRefresh,
    visibilityChanged,
    stop,
  }
}
