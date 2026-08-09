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
  const commit = async (available: boolean) => {
    inFlight += 1
    maxInFlight = Math.max(maxInFlight, inFlight)
    if (!available) await releaseFirst.promise
    committed = available
    inFlight -= 1
    return committed
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
