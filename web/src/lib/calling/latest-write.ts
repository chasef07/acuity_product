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
        commit: (input: Input) => Promise<Output>
      }
    | undefined
  private running: Promise<WriteResult<Input, Output>> | undefined
  private writeGeneration = 0

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
    commit: (input: Input) => Promise<Output>,
  ): Promise<WriteResult<Input, Output>> {
    this.writeGeneration += 1
    this.next = { generation: this.writeGeneration, input, commit }
    this.running ??= this.flush()
    return this.running
  }

  private async flush(): Promise<WriteResult<Input, Output>> {
    let result: WriteResult<Input, Output> | undefined
    try {
      while (this.next) {
        const current = this.next
        this.next = undefined
        try {
          const output = await current.commit(current.input)
          result = {
            generation: current.generation,
            input: current.input,
            output,
          }
        } catch (error) {
          if (!this.next) throw error
        }
      }
      if (!result) throw new Error("Latest write completed without a result.")
      return result
    } finally {
      this.running = undefined
    }
  }
}
