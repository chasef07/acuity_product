import assert from "node:assert/strict"
import test from "node:test"

import { createWorkspaceSync } from "./workspace-sync.ts"

test("startup waits for ready then performs one authoritative reconciliation", async () => {
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  let tokenAcquisitions = 0
  let reconciliations = 0
  const applied: number[] = []
  const states: string[] = []

  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
        { status: 200 },
      ),
    getToken: async () => {
      tokenAcquisitions += 1
      return `token-${tokenAcquisitions}`
    },
    reconcile: async ({ token }) => {
      reconciliations += 1
      assert.equal(token, "token-2")
      return {
        version: 4,
        apply: () => applied.push(4),
      }
    },
    onStateChange: (state) => states.push(state),
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.ok(stream))
  assert.equal(reconciliations, 0)

  stream!.enqueue(
    new TextEncoder().encode(
      'event: ready\ndata: {"practiceId":"practice-1","version":4}\n\n',
    ),
  )
  await eventually(() => assert.deepEqual(applied, [4]))

  assert.equal(reconciliations, 1)
  assert.equal(tokenAcquisitions, 2)
  assert.equal(states.at(-1), "connected")
  sync.stop()
})

test("stream parsing accepts CRLF delimiters split across chunks", async () => {
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  let reconciliations = 0
  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async () => {
      reconciliations += 1
      return { version: 6, apply: () => {} }
    },
    onStateChange: () => {},
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.ok(stream))
  for (const chunk of [
    "event: ready\r",
    '\ndata: {"practiceId":"practice-1","version":6}\r',
    "\n\r",
    "\n",
  ]) {
    stream!.enqueue(new TextEncoder().encode(chunk))
  }
  await eventually(() => assert.equal(reconciliations, 1))
  sync.stop()
})

test("planned stream rotation reconnects quietly and reconciles once", async () => {
  const streams: ReadableStreamDefaultController<Uint8Array>[] = []
  const applied: number[] = []
  const states: string[] = []

  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            streams.push(controller)
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async () => {
      const version = applied.length + 4
      return { version, apply: () => applied.push(version) }
    },
    onStateChange: (state) => states.push(state),
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.equal(streams.length, 1))
  streams[0]!.enqueue(readyEvent(4))
  await eventually(() => assert.deepEqual(applied, [4]))
  streams[0]!.close()
  await eventually(() => assert.equal(streams.length, 2))
  streams[1]!.enqueue(readyEvent(5))
  await eventually(() => assert.deepEqual(applied, [4, 5]))

  assert.deepEqual(states, ["connecting", "connected", "connected"])
  sync.stop()
})

test("a failed reconciliation cancels the superseded stream before reconnecting", async () => {
  const streams: ReadableStreamDefaultController<Uint8Array>[] = []
  let firstStreamCanceled = false
  let reconciliations = 0
  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () => {
      const index = streams.length
      return new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            streams.push(controller)
          },
          cancel() {
            if (index === 0) firstStreamCanceled = true
          },
        }),
      )
    },
    getToken: async () => "token",
    reconcile: async () => {
      reconciliations += 1
      if (reconciliations === 1) throw new Error("portal unavailable")
      return { version: 2, apply: () => {} }
    },
    onStateChange: () => {},
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.equal(streams.length, 1))
  streams[0]!.enqueue(readyEvent(1))
  await eventually(() => assert.equal(streams.length, 2))

  assert.equal(firstStreamCanceled, true)
  sync.stop()
})

test("hint bursts coalesce to the newest version without regression", async () => {
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  const releaseVersionTwo = deferred<void>()
  const requested: number[] = []
  const applied: number[] = []

  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async ({ minimumVersion }) => {
      requested.push(minimumVersion)
      if (minimumVersion === 2) await releaseVersionTwo.promise
      return {
        version: minimumVersion,
        apply: () => applied.push(minimumVersion),
      }
    },
    onStateChange: () => {},
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.ok(stream))
  stream!.enqueue(readyEvent(1))
  await eventually(() => assert.deepEqual(applied, [1]))
  stream!.enqueue(hintEvent(2))
  await eventually(() => assert.deepEqual(requested, [1, 2]))
  stream!.enqueue(hintEvent(3))
  stream!.enqueue(hintEvent(9))
  stream!.enqueue(hintEvent(5))
  releaseVersionTwo.resolve()
  await eventually(() => assert.deepEqual(applied, [1, 2, 9]))

  stream!.enqueue(hintEvent(4))
  await new Promise((resolve) => setTimeout(resolve, 10))
  assert.deepEqual(requested, [1, 2, 9])
  sync.stop()
})

test("a validated hint refreshes the authoritative Call before workspace reconciliation finishes", async () => {
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  const releaseReconciliation = deferred<void>()
  const applied: number[] = []
  let authoritativeCallRefreshes = 0

  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async ({ minimumVersion }) => {
      if (minimumVersion === 2) await releaseReconciliation.promise
      return {
        version: minimumVersion,
        apply: () => applied.push(minimumVersion),
      }
    },
    onValidatedHint: () => {
      authoritativeCallRefreshes += 1
    },
    onStateChange: () => {},
  })

  try {
    sync.setScope({ practiceID: "practice-1", locationID: "location-1" })
    await eventually(() => assert.ok(stream))
    stream!.enqueue(readyEvent(1))
    await eventually(() => assert.deepEqual(applied, [1]))

    stream!.enqueue(hintEvent(2))
    await eventually(() => assert.equal(authoritativeCallRefreshes, 1))
    assert.deepEqual(applied, [1])

    releaseReconciliation.resolve()
    await eventually(() => assert.deepEqual(applied, [1, 2]))
  } finally {
    sync.stop()
  }
})

test("failed hint reconciliation retries once immediately then backs off", async () => {
  const clock = new ManualClock()
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  let hintAttempts = 0
  const requested: number[] = []
  const applied: number[] = []
  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async ({ minimumVersion }) => {
      requested.push(minimumVersion)
      if (minimumVersion === 2) {
        hintAttempts += 1
        if (hintAttempts < 3) throw new Error("portal unavailable")
      }
      return {
        version: minimumVersion,
        apply: () => applied.push(minimumVersion),
      }
    },
    onStateChange: () => {},
    random: () => 0.5,
    sleep: clock.sleep,
    timing: {
      retryBaseMilliseconds: 1_000,
      retryCapMilliseconds: 8_000,
      degradedGraceMilliseconds: 5_000,
      pollMinimumMilliseconds: 10_000,
      pollMaximumMilliseconds: 20_000,
    },
  })

  try {
    sync.setScope({ practiceID: "practice-1", locationID: "location-1" })
    await eventually(() => assert.ok(stream))
    stream!.enqueue(readyEvent(1))
    await eventually(() => assert.deepEqual(applied, [1]))

    stream!.enqueue(hintEvent(2))
    await eventually(() => assert.deepEqual(requested, [1, 2]))
    await new Promise((resolve) => setTimeout(resolve, 10))
    assert.deepEqual(requested, [1, 2])

    await clock.advance(0)
    await eventually(() => assert.deepEqual(requested, [1, 2, 2]))
    await clock.advance(499)
    assert.deepEqual(requested, [1, 2, 2])
    await clock.advance(1)
    await eventually(() => assert.deepEqual(applied, [1, 2]))
  } finally {
    sync.stop()
  }
})

test("successful hint retry clears degraded state on the open stream", async () => {
  const clock = new ManualClock()
  const recovery = deferred<void>()
  let stream: ReadableStreamDefaultController<Uint8Array> | undefined
  let hintAttempts = 0
  const applied: number[] = []
  const states: string[] = []
  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            stream = controller
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async ({ minimumVersion }) => {
      if (minimumVersion === 2) {
        hintAttempts += 1
        if (hintAttempts === 1) throw new Error("portal unavailable")
        await recovery.promise
      }
      return {
        version: minimumVersion,
        apply: () => applied.push(minimumVersion),
      }
    },
    onStateChange: (state) => states.push(state),
    random: () => 0.5,
    sleep: clock.sleep,
    timing: {
      retryBaseMilliseconds: 1_000,
      retryCapMilliseconds: 8_000,
      degradedGraceMilliseconds: 100,
      pollMinimumMilliseconds: 10_000,
      pollMaximumMilliseconds: 20_000,
    },
  })

  try {
    sync.setScope({ practiceID: "practice-1", locationID: "location-1" })
    await eventually(() => assert.ok(stream))
    stream!.enqueue(readyEvent(1))
    await eventually(() => assert.deepEqual(applied, [1]))

    stream!.enqueue(hintEvent(2))
    await eventually(() => assert.equal(hintAttempts, 1))
    await clock.advance(0)
    await eventually(() => assert.equal(hintAttempts, 2))
    await clock.advance(100)
    assert.equal(states.at(-1), "degraded")

    recovery.resolve()
    await eventually(() => assert.deepEqual(applied, [1, 2]))
    assert.equal(states.at(-1), "connected")
  } finally {
    sync.stop()
  }
})

test("reconnect cancels stale hint backoff from the previous stream", async () => {
  const clock = new ManualClock()
  const streams: ReadableStreamDefaultController<Uint8Array>[] = []
  const requested: number[] = []
  const applied: number[] = []
  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () =>
      new Response(
        new ReadableStream<Uint8Array>({
          start(controller) {
            streams.push(controller)
          },
        }),
      ),
    getToken: async () => "token",
    reconcile: async ({ minimumVersion }) => {
      requested.push(minimumVersion)
      if (requested.length === 2 || requested.length === 3) {
        throw new Error("portal unavailable")
      }
      return {
        version: minimumVersion,
        apply: () => applied.push(minimumVersion),
      }
    },
    onStateChange: () => {},
    random: () => 0.5,
    sleep: clock.sleep,
    timing: {
      retryBaseMilliseconds: 1_000,
      retryCapMilliseconds: 8_000,
      degradedGraceMilliseconds: 5_000,
      pollMinimumMilliseconds: 10_000,
      pollMaximumMilliseconds: 20_000,
    },
  })

  try {
    sync.setScope({ practiceID: "practice-1", locationID: "location-1" })
    await eventually(() => assert.equal(streams.length, 1))
    streams[0]!.enqueue(readyEvent(1))
    await eventually(() => assert.deepEqual(applied, [1]))
    streams[0]!.enqueue(hintEvent(2))
    await eventually(() => assert.deepEqual(requested, [1, 2]))
    await clock.advance(0)
    await eventually(() => assert.deepEqual(requested, [1, 2, 2]))

    streams[0]!.close()
    await eventually(() => assert.equal(streams.length, 2))
    streams[1]!.enqueue(readyEvent(2))
    await eventually(() => assert.deepEqual(applied, [1, 2]))
    streams[1]!.enqueue(hintEvent(3))
    await eventually(() => assert.deepEqual(requested, [1, 2, 2, 2, 3]))
  } finally {
    sync.stop()
  }
})

test("sustained outage backs off, degrades after grace, and polls without a storm", async () => {
  const clock = new ManualClock()
  const states: string[] = []
  let streamAttempts = 0
  let tokenAcquisitions = 0
  let fallbackReconciliations = 0

  const sync = createWorkspaceSync({
    realtimeURL: "https://realtime.example",
    fetch: async () => {
      streamAttempts += 1
      throw new Error("realtime unavailable")
    },
    getToken: async () => {
      tokenAcquisitions += 1
      return "token"
    },
    reconcile: async () => {
      fallbackReconciliations += 1
      return { version: 1, apply: () => {} }
    },
    onStateChange: (state) => states.push(state),
    random: () => 0.5,
    sleep: clock.sleep,
    timing: {
      retryBaseMilliseconds: 1_000,
      retryCapMilliseconds: 8_000,
      degradedGraceMilliseconds: 2_000,
      pollMinimumMilliseconds: 4_000,
      pollMaximumMilliseconds: 8_000,
    },
  })
  sync.setScope({ practiceID: "practice-1", locationID: "location-1" })

  await eventually(() => assert.equal(streamAttempts, 2))
  assert.deepEqual(states, ["connecting"])
  await clock.advance(500)
  assert.equal(streamAttempts, 3)
  await clock.advance(1_000)
  assert.equal(streamAttempts, 4)
  await clock.advance(500)
  assert.equal(states.at(-1), "degraded")
  assert.equal(fallbackReconciliations, 0)
  await clock.advance(1_500)
  assert.equal(streamAttempts, 5)
  await clock.advance(4_000)
  assert.equal(streamAttempts, 6)
  await clock.advance(500)

  assert.equal(fallbackReconciliations, 1)
  assert.equal(tokenAcquisitions, 7)
  assert.deepEqual(clock.requested.slice(0, 5), [2_000, 500, 1_000, 2_000, 6_000])
  sync.stop()
})

function readyEvent(version: number) {
  return new TextEncoder().encode(
    `event: ready\ndata: {"practiceId":"practice-1","version":${version}}\n\n`,
  )
}

function hintEvent(version: number) {
  return new TextEncoder().encode(
    `event: hint\ndata: {"practiceId":"practice-1","version":${version}}\n\n`,
  )
}

function deferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

class ManualClock {
  now = 0
  requested: number[] = []
  private sleepers: Array<{
    deadline: number
    resolve: () => void
    signal: AbortSignal
  }> = []

  sleep = (milliseconds: number, signal: AbortSignal) => {
    this.requested.push(milliseconds)
    return new Promise<void>((resolve) => {
      this.sleepers.push({
        deadline: this.now + milliseconds,
        resolve,
        signal,
      })
    })
  }

  async advance(milliseconds: number) {
    this.now += milliseconds
    const ready = this.sleepers.filter(
      (sleeper) => !sleeper.signal.aborted && sleeper.deadline <= this.now,
    )
    this.sleepers = this.sleepers.filter(
      (sleeper) => sleeper.signal.aborted || sleeper.deadline > this.now,
    )
    for (const sleeper of ready) sleeper.resolve()
    await new Promise((resolve) => setTimeout(resolve, 0))
  }
}

async function eventually(assertion: () => void) {
  const deadline = Date.now() + 500
  let lastError: unknown
  while (Date.now() < deadline) {
    try {
      assertion()
      return
    } catch (error) {
      lastError = error
      await new Promise((resolve) => setTimeout(resolve, 0))
    }
  }
  throw lastError
}
