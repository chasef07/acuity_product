export type CallingHintSignal = "hint" | "connected" | "disconnected"

export type CallingHintSource = {
  live(): boolean
  subscribe(listener: (signal: CallingHintSignal) => void): () => void
}

export type CallingHintSink = {
  publish(): void
  setLive(live: boolean): void
}

export type CallingHintChannel = CallingHintSource & CallingHintSink

export function createCallingHintChannel(): CallingHintChannel {
  const listeners = new Set<(signal: CallingHintSignal) => void>()
  let live = false

  function emit(signal: CallingHintSignal) {
    for (const listener of [...listeners]) listener(signal)
  }

  return {
    live: () => live,
    subscribe(listener) {
      listeners.add(listener)
      return () => {
        listeners.delete(listener)
      }
    },
    publish() {
      emit("hint")
    },
    setLive(next) {
      if (next === live) return
      live = next
      emit(next ? "connected" : "disconnected")
    },
  }
}

export const browserCallingHints = createCallingHintChannel()
