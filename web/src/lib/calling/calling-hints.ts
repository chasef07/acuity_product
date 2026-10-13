export type CallingHintSignal = "hint" | "connected" | "disconnected"

export type CallingHintSource = {
  covers(practiceIDs: readonly string[] | undefined): boolean
  subscribe(listener: (signal: CallingHintSignal) => void): () => void
}

export type CallingHintSink = {
  publish(): void
  setCoverage(practiceID: string | undefined): void
}

export type CallingHintChannel = CallingHintSource & CallingHintSink

export function createCallingHintChannel(): CallingHintChannel {
  const listeners = new Set<(signal: CallingHintSignal) => void>()
  let coverage: string | undefined

  function emit(signal: CallingHintSignal) {
    for (const listener of [...listeners]) listener(signal)
  }

  return {
    covers(practiceIDs) {
      return (
        coverage !== undefined &&
        practiceIDs !== undefined &&
        practiceIDs.every((practiceID) => practiceID === coverage)
      )
    },
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    publish() {
      emit("hint")
    },
    setCoverage(practiceID) {
      if (practiceID === coverage) return
      const previous = coverage
      coverage = practiceID
      if (previous !== undefined) emit("disconnected")
      if (practiceID !== undefined) emit("connected")
    },
  }
}

export const browserCallingHints = createCallingHintChannel()
