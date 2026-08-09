export type WriteResult<Input, Output> = {
  generation: number
  input: Input
  output: Output
}

export class LatestWrite<Input, Output> {
  private next:
    | {
        generation: number
        input: Input
        commit: (input: Input, signal: AbortSignal) => Promise<Output>
      }
    | undefined
  private running: Promise<WriteResult<Input, Output>> | undefined
  private active: AbortController | undefined
  private writeGeneration = 0
  private readonly timeoutMilliseconds: number

  constructor(timeoutMilliseconds = 10_000) {
    this.timeoutMilliseconds = timeoutMilliseconds
  }

  get generation() {
    return this.writeGeneration
  }

  get pending() {
    return Boolean(this.running)
  }

  snapshotIsCurrent(generation: number, writeWasPending: boolean) {
    return (
      !writeWasPending &&
      generation === this.writeGeneration &&
      !this.running
    )
  }

  write(
    input: Input,
    commit: (input: Input, signal: AbortSignal) => Promise<Output>,
  ): Promise<WriteResult<Input, Output>> {
    this.writeGeneration += 1
    this.next = { generation: this.writeGeneration, input, commit }
    this.active?.abort(
      new DOMException("Superseded by a newer write.", "AbortError"),
    )
    this.running ??= this.flush()
    return this.running
  }

  private async flush(): Promise<WriteResult<Input, Output>> {
    let result: WriteResult<Input, Output> | undefined
    try {
      while (this.next) {
        const current = this.next
        this.next = undefined
        const controller = new AbortController()
        this.active = controller
        const timeout = setTimeout(
          () =>
            controller.abort(
              new DOMException("Latest write timed out.", "TimeoutError"),
            ),
          this.timeoutMilliseconds,
        )
        try {
          const output = await Promise.race([
            current.commit(current.input, controller.signal),
            rejectWhenAborted(controller.signal),
          ])
          result = {
            generation: current.generation,
            input: current.input,
            output,
          }
        } catch (error) {
          if (!this.next) throw error
        } finally {
          clearTimeout(timeout)
          if (this.active === controller) this.active = undefined
        }
      }
      if (!result) throw new Error("Latest write completed without a result.")
      return result
    } finally {
      this.running = undefined
    }
  }
}

function rejectWhenAborted(signal: AbortSignal): Promise<never> {
  return new Promise((_, reject) => {
    signal.addEventListener("abort", () => reject(signal.reason), {
      once: true,
    })
  })
}
