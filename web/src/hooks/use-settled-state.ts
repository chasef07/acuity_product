import { useEffect, useState } from "react"

export const stateSettleMilliseconds = 1_000

export function useSettledState<State>(state: State) {
  const [observed, setObserved] = useState({ state, settled: true })
  if (observed.state !== state) setObserved({ state, settled: false })
  useEffect(() => {
    if (observed.settled) return
    const timer = window.setTimeout(
      () => setObserved((current) => current.state === observed.state ? { ...current, settled: true } : current),
      stateSettleMilliseconds,
    )
    return () => window.clearTimeout(timer)
  }, [observed])
  return observed.state === state && observed.settled
}
