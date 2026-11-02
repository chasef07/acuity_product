type TimerID = number

type Clock = {
  setTimeout: (callback: () => void, milliseconds: number) => TimerID
  clearTimeout: (id: TimerID) => void
}

type WorkspaceRequestBudgetOptions = {
  refreshDetails: () => void
  clock: Clock
}

export type WorkspaceRequestBudget = {
  setDetailRefreshMounted: (mounted: boolean) => void
  signalDetailRefresh: () => void
  stop: () => void
}

const detailDelayMilliseconds = 500

export function createWorkspaceRequestBudget({
  refreshDetails,
  clock,
}: WorkspaceRequestBudgetOptions): WorkspaceRequestBudget {
  let detailTimer: TimerID | undefined
  let detailRefreshMounted = false

  function signalDetailRefresh() {
    if (!detailRefreshMounted || detailTimer !== undefined) return
    detailTimer = clock.setTimeout(() => {
      detailTimer = undefined
      refreshDetails()
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
