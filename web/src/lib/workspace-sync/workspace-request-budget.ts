type TimerID = number

type Clock = {
  setTimeout: (callback: () => void, milliseconds: number) => TimerID
  clearTimeout: (id: TimerID) => void
}

type WorkspaceRequestBudgetOptions = {
  refreshDetails?: () => void
  clock?: Clock
  detailDelayMilliseconds?: number
}

export type WorkspaceRequestBudget = {
  setDetailRefreshMounted: (mounted: boolean) => void
  signalDetailRefresh: () => void
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
  const detailDelayMilliseconds = options.detailDelayMilliseconds ?? 500
  let detailTimer: TimerID | undefined
  let detailRefreshMounted = false

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
    if (detailTimer !== undefined) clock.clearTimeout(detailTimer)
    detailTimer = undefined
  }

  return {
    setDetailRefreshMounted,
    signalDetailRefresh,
    stop,
  }
}
