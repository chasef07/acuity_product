import assert from "node:assert/strict"
import test from "node:test"

import { LatestWrite } from "./latest-write.ts"

function deferred() {
  let resolve = () => {}
  const promise = new Promise<void>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

test("readiness writes are serialized and settle on the latest requested state", async () => {
  const releaseFirst = deferred()
  const writer = new LatestWrite<boolean, boolean>()
  let committed = true
  let inFlight = 0
  let maxInFlight = 0
  const commit = async (available: boolean, signal: AbortSignal) => {
    inFlight += 1
    maxInFlight = Math.max(maxInFlight, inFlight)
    try {
      if (!available) {
        await Promise.race([
          releaseFirst.promise,
          new Promise<never>((_, reject) => {
            signal.addEventListener("abort", () => reject(signal.reason), {
              once: true,
            })
          }),
        ])
      }
      committed = available
      return committed
    } finally {
      inFlight -= 1
    }
  }

  const first = writer.write(false, commit)
  await Promise.resolve()
  const latest = writer.write(true, commit)
  await Promise.resolve()
  releaseFirst.resolve()

  const [firstResult, latestResult] = await Promise.all([first, latest])
  assert.equal(maxInFlight, 1)
  assert.equal(committed, true)
  assert.equal(firstResult.input, true)
  assert.equal(firstResult.output, true)
  assert.deepEqual(latestResult, firstResult)
})

test("a poll cannot repaint availability across a newer readiness write", async () => {
  const releaseWrite = deferred()
  const writer = new LatestWrite<boolean, boolean>()
  const beforeWrite = writer.generation
  const write = writer.write(true, async (available) => {
    await releaseWrite.promise
    return available
  })

  assert.equal(writer.snapshotIsCurrent(beforeWrite, false), false)
  const duringWrite = writer.generation
  assert.equal(writer.snapshotIsCurrent(duringWrite, true), false)

  releaseWrite.resolve()
  await write
  assert.equal(writer.snapshotIsCurrent(duringWrite, true), false)
  assert.equal(writer.snapshotIsCurrent(writer.generation, false), true)
})

test("a newer write aborts a stalled commit before it runs", async () => {
  const writer = new LatestWrite<boolean, boolean>()
  let firstAborted = false
  const first = writer.write(
    false,
    async (_available, signal?: AbortSignal) => {
      await new Promise<void>((resolve) => {
        signal?.addEventListener(
          "abort",
          () => {
            firstAborted = true
            resolve()
          },
          { once: true },
        )
      })
      return false
    },
  )
  await Promise.resolve()

  const latest = writer.write(true, async (available) => available)
  const [firstResult, latestResult] = await Promise.all([first, latest])

  assert.equal(firstAborted, true)
  assert.equal(firstResult.input, true)
  assert.equal(firstResult.output, true)
  assert.deepEqual(latestResult, firstResult)
})

test("a stalled write is bounded when no newer intent arrives", async () => {
  const writer = new LatestWrite<boolean, boolean>(10)
  const write = writer.write(
    true,
    async (_available, signal?: AbortSignal) => {
      await new Promise<void>((resolve) => {
        signal?.addEventListener("abort", () => resolve(), { once: true })
      })
      throw signal?.reason
    },
  )

  await assert.rejects(write, { name: "TimeoutError" })
  assert.equal(writer.pending, false)
})
