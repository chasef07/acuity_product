import assert from "node:assert/strict"
import test from "node:test"

import type {
  CallingCall,
  CallingDispositionResult,
  CallingReadinessRequest,
  CallingState,
  SoftphoneState,
  StartOutboundCallRequest,
} from "../api/generated/types.gen.ts"
import type {
  CallingMediaAdapter,
  IncomingMediaLeg,
  MediaState,
} from "./media-adapter.ts"
import {
  createSoftphoneRuntime,
  SoftphoneAdapterError,
  type SoftphoneBackend,
  type SoftphoneClock,
} from "./softphone-runtime.ts"

test("start restores the owned lease and never regresses a Call version", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-1" })
  backend.calls.set("call-1", call({ id: "call-1", version: 4 }))
  backend.state = callingState({
    softphone: backend.lease,
    bridged: {
      callId: "call-1",
      callLegId: "leg-1",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      state: "BRIDGED",
      version: 4,
    },
  })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    clock,
    visibility: visible(),
    microphone: readyMicrophone(),
  })
  const versions: number[] = []
  runtime.subscribe(() => {
    const version = runtime.getSnapshot().activeCall?.version
    if (version !== undefined) versions.push(version)
  })

  await runtime.start()
  backend.calls.set("call-1", call({ id: "call-1", version: 3 }))
  await runtime.signalRefresh()

  const snapshot = runtime.getSnapshot()
  assert.equal(snapshot.phase, "running")
  assert.equal(snapshot.lease?.owner, true)
  assert.equal(snapshot.activeCall?.version, 4)
  assert.equal(snapshot.expectedCallID, "call-1")
  assert.ok(versions.length > 0)
  assert.ok(versions.every((version) => version === 4))
  assert.equal(media.connects, 1)
})

test("start restores a pre-answer outbound Ending Call", async () => {
  const backend = new DeterministicBackend()
  const ending = call({
    id: "call-ending-before-answer",
    direction: "OUTBOUND",
    state: "RINGING",
    endRequested: true,
    version: 2,
  })
  backend.lease = lease({ owner: true, activeCallId: ending.id })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [
      offer({
        callId: ending.id,
        callLegId: "leg-ending-before-answer",
        mediaToken: "media-ending-before-answer",
      }),
    ],
  })
  backend.calls.set(ending.id, ending)
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })

  await runtime.start()

  assert.equal(runtime.getSnapshot().activeCall?.id, ending.id)
  assert.equal(runtime.getSnapshot().activeCall?.endRequested, true)
  assert.equal(runtime.getSnapshot().controls.canEnd, false)
  assert.equal(runtime.getSnapshot().offers.length, 0)
})

test("an unchanged Calling snapshot still refreshes the known active Call", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-1" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("call-1", "leg-1", 1),
  })
  backend.calls.set(
    "call-1",
    call({ id: "call-1", state: "CONNECTING", version: 1 }),
  )
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().activeCall?.state, "CONNECTING")
  assert.equal(runtime.getSnapshot().expectedCallID, "call-1")

  backend.calls.set(
    "call-1",
    call({
      id: "call-1",
      state: "CONNECTED",
      connectedAt: "2026-08-27T00:00:00Z",
      version: 2,
    }),
  )
  backend.readStateHandler = async () => ({
    status: "not-modified" as const,
    etag: backend.etag,
  })

  await runtime.signalRefresh()

  assert.equal(runtime.getSnapshot().activeCall?.state, "CONNECTED")
  assert.equal(runtime.getSnapshot().activeCall?.version, 2)
})

test("a terminal observation outranks a delayed nonterminal command response", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-terminal" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("call-terminal", "leg-terminal", 1),
  })
  backend.calls.set(
    "call-terminal",
    call({ id: "call-terminal", state: "CONNECTED", version: 1 }),
  )
  const delayedHangup = deferred<CallingCall>()
  backend.hangupHandler = async () => delayedHangup.promise
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const ending = runtime.hangup()
  await drainMicrotasks()
  const terminal = call({
    id: "call-terminal",
    state: "NEEDS_DISPOSITION",
    version: 3,
  })
  backend.lease = lease({
    owner: true,
    pendingOutcomeCallId: terminal.id,
  })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: {
      ...stateCall(terminal.id, "leg-terminal", terminal.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  backend.calls.set(terminal.id, terminal)
  await runtime.signalRefresh()
  delayedHangup.resolve(
    call({ id: terminal.id, state: "CONNECTED", version: 2 }),
  )
  await ending

  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().pendingDisposition?.version, 3)
  assert.equal(runtime.getSnapshot().pendingDisposition?.state, "NEEDS_DISPOSITION")
})

test("authoritative terminal state clears attached media without an SDK ended event", async () => {
  const fixture = await attachedOutboundMediaFixture(
    "provider-terminal-without-sdk-event",
  )
  const terminal = setAttachedOutboundTerminal(fixture)

  await fixture.runtime.signalRefresh()

  assert.equal(fixture.runtime.getSnapshot().pendingDisposition?.id, terminal.id)
  assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, false)
  assert.equal(fixture.media.disconnects, 0)
  await eventually(() => assert.equal(fixture.exact.rejections, 1))
})

test("terminal media purge failure or timeout fails calling closed", async (context) => {
  await context.test("failure", async () => {
    const fixture = await attachedOutboundMediaFixture("provider-terminal-failure")
    fixture.exact.failRejects = 1
    setAttachedOutboundTerminal(fixture)

    await fixture.runtime.signalRefresh()

    await eventually(() => assert.equal(fixture.media.disconnects, 1))
    assert.equal(fixture.runtime.getSnapshot().failure?.kind, "media")
    assert.equal(fixture.runtime.getSnapshot().readiness.mediaState, "unavailable")
    assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
    assert.equal(fixture.backend.readinessWrites.at(-1)?.available, false)
  })

  await context.test("timeout", async () => {
    const fixture = await attachedOutboundMediaFixture("provider-terminal-timeout")
    fixture.exact.rejectDeferred = deferred<void>()
    setAttachedOutboundTerminal(fixture)

    await fixture.runtime.signalRefresh()
    assert.notEqual(fixture.runtime.getSnapshot().mediaAttachment, undefined)
    await fixture.clock.advance(10_000)

    await eventually(() => assert.equal(fixture.media.disconnects, 1))
    assert.equal(fixture.runtime.getSnapshot().failure?.kind, "media")
    assert.equal(fixture.runtime.getSnapshot().readiness.mediaState, "unavailable")
    assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
    assert.equal(fixture.backend.readinessWrites.at(-1)?.available, false)
  })

  await context.test("disposition while purge is pending", async () => {
    const fixture = await attachedOutboundMediaFixture(
      "provider-terminal-disposition-race",
    )
    const purge = deferred<void>()
    fixture.exact.rejectDeferred = purge
    const terminal = setAttachedOutboundTerminal(fixture)
    const resolved = call({
      id: terminal.id,
      direction: "OUTBOUND",
      state: "RESOLVED",
      version: terminal.version + 1,
    })
    fixture.backend.disposeHandler = async () => {
      fixture.backend.lease = lease({ owner: true, available: false })
      fixture.backend.state = callingState({ softphone: fixture.backend.lease })
      fixture.backend.calls.set(resolved.id, resolved)
      return { call: resolved }
    }

    await fixture.runtime.signalRefresh()
    await fixture.runtime.dispose("RESOLVED")

    assert.equal(fixture.exact.rejections, 1)
    assert.equal(fixture.media.disconnects, 0)
    assert.equal(fixture.runtime.getSnapshot().failure, undefined)
    assert.notEqual(fixture.runtime.getSnapshot().mediaAttachment, undefined)

    purge.resolve()
    await eventually(() =>
      assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined),
    )
    assert.equal(fixture.exact.rejections, 1)
    assert.equal(fixture.media.disconnects, 0)
    assert.equal(fixture.runtime.getSnapshot().failure, undefined)
  })
})

test("Close cannot bypass a pending terminal media purge", async () => {
  const fixture = await attachedOutboundMediaFixture(
    "provider-terminal-close-race",
  )
  const purge = deferred<void>()
  fixture.exact.rejectDeferred = purge
  const terminal = call({
    id: fixture.connected.id,
    direction: "OUTBOUND",
    state: "UNANSWERED",
    version: fixture.connected.version + 1,
  })
  fixture.backend.lease = lease({ owner: true, available: false })
  fixture.backend.state = callingState({ softphone: fixture.backend.lease })
  fixture.backend.calls.set(terminal.id, terminal)

  await fixture.runtime.signalRefresh()
  fixture.runtime.dismissOutcome()

  assert.equal(fixture.runtime.getSnapshot().activeCall?.id, terminal.id)
  assert.notEqual(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.backend.readinessWrites.at(-1)?.available, false)

  purge.resolve()
  await eventually(() =>
    assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined),
  )
  fixture.runtime.dismissOutcome()
  assert.equal(fixture.runtime.getSnapshot().activeCall, undefined)
})

test("failed terminal media confirmation purge fails calling closed", async () => {
  const fixture = await outboundMediaFixture()
  const terminal = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "NEEDS_DISPOSITION",
    version: 2,
  })
  fixture.backend.confirmMediaHandler = async () => {
    fixture.backend.lease = lease({
      owner: true,
      available: false,
      pendingOutcomeCallId: terminal.id,
    })
    fixture.backend.state = callingState({
      softphone: fixture.backend.lease,
      disposition: {
        ...stateCall(terminal.id, fixture.expected.callLegId, terminal.version),
        state: "NEEDS_DISPOSITION",
      },
    })
    fixture.backend.calls.set(terminal.id, terminal)
    return terminal
  }
  const exact = mediaLeg({
    providerLegID: "provider-terminal-confirmation",
    mediaToken: fixture.expected.mediaToken,
    failRejects: 1,
  })

  fixture.media.emitIncoming(exact)

  await eventually(() => assert.ok(exact.rejections >= 1))
  await eventually(() => assert.equal(fixture.media.disconnects, 1))
  assert.equal(fixture.runtime.getSnapshot().pendingDisposition?.id, terminal.id)
  assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, false)
  assert.equal(fixture.runtime.getSnapshot().failure?.kind, "media")
  assert.equal(fixture.runtime.getSnapshot().readiness.mediaState, "unavailable")
})

test("terminal media confirmation stays occupied until exact purge completes", async () => {
  const fixture = await outboundMediaFixture()
  const terminal = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "UNANSWERED",
    version: 2,
  })
  fixture.backend.confirmMediaHandler = async () => {
    fixture.backend.lease = lease({ owner: true, available: false })
    fixture.backend.state = callingState({ softphone: fixture.backend.lease })
    fixture.backend.calls.set(terminal.id, terminal)
    return terminal
  }
  const purge = deferred<void>()
  const exact = mediaLeg({
    providerLegID: "provider-terminal-confirmation-pending",
    mediaToken: fixture.expected.mediaToken,
    rejectDeferred: purge,
  })

  fixture.media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(fixture.runtime.getSnapshot().activeCall?.state, "UNANSWERED"),
  )

  assert.notEqual(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.runtime.getSnapshot().occupied, true)
  fixture.runtime.dismissOutcome()
  assert.equal(fixture.runtime.getSnapshot().activeCall?.id, terminal.id)
  assert.equal(fixture.backend.readinessWrites.at(-1)?.available, false)

  purge.resolve()
  await eventually(() =>
    assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined),
  )
  fixture.runtime.dismissOutcome()
  assert.equal(fixture.runtime.getSnapshot().activeCall, undefined)
  assert.equal(fixture.media.disconnects, 0)
  assert.equal(fixture.runtime.getSnapshot().failure, undefined)
})

test("a terminal watermark survives disposition before a delayed response arrives", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-watermark" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("call-watermark", "leg-watermark", 1),
  })
  backend.calls.set(
    "call-watermark",
    call({ id: "call-watermark", state: "CONNECTED", version: 1 }),
  )
  const delayedHangup = deferred<CallingCall>()
  backend.hangupHandler = async () => delayedHangup.promise
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const ending = runtime.hangup()
  await drainMicrotasks()
  const terminal = call({
    id: "call-watermark",
    state: "NEEDS_DISPOSITION",
    version: 3,
  })
  backend.lease = lease({ owner: true, pendingOutcomeCallId: terminal.id })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: {
      ...stateCall(terminal.id, "leg-watermark", terminal.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  backend.calls.set(terminal.id, terminal)
  await runtime.signalRefresh()

  backend.disposeHandler = async () => {
    const resolved = call({ id: terminal.id, state: "RESOLVED", version: 4 })
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    backend.calls.set(terminal.id, resolved)
    return { call: resolved }
  }
  await runtime.dispose("RESOLVED")
  delayedHangup.resolve(
    call({ id: terminal.id, state: "CONNECTED", version: 2 }),
  )
  await ending

  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().pendingDisposition, undefined)
  assert.equal(runtime.getSnapshot().terminalVersions[terminal.id], 4)
})

test("a delayed disposition read cannot restore Outcome after its commit", async () => {
  const backend = new DeterministicBackend()
  const pending = call({
    id: "call-delayed-disposition",
    state: "NEEDS_DISPOSITION",
    version: 3,
  })
  backend.lease = lease({ owner: true, pendingOutcomeCallId: pending.id })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: {
      ...stateCall(pending.id, "delayed-disposition-leg", pending.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  backend.calls.set(pending.id, pending)
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().pendingDisposition?.version, 3)
  let dispositionCommitted = false
  let outcomeResurrected = false
  runtime.subscribe(() => {
    if (dispositionCommitted && runtime.getSnapshot().pendingDisposition) {
      outcomeResurrected = true
    }
  })

  const delayedRead = deferred<CallingCall>()
  backend.readCallHandler = async (callID) =>
    callID === pending.id ? delayedRead.promise : backend.calls.get(callID)!
  const staleRefresh = runtime.signalRefresh()
  await drainMicrotasks()
  backend.disposeHandler = async () => {
    const resolved = call({ id: pending.id, state: "RESOLVED", version: 4 })
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    backend.calls.set(pending.id, resolved)
    dispositionCommitted = true
    return { call: resolved }
  }
  const disposing = runtime.dispose("RESOLVED")
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().pendingDisposition, undefined)
  assert.equal(runtime.getSnapshot().terminalVersions[pending.id], 4)

  delayedRead.resolve(pending)
  await Promise.all([staleRefresh, disposing])

  assert.equal(runtime.getSnapshot().pendingDisposition, undefined)
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(outcomeResurrected, false)
})

test("outbound intent cannot replace an Outcome before disposition commits", async () => {
  const backend = new DeterministicBackend()
  const pending = call({
    id: "call-awaiting-outcome",
    state: "NEEDS_DISPOSITION",
    version: 3,
  })
  backend.lease = lease({ owner: true, pendingOutcomeCallId: pending.id })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: {
      ...stateCall(pending.id, "outcome-leg", pending.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  backend.calls.set(pending.id, pending)
  let outboundStarts = 0
  backend.startOutboundHandler = async () => {
    outboundStarts += 1
    return call({ id: "forbidden-outbound", state: "PREPARING" })
  }
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  await runtime.startOutbound({
    idempotencyKey: "before-disposition",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })

  assert.equal(outboundStarts, 0)
  assert.equal(runtime.getSnapshot().pendingDisposition?.id, pending.id)
  assert.equal(runtime.getSnapshot().pendingCall, undefined)
  assert.equal(runtime.getSnapshot().failure?.kind, "conflict")
})

test("availability becomes true only after every technical readiness fact is current", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, available: false })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const microphone = controllableMicrophone()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone,
    availabilityIntent: true,
    clock: new ManualClock(),
    visibility: visible(),
  })

  await runtime.start()

  assert.ok(backend.readinessWrites.length > 0)
  assert.deepEqual(backend.readinessWrites.at(-1), {
    sessionId: "session-1",
    registered: true,
    microphoneReady: true,
    audioReady: true,
    sessionHealthy: true,
    available: true,
  })
  await eventually(() =>
    assert.equal(runtime.getSnapshot().lease?.available, true),
  )
  media.emitState("unavailable")
  await drainMicrotasks()
  assert.equal(backend.readinessWrites.at(-1)?.available, false)
  assert.equal(runtime.getSnapshot().lease?.available, false)
  assert.equal(microphone.starts, 1)
})

test("media connection is single-flight while microphone permission is pending", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const microphoneReady = deferred<{ stop(): void }>()
  let microphoneStarts = 0
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: {
      start: async () => {
        microphoneStarts += 1
        return microphoneReady.promise
      },
    },
    clock: new ManualClock(),
    visibility: visible(),
  })

  const starting = runtime.start()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().readiness.mediaState, "registering"),
  )
  const firstIntent = runtime.setAvailability(true)
  const secondIntent = runtime.setAvailability(true)
  await drainMicrotasks()
  assert.equal(microphoneStarts, 1)

  microphoneReady.resolve({ stop() {} })
  await Promise.all([starting, firstIntent, secondIntent])
  assert.equal(microphoneStarts, 1)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
})

test("availability Off wins when an older On intent is waiting for media", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  media.connectError = new Error("initial media connection failed")
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")

  media.connectError = undefined
  media.connectDeferred = deferred<void>()
  const turningOn = runtime.setAvailability(true)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().readiness.mediaState, "registering"),
  )
  const turningOff = runtime.setAvailability(false)
  await drainMicrotasks()
  media.connectDeferred.resolve()
  await Promise.all([turningOn, turningOff])

  assert.equal(runtime.getSnapshot().availabilityIntent, false)
  assert.equal(backend.lease.available, false)
  assert.equal(backend.readinessWrites.at(-1)?.available, false)
})

test("stop fences a media credential that resolves after cleanup", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const credential = deferred<string>()
  backend.issueMediaTokenHandler = async () => credential.promise
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })

  const starting = runtime.start()
  await eventually(() => assert.equal(backend.mediaTokenRequests, 1))
  await runtime.stop()
  credential.resolve("late-media-credential")
  await starting

  assert.equal(media.connects, 0)
  assert.equal(runtime.getSnapshot().phase, "stopped")
})

test("lease loss disconnects a media client whose connect resolves late", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  media.connectDeferred = deferred<void>()
  media.connectError = new Error("late socket registration failure")
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })

  const starting = runtime.start()
  await eventually(() => assert.equal(media.connects, 1))
  backend.lease = lease({ sessionId: "other-session", owner: true })
  backend.state = callingState({ softphone: backend.lease })
  await runtime.signalRefresh()
  const mediaTokenRequests = backend.mediaTokenRequests
  assert.equal(await media.refreshCredential(), undefined)
  assert.equal(backend.mediaTokenRequests, mediaTokenRequests)
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
  media.connectDeferred.resolve()
  await starting

  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.ok(media.disconnects >= 1)
})

test("a partial media connect failure disconnects the abandoned client", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  media.connectError = new Error("socket registration failed")
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })

  await runtime.start()

  assert.equal(media.connects, 1)
  assert.equal(media.disconnects, 1)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.equal(runtime.getSnapshot().failure?.kind, "technical-readiness")

  media.emitState("ready")
  const stale = mediaLeg({
    providerLegID: "abandoned-provider-leg",
    mediaToken: "abandoned-media-token",
  })
  media.emitIncoming(stale)
  await eventually(() => assert.equal(stale.rejections, 1))
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
})

test("socket reconnection is visible and recoverable until media is ready", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  media.emitState("reconnecting")
  assert.equal(runtime.getSnapshot().readiness.mediaState, "reconnecting")
  assert.equal(runtime.getSnapshot().failure?.kind, "media")
  assert.equal(runtime.getSnapshot().failure?.recoverable, true)

  media.emitState("ready")
  assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("failed explicit recovery closes backend availability before media teardown", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    availabilityIntent: true,
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(backend.lease.available, true)

  media.emitAudioIssue()
  assert.equal(runtime.getSnapshot().failure?.kind, "media")
  const writesBeforeRecovery = backend.readinessWrites.length
  media.connectError = new Error("socket registration failed")
  await runtime.recover()

  assert.equal(
    backend.readinessWrites[writesBeforeRecovery]?.available,
    false,
  )
  assert.equal(backend.lease.available, false)
  assert.equal(backend.readinessWrites.at(-1)?.available, false)
  assert.equal(media.disconnects, 2)
  assert.equal(media.connects, 2)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.equal(runtime.getSnapshot().failure?.kind, "technical-readiness")
})

test("explicit recovery preserves media when backend availability cannot close", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    availabilityIntent: true,
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(backend.lease.available, true)
  const disconnectsBeforeRecovery = media.disconnects

  media.emitAudioIssue()
  backend.writeReadinessHandler = async (input) => {
    if (!input.available) {
      throw new SoftphoneAdapterError(
        "temporary-request",
        "Readiness could not be closed.",
        true,
      )
    }
    return lease({ owner: true, available: input.available })
  }
  await runtime.recover()

  assert.equal(media.disconnects, disconnectsBeforeRecovery)
  assert.equal(media.connects, 1)
  assert.equal(backend.lease.available, true)
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")
})

test("a temporarily unavailable media credential retries without losing availability intent", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  let attempts = 0
  backend.issueMediaTokenHandler = async () => {
    attempts += 1
    if (attempts === 1) {
      throw new SoftphoneAdapterError(
        "temporary-request",
        "Calling credentials are still being prepared. Trying again shortly.",
        true,
      )
    }
    return "media-credential"
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    availabilityIntent: true,
    clock,
    visibility: visible(),
  })

  await runtime.start()

  assert.equal(attempts, 1)
  assert.equal(media.connects, 0)
  assert.equal(runtime.getSnapshot().availabilityIntent, true)
  assert.equal(runtime.getSnapshot().failure?.recoverable, true)
  assert.equal(backend.readinessWrites.length, 0)

  await clock.advance(4_000)

  assert.equal(attempts, 2)
  assert.equal(media.connects, 1)
  assert.equal(runtime.getSnapshot().lease?.available, true)
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("a delayed readiness response cannot repaint a newer availability intent", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const persisted: boolean[] = []
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    persistAvailabilityIntent: (available) => persisted.push(available),
  })
  await runtime.start()
  const first = deferred<SoftphoneState>()
  backend.writeReadinessHandler = (input) =>
    input.available ? lease({ owner: true, available: true }) : first.promise

  const pause = runtime.setAvailability(false)
  await drainMicrotasks()
  const resume = runtime.setAvailability(true)
  await drainMicrotasks()
  assert.equal(backend.readinessWrites.at(-1)?.available, false)
  first.resolve(lease({ owner: true, available: false }))
  await Promise.all([pause, resume])

  assert.equal(runtime.getSnapshot().availabilityIntent, true)
  assert.equal(runtime.getSnapshot().lease?.available, true)
  assert.deepEqual(
    backend.readinessWrites.slice(-2).map((write) => write.available),
    [false, true],
  )
  assert.deepEqual(persisted, [false, true])
})

test("stop closes readiness after an older write before a restarted runtime becomes available", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    availabilityIntent: true,
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  await drainMicrotasks()

  const olderWrite = deferred<SoftphoneState>()
  let delayNextAvailableWrite = true
  backend.writeReadinessHandler = (input) => {
    if (input.available && delayNextAvailableWrite) {
      delayNextAvailableWrite = false
      return olderWrite.promise
    }
    return lease({ owner: true, available: input.available })
  }
  const refreshingAvailability = runtime.setAvailability(true)
  await eventually(() =>
    assert.equal(backend.readinessWrites.at(-1)?.available, true),
  )
  const requestsBeforeRestart = backend.leaseRequests.length

  const stopping = runtime.stop()
  const restarting = runtime.start()
  await drainMicrotasks()
  assert.equal(backend.leaseRequests.length, requestsBeforeRestart)

  olderWrite.resolve(lease({ owner: true, available: true }))
  await Promise.all([refreshingAvailability, stopping, restarting])

  const writes = backend.readinessWrites.slice(-3)
  assert.deepEqual(
    writes.map((write) => write.available),
    [true, false, true],
  )
  assert.deepEqual(writes[1], {
    sessionId: "session-1",
    registered: false,
    microphoneReady: false,
    audioReady: false,
    sessionHealthy: false,
    available: false,
  })
  await eventually(() => assert.equal(backend.lease.available, true))
  assert.equal(runtime.getSnapshot().phase, "running")
  await eventually(() =>
    assert.equal(runtime.getSnapshot().lease?.available, true),
  )
})

test("a failed final readiness write does not undo local stop or reject cleanup", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  backend.writeReadinessHandler = async (input) => {
    if (!input.registered) {
      throw new SoftphoneAdapterError(
        "temporary-request",
        "readiness endpoint is unavailable",
        true,
      )
    }
    return lease({ owner: true, available: input.available })
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  await assert.doesNotReject(runtime.stop())

  assert.equal(runtime.getSnapshot().phase, "stopped")
  assert.equal(runtime.getSnapshot().lease, undefined)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.equal(media.disconnects, 1)
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")
  assert.match(
    runtime.getSnapshot().failure?.message ?? "",
    /stopped locally.*readiness could not be cleared/i,
  )

  backend.writeReadinessHandler = undefined
  await runtime.start()
  assert.equal(runtime.getSnapshot().phase, "running")
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("stop aborts a hung refresh so restart owns a fresh request", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  let stallNextRead = true
  let stalled = false
  let aborted = false
  backend.readStateHandler = async (_input, signal) => {
    if (!stallNextRead) {
      return { status: "modified", state: backend.state, etag: backend.etag }
    }
    stallNextRead = false
    stalled = true
    return new Promise((_resolve, reject) => {
      signal?.addEventListener(
        "abort",
        () => {
          aborted = true
          reject(new DOMException("aborted", "AbortError"))
        },
        { once: true },
      )
    })
  }
  const refreshing = runtime.signalRefresh()
  await eventually(() => assert.equal(stalled, true))
  const leaseRequestsBeforeRestart = backend.leaseRequests.length

  const stopping = runtime.stop()
  const restarting = runtime.start()
  await Promise.all([refreshing, stopping, restarting])

  assert.equal(aborted, true)
  assert.equal(backend.leaseRequests.length, leaseRequestsBeforeRestart + 1)
  assert.equal(runtime.getSnapshot().phase, "running")
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("stop aborts a hung readiness write before final close and restart", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  let hangNextAvailableWrite = true
  let aborted = false
  backend.writeReadinessHandler = (input, signal) => {
    if (input.available && hangNextAvailableWrite) {
      hangNextAvailableWrite = false
      return new Promise((_resolve, reject) => {
        signal?.addEventListener(
          "abort",
          () => {
            aborted = true
            reject(new DOMException("aborted", "AbortError"))
          },
          { once: true },
        )
      })
    }
    return lease({ owner: true, available: input.available })
  }
  const becomingAvailable = runtime.setAvailability(true)
  await eventually(() =>
    assert.equal(backend.readinessWrites.at(-1)?.available, true),
  )

  const stopping = runtime.stop()
  const restarting = runtime.start()
  await Promise.all([becomingAvailable, stopping, restarting])

  assert.equal(aborted, true)
  assert.deepEqual(
    backend.readinessWrites.slice(-3).map((write) => write.available),
    [true, false, true],
  )
  assert.equal(runtime.getSnapshot().phase, "running")
  assert.equal(runtime.getSnapshot().lease?.available, true)
})

test("polling after a lost lease response restores media and heartbeat", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  const restored = call({ id: "call-lost-acquire", state: "CONNECTED" })
  backend.calls.set(restored.id, restored)
  backend.acquireLeaseHandler = async () => {
    backend.lease = lease({ owner: true, activeCallId: restored.id })
    backend.state = callingState({
      softphone: backend.lease,
      bridged: stateCall(restored.id, "leg-lost-acquire", restored.version),
    })
    throw new SoftphoneAdapterError(
      "temporary-request",
      "The lease response was lost.",
      true,
    )
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })

  await runtime.start()
  assert.equal(media.connects, 0)
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")

  backend.acquireLeaseHandler = undefined
  await runtime.signalRefresh()
  await eventually(() => assert.equal(media.connects, 1))
  await eventually(() =>
    assert.equal(runtime.getSnapshot().activeCall?.id, restored.id),
  )
  assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
  assert.equal(runtime.getSnapshot().failure, undefined)
  await drainMicrotasks()

  assert.ok(clock.pendingTimers >= 2)
})

test("routine heartbeats use a stable per-session stagger without surfacing availability pending", async () => {
  async function observeHeartbeat(sessionID: string) {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    backend.lease = lease({ sessionId: sessionID, owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const runtime = createSoftphoneRuntime({
      sessionID,
      backend,
      media: new DeterministicMedia(),
      microphone: readyMicrophone(),
      availabilityIntent: true,
      clock,
      visibility: visible(),
    })
    await runtime.start()
    const writesBeforeHeartbeat = backend.readinessWrites.length
    const surfacedPendingAt: number[] = []
    let availabilityPending = runtime.getSnapshot().pending.availability
    const unsubscribe = runtime.subscribe(() => {
      const nextPending = runtime.getSnapshot().pending.availability
      if (!availabilityPending && nextPending) surfacedPendingAt.push(clock.now)
      availabilityPending = nextPending
    })

    await clock.advance(3_499)
    assert.equal(backend.readinessWrites.length, writesBeforeHeartbeat)
    while (
      clock.now < 4_000 &&
      backend.readinessWrites.length === writesBeforeHeartbeat
    ) {
      await clock.advance(1)
    }

    assert.equal(backend.readinessWrites.length, writesBeforeHeartbeat + 1)
    assert.equal(runtime.getSnapshot().lease?.available, true)
    assert.deepEqual(surfacedPendingAt, [])
    const heartbeatAt = clock.now
    unsubscribe()
    await runtime.stop()
    return heartbeatAt
  }

  const firstHeartbeatAt = await observeHeartbeat("session-1")
  const secondHeartbeatAt = await observeHeartbeat("session-2")
  assert.ok(firstHeartbeatAt >= 3_500 && firstHeartbeatAt <= 4_000)
  assert.ok(secondHeartbeatAt >= 3_500 && secondHeartbeatAt <= 4_000)
  assert.notEqual(firstHeartbeatAt, secondHeartbeatAt)
})

test("stop discards local Call projections and abandoned command state", async () => {
  const backend = new DeterministicBackend()
  const stale = call({ id: "call-ended-while-stopped", state: "CONNECTED" })
  backend.lease = lease({ owner: true, activeCallId: stale.id })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(stale.id, "leg-ended-while-stopped", stale.version),
  })
  backend.calls.set(stale.id, stale)
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().activeCall?.id, stale.id)

  await runtime.stop()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  await runtime.start()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().expectedCallID, "")

  backend.startOutboundHandler = async () => new Promise<CallingCall>(() => {})
  const starting = runtime.startOutbound({
    idempotencyKey: "abandoned-outbound",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  await eventually(() => assert.notEqual(runtime.getSnapshot().pendingCall, undefined))
  await runtime.stop()
  await starting
  await runtime.start()

  assert.equal(runtime.getSnapshot().pendingCall, undefined)
  assert.deepEqual(runtime.getSnapshot().pending, {
    availability: false,
    retry: false,
    disposition: false,
  })
  assert.equal(runtime.getSnapshot().occupied, false)
})

test("bounded media lifecycle cannot poison stop or restart", async (context) => {
  await context.test("microphone acquisition", async () => {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const firstMicrophone = deferred<{ stop(): void }>()
    let starts = 0
    let lateStops = 0
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media: new DeterministicMedia(),
      microphone: {
        async start() {
          starts += 1
          if (starts === 1) return firstMicrophone.promise
          return { stop() {} }
        },
      },
      clock,
      visibility: visible(),
    })

    const starting = runtime.start()
    await eventually(() =>
      assert.equal(runtime.getSnapshot().readiness.mediaState, "registering"),
    )
    await clock.advance(10_000)
    await starting
    assert.equal(runtime.getSnapshot().failure?.kind, "technical-readiness")
    await runtime.stop()
    await runtime.start()
    assert.equal(starts, 2)
    assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")

    firstMicrophone.resolve({ stop: () => { lateStops += 1 } })
    await drainMicrotasks()
    assert.equal(lateStops, 1)
    assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
  })

  await context.test("media connect", async () => {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const media = new DeterministicMedia()
    media.connectDeferred = deferred<void>()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock,
      visibility: visible(),
    })

    const starting = runtime.start()
    await eventually(() => assert.equal(media.connects, 1))
    await clock.advance(10_000)
    await starting
    assert.equal(runtime.getSnapshot().failure?.kind, "technical-readiness")
    await runtime.stop()
    media.connectDeferred = undefined
    await runtime.start()
    assert.equal(media.connects, 2)
    assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
  })

  await context.test("media disconnect", async () => {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock,
      visibility: visible(),
    })
    await runtime.start()
    media.disconnectDeferred = deferred<void>()

    const stopping = runtime.stop()
    await clock.advance(10_000)
    await stopping
    assert.equal(runtime.getSnapshot().phase, "stopped")
    media.disconnectDeferred = undefined
    await runtime.start()
    assert.equal(runtime.getSnapshot().phase, "running")
  })
})

test("media failure stays visible through readiness outage and serializes recovery", async (context) => {
  await context.test("readiness outage", async () => {
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    backend.writeReadinessHandler = async (input) => {
      if (!input.registered) {
        throw new SoftphoneAdapterError(
          "temporary-request",
          "Readiness is offline with media.",
          true,
        )
      }
      return lease({ owner: true, available: input.available })
    }
    backend.readStateHandler = async () => ({
      status: "not-modified" as const,
      etag: backend.etag,
    })
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      availabilityIntent: true,
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()
    const readinessWrites = backend.readinessWrites.length

    media.emitFailure("network")
    await eventually(() =>
      assert.ok(backend.readinessWrites.length > readinessWrites),
    )
    await eventually(() =>
      assert.equal(runtime.getSnapshot().failure?.kind, "media"),
    )
    await runtime.signalRefresh()
    assert.equal(runtime.getSnapshot().failure?.kind, "media")

    backend.writeReadinessHandler = undefined
    await runtime.recover()
    assert.equal(media.connects, 2)
    assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
    assert.equal(runtime.getSnapshot().failure, undefined)
  })

  await context.test("concurrent recovery", async () => {
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const media = new DeterministicMedia()
    const firstDisconnect = deferred<void>()
    const microphone = controllableMicrophone()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone,
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()
    media.disconnectDeferred = firstDisconnect

    media.emitFailure("provider")
    await eventually(() => assert.equal(media.disconnects, 1))
    const recovering = runtime.recover()
    await drainMicrotasks()
    assert.equal(media.connects, 1)

    firstDisconnect.resolve()
    await recovering
    assert.equal(media.connects, 2)
    assert.equal(runtime.getSnapshot().readiness.mediaState, "ready")
    assert.equal(runtime.getSnapshot().failure, undefined)
    assert.equal(microphone.starts, 2)
    assert.equal(microphone.stops, 1)
  })
})

test("an incoming Call attaches by media token then requires the exact durable bridged winner", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  const incoming = offer({
    callId: "call-inbound",
    callLegId: "durable-leg-17",
    mediaToken: "media-token-17",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease, ringing: [incoming] })
  backend.calls.set(
    "call-inbound",
    call({
      id: "call-inbound",
      direction: "INBOUND",
      state: "CONNECTING",
      version: 2,
    }),
  )
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()

  const unrelated = mediaLeg({
    providerLegID: "provider-leg-unrelated",
    mediaToken: "wrong-token",
  })
  media.emitIncoming(unrelated)
  await clock.advance(5_000)
  await runtime.signalRefresh()
  await eventually(() => assert.equal(unrelated.rejections, 1))

  const exact = mediaLeg({
    providerLegID: "provider-leg-9000",
    mediaToken: incoming.mediaToken,
    failAnswers: 1,
  })
  media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )
  const duplicate = mediaLeg({
    providerLegID: "provider-leg-duplicate",
    mediaToken: incoming.mediaToken,
  })
  media.emitIncoming(duplicate)
  await eventually(() => assert.equal(duplicate.rejections, 1))

  media.emitEnded(exact)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, false),
  )
  media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )

  await runtime.answer(incoming.callLegId)
  assert.equal(exact.answers, 1)
  assert.equal(runtime.getSnapshot().failure?.kind, "media")
  assert.match(runtime.getSnapshot().failure?.message ?? "", /try Answer again/)
  assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true)
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)

  await runtime.answer(incoming.callLegId)
  assert.equal(exact.answers, 2)
  assert.equal(runtime.getSnapshot().failure, undefined)
  assert.deepEqual(runtime.getSnapshot().mediaAttachment, {
    callID: "call-inbound",
    callLegID: "durable-leg-17",
    providerLegID: "provider-leg-9000",
    mediaToken: "media-token-17",
  })
  assert.equal(runtime.getSnapshot().controls.canMute, false)
  assert.equal(runtime.getSnapshot().controls.canEnd, false)

  backend.lease = lease({ owner: true, activeCallId: "call-inbound" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: {
      callId: "call-inbound",
      callLegId: "durable-leg-17",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      state: "BRIDGED",
      version: 2,
    },
  })
  backend.calls.set(
    "call-inbound",
    call({
      id: "call-inbound",
      direction: "INBOUND",
      state: "CONNECTED",
      version: 3,
    }),
  )
  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().activeCall?.id, "call-inbound")
  assert.equal(runtime.getSnapshot().controls.canMute, true)
  assert.equal(runtime.getSnapshot().controls.canEnd, true)

  const losingPurge = deferred<void>()
  exact.rejectDeferred = losingPurge
  backend.state = callingState({
    softphone: lease({ owner: true }),
    bridged: {
      callId: "call-inbound",
      callLegId: "different-durable-winner",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      state: "BRIDGED",
      version: 3,
    },
  })
  await runtime.signalRefresh()

  assert.equal(exact.rejections, 1)
  assert.notEqual(runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().expectedCallID, "")
  assert.equal(runtime.getSnapshot().occupied, true)
  assert.equal(backend.readinessWrites.at(-1)?.available, false)

  losingPurge.resolve()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().mediaAttachment, undefined),
  )

  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().expectedCallID, "")
})

test("inbound disposition stays occupied until exact media purge completes", async () => {
  const backend = new DeterministicBackend()
  const incoming = offer({
    callId: "call-inbound-disposition-purge",
    callLegId: "leg-inbound-disposition-purge",
    mediaToken: "media-inbound-disposition-purge",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease, ringing: [incoming] })
  backend.calls.set(
    incoming.callId,
    call({
      id: incoming.callId,
      direction: "INBOUND",
      state: "CONNECTING",
      version: 1,
    }),
  )
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  const exact = mediaLeg({
    providerLegID: "provider-inbound-disposition-purge",
    mediaToken: incoming.mediaToken,
  })
  media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )
  await runtime.answer(incoming.callLegId)
  assert.notEqual(runtime.getSnapshot().mediaAttachment, undefined)

  const purge = deferred<void>()
  exact.rejectDeferred = purge
  const terminal = call({
    id: incoming.callId,
    direction: "INBOUND",
    state: "NEEDS_DISPOSITION",
    version: 2,
  })
  backend.lease = lease({
    owner: true,
    available: false,
    pendingOutcomeCallId: terminal.id,
  })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: {
      ...stateCall(terminal.id, incoming.callLegId, terminal.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  backend.calls.set(terminal.id, terminal)

  await runtime.signalRefresh()

  assert.equal(runtime.getSnapshot().pendingDisposition?.id, terminal.id)
  assert.notEqual(runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(runtime.getSnapshot().occupied, true)
  assert.equal(backend.readinessWrites.at(-1)?.available, false)

  purge.resolve()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().mediaAttachment, undefined),
  )
  assert.equal(exact.rejections, 1)
  assert.equal(media.disconnects, 0)
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("ended answered media keeps its durable leg correlation until a different winner resolves", async () => {
  const backend = new DeterministicBackend()
  const incoming = offer({
    callId: "call-inbound",
    callLegId: "durable-leg-loser",
    mediaToken: "media-token-loser",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease, ringing: [incoming] })
  backend.calls.set(
    incoming.callId,
    call({ id: incoming.callId, direction: "INBOUND", state: "CONNECTED", version: 3 }),
  )
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  const losingMedia = mediaLeg({
    providerLegID: "provider-leg-loser",
    mediaToken: incoming.mediaToken,
    answerDeferred: deferred<"attached" | "ended">(),
  })
  media.emitIncoming(losingMedia)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )
  const answer = runtime.answer(incoming.callLegId)
  await drainMicrotasks()

  backend.state = callingState({
    softphone: lease({ owner: true, activeCallId: incoming.callId }),
    bridged: {
      callId: incoming.callId,
      callLegId: "durable-leg-winner",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      state: "BRIDGED",
      version: 3,
    },
  })
  await runtime.signalRefresh()
  losingMedia.answerDeferred!.resolve("ended")
  await answer
  await runtime.signalRefresh()

  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().expectedCallID, "")
})

test("refresh is single-flight, keeps ETag, adapts cadence, and renders a committed transient Call within one second", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  await drainMicrotasks()
  const initialReads = backend.reads.length
  assert.equal(initialReads, 2)
  assert.equal(backend.reads[0]?.etag, undefined)

  await clock.advance(3_999)
  assert.equal(backend.reads.length, initialReads)
  await clock.advance(1)
  assert.equal(backend.reads.length, initialReads + 1)
  assert.equal(backend.reads.at(-1)?.etag, '"state-1"')

  const blocked = deferred<void>()
  let overlappingReads = 0
  backend.readStateHandler = async (input) => {
    backend.reads.push({ etag: input.etag, at: clock.now })
    overlappingReads += 1
    if (overlappingReads === 1) await blocked.promise
    return { status: "not-modified", etag: backend.etag }
  }
  const first = runtime.signalRefresh()
  const second = runtime.signalRefresh()
  const third = runtime.signalRefresh()
  await drainMicrotasks()
  assert.equal(overlappingReads, 1)
  blocked.resolve()
  await Promise.all([first, second, third])
  assert.equal(overlappingReads, 2)

  backend.readStateHandler = undefined
  backend.lease = lease({ owner: true, activeCallId: "call-fast" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: {
      callId: "call-fast",
      callLegId: "durable-outbound",
      practiceId: "practice-1",
      locationId: "location-1",
      locationName: "Main",
      state: "BRIDGE_PENDING",
      version: 5,
    },
  })
  backend.calls.set(
    "call-fast",
    call({ id: "call-fast", state: "CONNECTING", version: 5 }),
  )
  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().activeCall?.version, 5)

  const committedAt = clock.now
  let renderedAt = -1
  const unsubscribe = runtime.subscribe(() => {
    if (runtime.getSnapshot().activeCall?.version === 6) renderedAt = clock.now
  })
  backend.calls.set(
    "call-fast",
    call({ id: "call-fast", state: "CONNECTED", version: 6 }),
  )
  await clock.advance(249)
  assert.equal(runtime.getSnapshot().activeCall?.version, 5)
  await clock.advance(1)
  assert.equal(runtime.getSnapshot().activeCall?.version, 6)
  assert.ok(renderedAt - committedAt < 1_000)
  unsubscribe()

  let failNextRead = true
  backend.readStateHandler = async (input) => {
    backend.reads.push({ etag: input.etag, at: clock.now })
    if (failNextRead) {
      failNextRead = false
      throw new Error("portal temporarily unavailable")
    }
    return { status: "not-modified", etag: backend.etag }
  }
  const beforeFailure = backend.reads.length
  await clock.advance(1_000)
  assert.equal(backend.reads.length, beforeFailure + 1)
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")
  await clock.advance(499)
  assert.equal(backend.reads.length, beforeFailure + 1)
  await clock.advance(1)
  assert.equal(backend.reads.length, beforeFailure + 2)
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("Hangup preserves Ending until committed disposition and disposition clears only after commit", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-end" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("call-end", "durable-outbound", 1),
  })
  backend.calls.set(
    "call-end",
    call({ id: "call-end", state: "CONNECTED", version: 1 }),
  )
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  const hangupCommit = deferred<CallingCall>()
  backend.hangupHandler = () => hangupCommit.promise

  const ending = runtime.hangup()
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().endingCallID, "call-end")
  assert.equal(runtime.getSnapshot().controls.canEnd, false)
  hangupCommit.resolve(
    call({
      id: "call-end",
      state: "CONNECTED",
      endRequested: true,
      version: 2,
    }),
  )
  await ending
  assert.equal(runtime.getSnapshot().activeCall?.version, 2)
  assert.equal(runtime.getSnapshot().endingCallID, "call-end")

  backend.lease = lease({ owner: true, pendingOutcomeCallId: "call-end" })
  backend.state = callingState({
    softphone: backend.lease,
    disposition: stateCall("call-end", "durable-outbound", 3),
  })
  backend.calls.set(
    "call-end",
    call({ id: "call-end", state: "NEEDS_DISPOSITION", version: 3 }),
  )
  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().endingCallID, "")
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().pendingDisposition?.id, "call-end")
  assert.equal(runtime.getSnapshot().controls.canDispose, true)

  backend.disposeHandler = async () => {
    const resolved = call({ id: "call-end", state: "RESOLVED", version: 4 })
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    backend.calls.set("call-end", resolved)
    return { call: resolved }
  }
  const result = await runtime.dispose("RESOLVED")
  assert.equal(result?.call.state, "RESOLVED")
  assert.equal(runtime.getSnapshot().pendingDisposition, undefined)
})

test("a provider-first Hangup conflict converges to Outcome without a stale alert", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-provider-first" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("call-provider-first", "provider-first-leg", 1),
  })
  backend.calls.set(
    "call-provider-first",
    call({ id: "call-provider-first", state: "CONNECTED", version: 1 }),
  )
  backend.hangupHandler = async () => {
    backend.lease = lease({
      owner: true,
      pendingOutcomeCallId: "call-provider-first",
    })
    backend.state = callingState({
      softphone: backend.lease,
      disposition: stateCall("call-provider-first", "provider-first-leg", 2),
    })
    backend.calls.set(
      "call-provider-first",
      call({
        id: "call-provider-first",
        state: "NEEDS_DISPOSITION",
        version: 2,
      }),
    )
    throw new SoftphoneAdapterError(
      "conflict",
      "Call state changed before End.",
    )
  }
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  await runtime.hangup()

  assert.equal(runtime.getSnapshot().endingCallID, "")
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(
    runtime.getSnapshot().pendingDisposition?.id,
    "call-provider-first",
  )
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("retry keeps the retryable Outcome visible and cannot create two active Calls", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true, activeCallId: "call-failed" })
  backend.state = callingState({ softphone: backend.lease })
  backend.calls.set(
    "call-failed",
    call({
      id: "call-failed",
      state: "UNANSWERED",
      retryAllowed: true,
      version: 3,
    }),
  )
  const retryCommit = deferred<CallingCall>()
  let retries = 0
  backend.retryHandler = async () => {
    retries += 1
    return retryCommit.promise
  }
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().activeCall?.state, "UNANSWERED")
  assert.equal(runtime.getSnapshot().expectedCallID, "")
  assert.equal(runtime.getSnapshot().controls.canRetry, true)

  const first = runtime.retry("retry-1")
  const duplicate = runtime.retry("retry-2")
  await drainMicrotasks()
  assert.equal(retries, 1)
  assert.equal(runtime.getSnapshot().pending.retry, true)
  runtime.dismissOutcome()
  assert.equal(runtime.getSnapshot().activeCall?.id, "call-failed")
  const retried = call({
    id: "call-retried",
    state: "PREPARING",
    retryOfCallId: "call-failed",
    retryAllowed: false,
    version: 1,
  })
  backend.lease = lease({ owner: true, activeCallId: retried.id })
  backend.state = callingState({ softphone: backend.lease })
  backend.calls.set(retried.id, retried)
  retryCommit.resolve(retried)
  await Promise.all([first, duplicate])

  assert.equal(runtime.getSnapshot().activeCall?.id, "call-retried")
  assert.equal(runtime.getSnapshot().pending.retry, false)
  assert.equal(retries, 1)

  backend.lease = lease({ owner: true, activeCallId: retried.id })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(retried.id, "retried-leg", 2),
  })
  backend.calls.set(
    retried.id,
    call({
      id: retried.id,
      state: "UNANSWERED",
      retryAllowed: false,
      version: 2,
    }),
  )
  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().activeCall?.state, "UNANSWERED")
  assert.equal(runtime.getSnapshot().controls.canRetry, false)
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  runtime.dismissOutcome()
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(backend.readinessWrites.at(-1)?.available, false)
})

test("lost retry and disposition responses reconcile committed state", async (t) => {
  await t.test("retry", async () => {
    const backend = new DeterministicBackend()
    const failed = call({
      id: "call-retry-response-lost",
      state: "UNANSWERED",
      retryAllowed: true,
      version: 2,
    })
    const retried = call({
      id: "call-retry-response-committed",
      state: "PREPARING",
      retryAllowed: false,
      version: 1,
    })
    backend.lease = lease({ owner: true, activeCallId: failed.id })
    backend.state = callingState({ softphone: backend.lease })
    backend.calls.set(failed.id, failed)
    backend.retryHandler = async () => {
      backend.lease = lease({ owner: true, activeCallId: retried.id })
      backend.state = callingState({ softphone: backend.lease })
      backend.calls.set(retried.id, retried)
      throw new SoftphoneAdapterError(
        "temporary-request",
        "The committed retry response was lost.",
        true,
      )
    }
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media: new DeterministicMedia(),
      microphone: readyMicrophone(),
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()

    await runtime.retry("retry-response-lost")


    assert.equal(runtime.getSnapshot().activeCall?.id, retried.id)
    assert.equal(runtime.getSnapshot().pending.retry, false)
    assert.equal(runtime.getSnapshot().failure, undefined)
  })

  await t.test("disposition", async () => {
    const backend = new DeterministicBackend()
    const pending = call({
      id: "call-disposition-response-lost",
      state: "NEEDS_DISPOSITION",
      version: 3,
    })
    const resolved = call({
      id: pending.id,
      state: "RESOLVED",
      version: 4,
    })
    backend.lease = lease({
      owner: true,
      pendingOutcomeCallId: pending.id,
    })
    backend.state = callingState({
      softphone: backend.lease,
      disposition: stateCall(pending.id, "disposition-leg", pending.version),
    })
    backend.calls.set(pending.id, pending)
    backend.disposeHandler = async () => {
      backend.lease = lease({ owner: true })
      backend.state = callingState({ softphone: backend.lease })
      backend.calls.set(resolved.id, resolved)
      throw new SoftphoneAdapterError(
        "temporary-request",
        "The committed disposition response was lost.",
        true,
      )
    }
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media: new DeterministicMedia(),
      microphone: readyMicrophone(),
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()

    const result = await runtime.dispose("RESOLVED")

    assert.equal(result, undefined)
    assert.equal(runtime.getSnapshot().pendingDisposition, undefined)
    assert.equal(runtime.getSnapshot().pending.disposition, false)
    assert.equal(runtime.getSnapshot().failure, undefined)
  })
})

test("offers use transient cadence and stop cleans timers, visibility, media, and microphone without severing subscribers", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [offer({ callLegId: "offer-cadence" })],
  })
  const media = new DeterministicMedia()
  const microphone = controllableMicrophone()
  const visibility = new DeterministicVisibility()
  const attention = new DeterministicAttention()
  const phases: string[] = []
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone,
    clock,
    visibility,
    attention,
  })
  runtime.subscribe(() => phases.push(runtime.getSnapshot().phase))
  await runtime.start()
  await drainMicrotasks()
  assert.equal(visibility.listenerCount, 1)
  const initialReads = backend.reads.length
  assert.equal(initialReads, 2)
  await clock.advance(249)
  assert.equal(backend.reads.length, initialReads)
  await clock.advance(1)
  assert.equal(backend.reads.length, initialReads + 1)

  const visibilityRead = deferred<void>()
  let visibilityReads = 0
  backend.readStateHandler = async (input) => {
    backend.reads.push({ etag: input.etag, at: clock.now })
    visibilityReads += 1
    if (visibilityReads === 1) await visibilityRead.promise
    return { status: "modified", state: backend.state, etag: backend.etag }
  }
  visibility.emit()
  visibility.emit()
  visibility.emit()
  await drainMicrotasks()
  assert.equal(visibilityReads, 1)
  visibilityRead.resolve()
  await eventually(() => assert.equal(visibilityReads, 2))

  backend.readStateHandler = undefined
  visibility.hidden = true
  visibility.emit()
  await eventually(() => assert.equal(backend.reads.length, initialReads + 4))
  await drainMicrotasks()
  const hiddenReads = backend.reads.length
  await clock.advance(7_999)
  assert.equal(backend.reads.length, hiddenReads)
  await clock.advance(1)
  assert.equal(backend.reads.length, hiddenReads + 1)
  visibility.hidden = false
  visibility.emit()
  await eventually(() => assert.equal(backend.reads.length, hiddenReads + 2))
  assert.ok(attention.ringtoneStarts > 0)
  assert.ok(attention.notificationStarts > 0)

  microphone.lose()
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().readiness.microphoneReady, false)
  assert.equal(runtime.getSnapshot().failure?.kind, "technical-readiness")
  assert.equal(backend.readinessWrites.at(-1)?.available, false)

  const readsBeforeStop = backend.reads.length
  await runtime.stop()
  assert.equal(runtime.getSnapshot().phase, "stopped")
  assert.equal(visibility.listenerCount, 0)
  assert.equal(clock.pendingTimers, 0)
  assert.equal(media.disconnects, 2)
  assert.equal(microphone.stops, 1)
  assert.equal(attention.ringtoneStops, attention.ringtoneStarts)
  assert.equal(attention.notificationStops, attention.notificationStarts)
  assert.equal(phases.at(-1), "stopped")
  visibility.emit()
  await drainMicrotasks()
  assert.equal(backend.reads.length, readsBeforeStop)

  await runtime.start()
  assert.equal(runtime.getSnapshot().phase, "running")
  assert.equal(visibility.listenerCount, 1)
  assert.equal(media.connects, 2)
  assert.equal(phases.at(-1), "running")
})

test("expired offers yield to a visible refresh failure", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [
      offer({
        callLegId: "offer-expiring",
        deadline: new Date(250).toISOString(),
      }),
    ],
  })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().offers.length, 1)
  backend.readStateHandler = async () => {
    throw new SoftphoneAdapterError(
      "temporary-request",
      "Calling refresh is unavailable.",
      true,
    )
  }

  await clock.advance(250)

  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")
})

test("unmatched media survives failed sync and the bounded correlation window before rejection", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  backend.readStateHandler = async () => {
    throw new SoftphoneAdapterError(
      "temporary-request",
      "Calling refresh is unavailable.",
      true,
    )
  }
  const unmatched = mediaLeg({
    providerLegID: "provider-unmatched",
    mediaToken: "media-unmatched",
  })

  media.emitIncoming(unmatched)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request"),
  )
  assert.equal(unmatched.rejections, 0)

  backend.readStateHandler = undefined
  await runtime.signalRefresh()
  assert.equal(unmatched.rejections, 0)
  await clock.advance(4_999)
  assert.equal(unmatched.rejections, 0)
  await clock.advance(1)
  await runtime.signalRefresh()
  assert.equal(unmatched.rejections, 1)
})

test("304 refreshes still expire and reject unmatched provider media", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  backend.readStateHandler = async () => ({
    status: "not-modified" as const,
    etag: backend.etag,
  })
  const unmatched = mediaLeg({
    providerLegID: "provider-unmatched-304",
    mediaToken: "media-unmatched-304",
  })

  media.emitIncoming(unmatched)
  await drainMicrotasks()
  assert.equal(unmatched.rejections, 0)
  await clock.advance(5_000)
  await runtime.signalRefresh()

  assert.equal(unmatched.rejections, 1)
})

test("bounded provider leg effects cannot wedge Answer or refresh", async (context) => {
  await context.test("Answer", async () => {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    const incoming = offer({
      callId: "call-hung-answer",
      callLegId: "leg-hung-answer",
      mediaToken: "media-hung-answer",
    })
    backend.lease = lease({ owner: true })
    backend.state = callingState({
      softphone: backend.lease,
      ringing: [incoming],
    })
    backend.calls.set(
      incoming.callId,
      call({ id: incoming.callId, direction: "INBOUND", state: "CONNECTING" }),
    )
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock,
      visibility: visible(),
    })
    await runtime.start()
    const answerDeferred = deferred<"attached" | "ended">()
    const firstReject = deferred<void>()
    const leg = mediaLeg({
      providerLegID: "provider-hung-answer",
      mediaToken: incoming.mediaToken,
      answerDeferred,
      rejectDeferred: firstReject,
    })
    media.emitIncoming(leg)
    await eventually(() =>
      assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
    )

    const answering = runtime.answer(incoming.callLegId)
    await eventually(() => assert.equal(runtime.getSnapshot().pendingCall?.id, incoming.callId))
    await clock.advance(10_000)
    await answering

    assert.equal(runtime.getSnapshot().pendingCall, undefined)
    assert.equal(runtime.getSnapshot().failure?.kind, "media")
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, false)
    assert.equal(backend.readinessWrites.at(-1)?.available, false)
    await eventually(() => assert.equal(leg.rejections, 1))
    await clock.advance(10_000)
    leg.rejectDeferred = undefined
    answerDeferred.resolve("attached")
    await eventually(() => assert.equal(leg.rejections, 2))
    firstReject.resolve()
  })

  await context.test("reject", async () => {
    const clock = new ManualClock()
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock,
      visibility: visible(),
    })
    await runtime.start()
    const rejectDeferred = deferred<void>()
    const leg = mediaLeg({
      providerLegID: "provider-hung-reject",
      mediaToken: "media-hung-reject",
      rejectDeferred,
    })
    media.emitIncoming(leg)
    await drainMicrotasks()
    await clock.advance(5_000)
    await runtime.signalRefresh()

    assert.equal(leg.rejections, 1)
    await assert.doesNotReject(runtime.signalRefresh())
    rejectDeferred.resolve()
  })
})

test("provider media waits for durable correlation in inbound and outbound command gaps", async (context) => {
  await context.test("inbound", async () => {
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()
    const incoming = offer({
      callId: "call-provider-gap-inbound",
      callLegId: "leg-provider-gap-inbound",
      mediaToken: "media-provider-gap-inbound",
    })
    const leg = mediaLeg({
      providerLegID: "provider-gap-inbound",
      mediaToken: incoming.mediaToken,
    })

    media.emitIncoming(leg)
    await drainMicrotasks()
    assert.equal(leg.rejections, 0)
    assert.equal(runtime.getSnapshot().offers.length, 0)

    backend.state = callingState({
      softphone: backend.lease,
      ringing: [incoming],
    })
    await runtime.signalRefresh()
    assert.equal(leg.rejections, 0)
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true)
  })

  await context.test("outbound", async () => {
    const backend = new DeterministicBackend()
    backend.lease = lease({ owner: true })
    backend.state = callingState({ softphone: backend.lease })
    const outbound = call({
      id: "call-provider-gap-outbound",
      state: "PREPARING",
    })
    backend.startOutboundHandler = async () => {
      backend.lease = lease({ owner: true, activeCallId: outbound.id })
      backend.state = callingState({ softphone: backend.lease })
      backend.calls.set(outbound.id, outbound)
      return outbound
    }
    const media = new DeterministicMedia()
    const runtime = createSoftphoneRuntime({
      sessionID: "session-1",
      backend,
      media,
      microphone: readyMicrophone(),
      clock: new ManualClock(),
      visibility: visible(),
    })
    await runtime.start()
    await runtime.startOutbound({
      idempotencyKey: "provider-gap-outbound",
      practiceId: "practice-1",
      locationId: "location-1",
      destination: "+15551234567",
    })
    const expected = offer({
      callId: outbound.id,
      callLegId: "leg-provider-gap-outbound",
      mediaToken: "media-provider-gap-outbound",
    })
    const leg = mediaLeg({
      providerLegID: "provider-gap-outbound",
      mediaToken: expected.mediaToken,
    })
    backend.confirmMediaHandler = async () =>
      call({ id: outbound.id, state: "CONNECTED", version: 2 })

    media.emitIncoming(leg)
    await drainMicrotasks()
    assert.equal(leg.answers, 0)
    assert.equal(leg.rejections, 0)

    backend.state = callingState({
      softphone: backend.lease,
      ringing: [expected],
    })
    await runtime.signalRefresh()
    await eventually(() => assert.equal(leg.answers, 1))
    assert.equal(runtime.getSnapshot().mediaAttachment?.mediaToken, leg.mediaToken)
  })
})

test("a repeated Telnyx update cannot purge the attached Call", async () => {
  const fixture = await outboundMediaFixture()
  const connected = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "CONNECTED",
    version: 2,
  })
  fixture.backend.confirmMediaHandler = async () => {
    fixture.backend.lease = lease({
      owner: true,
      available: false,
      activeCallId: connected.id,
    })
    fixture.backend.state = callingState({
      softphone: fixture.backend.lease,
      bridged: stateCall(connected.id, fixture.expected.callLegId, connected.version),
    })
    fixture.backend.calls.set(connected.id, connected)
    return connected
  }

  const attachment = deferred<"attached" | "ended">()
  let sharedCallPurges = 0
  const purgeSharedCall = async () => {
    sharedCallPurges += 1
  }
  const ringingUpdate = mediaLeg({
    providerLegID: "provider-repeated-update",
    mediaToken: fixture.expected.mediaToken,
    answerDeferred: attachment,
  })
  ringingUpdate.reject = purgeSharedCall
  fixture.media.emitIncoming(ringingUpdate)
  await eventually(() => assert.equal(ringingUpdate.answers, 1))

  const activeUpdate = mediaLeg({
    providerLegID: ringingUpdate.providerLegID,
    mediaToken: ringingUpdate.mediaToken,
    recovery: true,
  })
  activeUpdate.reject = purgeSharedCall
  fixture.media.emitIncoming(activeUpdate)

  attachment.resolve("attached")
  await eventually(() =>
    assert.equal(
      fixture.runtime.getSnapshot().mediaAttachment?.mediaToken,
      ringingUpdate.mediaToken,
    ),
  )
  await drainMicrotasks()
  await fixture.clock.advance(5_000)
  await fixture.runtime.signalRefresh()

  assert.equal(sharedCallPurges, 0)
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, true)
  assert.equal(fixture.runtime.getSnapshot().controls.canKeypad, true)
})

test("a repeated Telnyx update cannot purge an answered inbound Call", async () => {
  const backend = new DeterministicBackend()
  const incoming = offer({
    callId: "call-repeated-inbound",
    callLegId: "leg-repeated-inbound",
    mediaToken: "media-repeated-inbound",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [incoming],
  })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const attachment = deferred<"attached" | "ended">()
  let sharedCallPurges = 0
  const purgeSharedCall = async () => {
    sharedCallPurges += 1
  }
  const ringingUpdate = mediaLeg({
    providerLegID: "provider-repeated-inbound",
    mediaToken: incoming.mediaToken,
    answerDeferred: attachment,
  })
  ringingUpdate.reject = purgeSharedCall
  media.emitIncoming(ringingUpdate)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )

  const answering = runtime.answer(incoming.callLegId)
  await eventually(() => assert.equal(ringingUpdate.answers, 1))
  await runtime.signalRefresh()
  const refreshStarted = deferred<void>()
  const finishRefresh = deferred<void>()
  backend.readStateHandler = async () => {
    refreshStarted.resolve(undefined)
    await finishRefresh.promise
    return {
      status: "modified" as const,
      state: backend.state,
      etag: backend.etag,
    }
  }
  const activeUpdate = mediaLeg({
    providerLegID: ringingUpdate.providerLegID,
    mediaToken: ringingUpdate.mediaToken,
    recovery: true,
  })
  activeUpdate.reject = purgeSharedCall
  media.emitIncoming(activeUpdate)
  await refreshStarted.promise

  const connected = call({
    id: incoming.callId,
    direction: "INBOUND",
    state: "CONNECTED",
    version: 2,
  })
  backend.lease = lease({
    owner: true,
    available: false,
    activeCallId: connected.id,
  })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(connected.id, incoming.callLegId, connected.version),
  })
  backend.calls.set(connected.id, connected)
  attachment.resolve("attached")
  await eventually(() =>
    assert.equal(runtime.getSnapshot().mediaAttachment?.mediaToken, incoming.mediaToken),
  )
  finishRefresh.resolve(undefined)
  await answering

  assert.equal(sharedCallPurges, 0)
  assert.equal(runtime.getSnapshot().controls.canMute, true)
  assert.equal(runtime.getSnapshot().controls.canKeypad, true)
})

test("a refresh completed after stop cannot repaint a restarted runtime", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  await drainMicrotasks()

  const staleRead = deferred<void>()
  let reads = 0
  backend.readStateHandler = async () => {
    reads += 1
    if (reads === 1) {
      await staleRead.promise
      return {
        status: "modified" as const,
        state: callingState({
          softphone: lease({ owner: true, activeCallId: "stale-call" }),
          bridged: stateCall("stale-call", "stale-leg", 9),
        }),
      }
    }
    return { status: "modified" as const, state: backend.state, etag: backend.etag }
  }
  backend.calls.set("stale-call", call({ id: "stale-call", version: 9 }))
  const staleRefresh = runtime.signalRefresh()
  await drainMicrotasks()
  assert.equal(reads, 1)
  await runtime.stop()

  backend.lease = lease({ owner: true, activeCallId: "current-call" })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall("current-call", "current-leg", 1),
  })
  backend.calls.set("current-call", call({ id: "current-call", version: 1 }))
  const restarted = runtime.start()
  staleRead.resolve()
  await Promise.all([staleRefresh, restarted])

  assert.equal(runtime.getSnapshot().activeCall?.id, "current-call")
  assert.equal(runtime.getSnapshot().activeCall?.version, 1)
})

test("a non-owner can take over and a later lease loss fails media and readiness closed", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({
    sessionId: "other-session",
    owner: false,
    available: true,
  })
  backend.state = callingState({ softphone: backend.lease })
  backend.acquireLeaseHandler = async (input) => {
    if (!input.takeover) return backend.lease
    if (backend.lease.sessionId === "other-session") {
      backend.lease = lease({
        sessionId: "other-session-after-failure",
        owner: false,
      })
      throw new SoftphoneAdapterError(
        "temporary-request",
        "Calling could not reach the service.",
        true,
      )
    }
    backend.lease = lease({ owner: true, available: false })
    backend.state = callingState({ softphone: backend.lease })
    return backend.lease
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })

  await runtime.start()
  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().lease?.available, false)
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
  assert.match(runtime.getSnapshot().failure?.message ?? "", /Take over/)
  assert.equal(media.connects, 0)

  await runtime.recover()
  assert.equal(runtime.getSnapshot().failure?.kind, "temporary-request")
  assert.equal(runtime.getSnapshot().lease?.owner, false)

  await runtime.recover()
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().lease?.owner, true)
  assert.equal(runtime.getSnapshot().failure, undefined)
  assert.equal(media.connects, 1)

  backend.writeReadinessHandler = () => {
    backend.lease = lease({
      sessionId: "other-session",
      owner: false,
      available: false,
    })
    backend.state = callingState({ softphone: backend.lease })
    return backend.lease
  }
  await clock.advance(4_000)

  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
  assert.equal(media.disconnects, 1)
})

test("another session's active lease never becomes local ownership on refresh", async () => {
  const backend = new DeterministicBackend()
  const otherLease = lease({
    sessionId: "other-session",
    owner: true,
    available: true,
    activeCallId: "call-active-other-browser",
  })
  backend.lease = otherLease
  backend.state = callingState({
    softphone: otherLease,
    ringing: [offer({ callLegId: "other-session-leg" })],
  })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })

  await runtime.start()
  await runtime.signalRefresh()

  assert.equal(runtime.getSnapshot().lease?.sessionId, "other-session")
  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().lease?.available, false)
  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
  assert.equal(runtime.getSnapshot().failure?.recoverable, false)
  assert.equal(media.connects, 0)
  const leaseRequests = backend.leaseRequests.length
  await runtime.recover()
  assert.equal(backend.leaseRequests.length, leaseRequests)
})

test("active Practice access revocation tears down attached media and local Call controls", async () => {
  const fixture = await outboundMediaFixture()
  const connected = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "CONNECTED",
    version: 2,
  })
  fixture.backend.confirmMediaHandler = async () => {
    fixture.backend.lease = lease({
      owner: true,
      available: false,
      activeCallId: connected.id,
    })
    fixture.backend.state = callingState({
      softphone: fixture.backend.lease,
      bridged: stateCall(connected.id, fixture.expected.callLegId, 2),
    })
    fixture.backend.calls.set(connected.id, connected)
    return connected
  }
  const exact = mediaLeg({
    providerLegID: "provider-revoked-access",
    mediaToken: fixture.expected.mediaToken,
  })
  fixture.media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(
      fixture.runtime.getSnapshot().mediaAttachment?.mediaToken,
      exact.mediaToken,
    ),
  )
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, true)

  fixture.backend.lease = lease({ owner: true })
  fixture.backend.state = callingState({ softphone: fixture.backend.lease })
  fixture.backend.readCallHandler = async (callID) => {
    assert.equal(callID, connected.id)
    throw new SoftphoneAdapterError(
      "access",
      "Access to this Call's Practice was revoked.",
      false,
    )
  }
  fixture.media.disconnectDeferred = deferred<void>()
  const revoked = fixture.runtime.signalRefresh()
  await eventually(() =>
    assert.equal(fixture.runtime.getSnapshot().failure?.kind, "access"),
  )

  assert.equal(fixture.media.disconnects, 1)
  assert.equal(fixture.runtime.getSnapshot().lease?.owner, false)
  assert.equal(fixture.runtime.getSnapshot().activeCall, undefined)
  assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.runtime.getSnapshot().controls.canEnd, false)
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, false)
  assert.equal(fixture.runtime.getSnapshot().failure?.kind, "access")
  fixture.media.disconnectDeferred.resolve()
  await revoked
  assert.equal(fixture.runtime.getSnapshot().lease?.owner, false)
  assert.equal(fixture.backend.readinessWrites.at(-1)?.registered, false)
  assert.equal(fixture.backend.readinessWrites.at(-1)?.available, false)

  const connectsAfterRevocation = fixture.media.connects
  const readinessWritesAfterRevocation = fixture.backend.readinessWrites.length
  await fixture.runtime.signalRefresh()
  assert.equal(fixture.runtime.getSnapshot().lease?.owner, false)
  assert.equal(fixture.runtime.getSnapshot().failure?.kind, "access")
  assert.equal(fixture.media.connects, connectsAfterRevocation)
  assert.equal(
    fixture.backend.readinessWrites.length,
    readinessWritesAfterRevocation,
  )
  const revokedRecovery = mediaLeg({
    providerLegID: exact.providerLegID,
    mediaToken: exact.mediaToken,
    recovery: true,
  })
  fixture.media.emitIncoming(revokedRecovery)
  await eventually(() => assert.equal(revokedRecovery.rejections, 1))
  assert.equal(revokedRecovery.answers, 0)
})

test("access fail-close serializes behind an older availability write", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  const olderAvailable = deferred<SoftphoneState>()
  backend.writeReadinessHandler = async (input) => {
    if (input.available) return olderAvailable.promise
    return lease({ owner: true, available: false })
  }
  const turningOn = runtime.setAvailability(true)
  await eventually(() =>
    assert.equal(backend.readinessWrites.at(-1)?.available, true),
  )
  let denyNextRead = true
  backend.readStateHandler = async () => {
    if (denyNextRead) {
      denyNextRead = false
      throw new SoftphoneAdapterError(
        "access",
        "Calling access was revoked.",
        false,
      )
    }
    return { status: "modified" as const, state: backend.state, etag: backend.etag }
  }
  const revoked = runtime.signalRefresh()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().failure?.kind, "access"),
  )

  olderAvailable.resolve(lease({ owner: true, available: true }))
  await Promise.all([turningOn, revoked])
  await eventually(() =>
    assert.equal(backend.readinessWrites.at(-1)?.available, false),
  )

  assert.equal(backend.lease.available, false)
  assert.equal(runtime.getSnapshot().lease?.available, false)
})

test("a fresh runtime restores and can dismiss its persisted terminal outbound Outcome", async () => {
  const backend = new DeterministicBackend()
  const terminal = call({
    id: "call-reload-unanswered",
    practiceId: "practice-b",
    locationId: "location-b",
    direction: "OUTBOUND",
    state: "UNANSWERED",
    retryAllowed: true,
    version: 4,
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  backend.calls.set(terminal.id, terminal)
  let persistedCallID: string | undefined = terminal.id
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    loadCallRecoveryID: () => persistedCallID,
    persistCallRecoveryID: (callID) => {
      persistedCallID = callID
    },
  })

  await runtime.start()
  assert.equal(runtime.getSnapshot().activeCall?.id, terminal.id)
  assert.equal(runtime.getSnapshot().activeCall?.practiceId, "practice-b")
  assert.equal(runtime.getSnapshot().activeCall?.locationId, "location-b")
  assert.equal(runtime.getSnapshot().controls.canRetry, true)

  runtime.dismissOutcome()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(persistedCallID, undefined)
  await runtime.stop()
  await runtime.start()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
})

test("a denied readiness command fails closed instead of offering ownership recovery", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const otherLease = lease({ sessionId: "other-session", owner: true })
  backend.state = callingState({ softphone: otherLease })
  backend.writeReadinessHandler = async () => {
    throw new SoftphoneAdapterError(
      "access",
      "You do not have calling access for this practice.",
      false,
    )
  }
  await runtime.setAvailability(false)

  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().failure?.kind, "access")
  assert.equal(runtime.getSnapshot().failure?.recoverable, false)
})

test("outbound media becomes active only after exact committed Call confirmation", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const committed = call({
    id: "call-outbound",
    state: "PREPARING",
    version: 1,
  })
  backend.startOutboundHandler = async () => {
    // Transient outbound Calls are not occupied capacity yet, so the Calling
    // snapshot can legitimately retain an empty activeCallId.
    backend.lease = lease({ owner: true, activeCallId: "" })
    backend.state = callingState({
      softphone: backend.lease,
      ringing: [
        offer({
          callId: committed.id,
          callLegId: "outbound-staff-leg",
          mediaToken: "outbound-token-exact",
          state: "RINGING",
        }),
      ],
    })
    backend.calls.set(committed.id, committed)
    return committed
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  await runtime.startOutbound({
    idempotencyKey: "outbound-1",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })

  assert.equal(runtime.getSnapshot().expectedCallID, committed.id)
  assert.equal(runtime.getSnapshot().activeCall?.state, "PREPARING")
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().expectedMedia?.callLegId, "outbound-staff-leg")

  backend.confirmMediaHandler = async () =>
    call({ id: "stale-call", state: "CONNECTED", version: 8 })
  const stale = mediaLeg({
    providerLegID: "provider-outbound-stale",
    mediaToken: "outbound-token-stale",
  })
  media.emitIncoming(stale)
  await eventually(() => assert.equal(stale.rejections, 1))
  assert.equal(runtime.getSnapshot().activeCall?.id, committed.id)
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)

  const connected = call({
    id: committed.id,
    state: "CONNECTED",
    version: 2,
  })
  backend.confirmMediaHandler = async (input) => {
    assert.equal(input.callID, committed.id)
    assert.equal(input.mediaToken, "outbound-token-exact")
    backend.calls.set(committed.id, connected)
    return connected
  }
  const exact = mediaLeg({
    providerLegID: "provider-outbound-exact",
    mediaToken: "outbound-token-exact",
  })
  media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().mediaAttachment?.mediaToken, exact.mediaToken),
  )

  assert.equal(runtime.getSnapshot().activeCall?.id, committed.id)
  assert.equal(runtime.getSnapshot().activeCall?.version, 2)
  assert.equal(exact.answers, 1)
})

test("outbound media attachment failure stays visible and recoverable", async () => {
  const fixture = await outboundMediaFixture()
  fixture.backend.confirmMediaHandler = async () => {
    throw new Error("confirmation must not run after audio attachment fails")
  }
  const broken = mediaLeg({
    providerLegID: "provider-outbound",
    mediaToken: fixture.expected.mediaToken,
    failAnswers: 1,
  })

  fixture.media.emitIncoming(broken)
  await eventually(() =>
    assert.equal(fixture.runtime.getSnapshot().failure?.kind, "media"),
  )

  assert.equal(broken.answers, 1)
  assert.equal(broken.rejections, 1)
  assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(fixture.runtime.getSnapshot().controls.canMute, false)
})

test("outbound media confirmation retries conflict and a lost response", async () => {
  const fixture = await outboundMediaFixture()
  const connected = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "CONNECTED",
    version: 2,
  })
  let attempts = 0
  fixture.backend.confirmMediaHandler = async () => {
    attempts += 1
    if (attempts === 1) {
      throw new SoftphoneAdapterError(
        "conflict",
        "Provider answer evidence is not committed yet.",
      )
    }
    fixture.backend.calls.set(connected.id, connected)
    if (attempts === 2) {
      throw new SoftphoneAdapterError(
        "temporary-request",
        "The committed response was lost.",
        true,
      )
    }
    return connected
  }
  const exact = mediaLeg({
    providerLegID: "provider-outbound",
    mediaToken: fixture.expected.mediaToken,
  })

  fixture.media.emitIncoming(exact)
  await eventually(() => assert.equal(attempts, 1))
  await drainMicrotasks()
  await fixture.clock.advance(250)
  await eventually(() => assert.equal(attempts, 2))
  await drainMicrotasks()
  await fixture.clock.advance(250)
  await eventually(() =>
    assert.equal(
      fixture.runtime.getSnapshot().mediaAttachment?.mediaToken,
      fixture.expected.mediaToken,
    ),
  )

  assert.equal(attempts, 3)
  assert.equal(exact.answers, 1)
  assert.equal(exact.rejections, 0)
  assert.equal(fixture.runtime.getSnapshot().failure, undefined)
})

test("lease loss fences outbound media confirmation retry", async () => {
  const fixture = await outboundMediaFixture()
  let attempts = 0
  fixture.backend.confirmMediaHandler = async () => {
    attempts += 1
    throw new SoftphoneAdapterError(
      "conflict",
      "Provider answer evidence is not committed yet.",
    )
  }
  const exact = mediaLeg({
    providerLegID: "provider-outbound",
    mediaToken: fixture.expected.mediaToken,
  })

  fixture.media.emitIncoming(exact)
  await eventually(() => assert.equal(attempts, 1))
  fixture.backend.lease = lease({
    sessionId: "other-session",
    owner: true,
    activeCallId: fixture.outbound.id,
  })
  fixture.backend.state = callingState({ softphone: fixture.backend.lease })
  const losingOwnership = fixture.runtime.setAvailability(false)
  await drainMicrotasks()
  await fixture.clock.advance(250)
  await losingOwnership

  assert.equal(attempts, 1)
  assert.equal(exact.rejections, 1)
  assert.equal(fixture.runtime.getSnapshot().lease?.owner, false)
  assert.equal(fixture.runtime.getSnapshot().mediaAttachment, undefined)
})

test("stop cancels an outbound media confirmation retry timer", async () => {
  const fixture = await outboundMediaFixture()
  let attempts = 0
  fixture.backend.confirmMediaHandler = async () => {
    attempts += 1
    throw new SoftphoneAdapterError(
      "conflict",
      "Provider answer evidence is not committed yet.",
    )
  }
  const exact = mediaLeg({
    providerLegID: "provider-outbound-stop",
    mediaToken: fixture.expected.mediaToken,
  })

  fixture.media.emitIncoming(exact)
  await eventually(() => assert.equal(attempts, 1))
  await fixture.runtime.stop()
  await drainMicrotasks()

  assert.equal(attempts, 1)
  assert.equal(fixture.clock.pendingTimers, 0)
  assert.equal(fixture.runtime.getSnapshot().phase, "stopped")
})

test("a restored connected Call confirms an exact recovery media leg", async () => {
  const backend = new DeterministicBackend()
  const restored = call({
    id: "call-restored",
    direction: "INBOUND",
    state: "CONNECTED",
    version: 7,
  })
  backend.lease = lease({ owner: true, activeCallId: restored.id })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(restored.id, "restored-leg", restored.version),
  })
  backend.calls.set(restored.id, restored)
  let outboundConfirmations = 0
  backend.confirmMediaHandler = async () => {
    outboundConfirmations += 1
    throw new Error("inbound recovery must not use outbound media confirmation")
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    loadMediaCorrelation: () => ({
      callID: restored.id,
      callLegID: "restored-leg",
      providerLegID: "restored-provider-leg",
      mediaToken: "restored-media-token",
    }),
  })
  await runtime.start()
  assert.equal(runtime.getSnapshot().controls.canEnd, true)
  assert.equal(runtime.getSnapshot().controls.canMute, false)

  const recovery = mediaLeg({
    providerLegID: "restored-provider-leg",
    mediaToken: "restored-media-token",
    recovery: true,
  })
  media.emitIncoming(recovery)
  await eventually(() =>
    assert.equal(
      runtime.getSnapshot().mediaAttachment?.providerLegID,
      recovery.providerLegID,
    ),
  )

  assert.equal(recovery.answers, 1)
  assert.equal(recovery.rejections, 0)
  assert.equal(outboundConfirmations, 0)
  assert.equal(runtime.getSnapshot().controls.canEnd, true)
})

test("authoritative state removes a restored inbound Call after Staff becomes Admin", async () => {
  const backend = new DeterministicBackend()
  const restored = call({
    id: "call-restored-before-admin-role",
    direction: "INBOUND",
    state: "CONNECTED",
    version: 7,
  })
  const correlation = {
    callID: restored.id,
    callLegID: "restored-before-admin-leg",
    providerLegID: "restored-before-admin-provider-leg",
    mediaToken: "restored-before-admin-media-token",
  }
  backend.lease = lease({
    owner: true,
    available: false,
    activeCallId: restored.id,
  })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(restored.id, correlation.callLegID, restored.version),
  })
  backend.calls.set(restored.id, restored)
  const persistedCallIDs: Array<string | undefined> = []
  const persistedMediaTokens: Array<string | undefined> = []
  const media = new DeterministicMedia()
  media.disconnectDeferred = deferred<void>()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    loadCallRecoveryID: () => restored.id,
    persistCallRecoveryID: (callID) => persistedCallIDs.push(callID),
    loadMediaCorrelation: () => correlation,
    persistMediaCorrelation: (value) =>
      persistedMediaTokens.push(value?.mediaToken),
  })
  await runtime.start()

  const recoveryReject = deferred<void>()
  const recovery = mediaLeg({
    providerLegID: correlation.providerLegID,
    mediaToken: correlation.mediaToken,
    recovery: true,
    rejectDeferred: recoveryReject,
  })
  media.emitIncoming(recovery)
  await eventually(() =>
    assert.equal(
      runtime.getSnapshot().mediaAttachment?.mediaToken,
      correlation.mediaToken,
    ),
  )

  const hangupResponse = deferred<CallingCall>()
  backend.hangupHandler = async () => hangupResponse.promise
  const hangingUp = runtime.hangup()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().endingCallID, restored.id),
  )
  await drainMicrotasks()

  let staleDetailReads = 0
  backend.readCallHandler = async () => {
    staleDetailReads += 1
    return restored
  }
  backend.lease = lease({ owner: true, available: false })
  backend.state = callingState({ softphone: backend.lease })
  backend.etag = '"state-after-admin-role"'
  const roleChanged = runtime.signalRefresh()
  await eventually(() =>
    assert.equal(runtime.getSnapshot().activeCall, undefined),
  )

  assert.equal(runtime.getSnapshot().expectedCallID, "")
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)
  assert.equal(runtime.getSnapshot().readiness.mediaState, "unavailable")
  assert.equal(runtime.getSnapshot().controls.canEnd, false)
  assert.equal(runtime.getSnapshot().controls.canMute, false)
  assert.equal(staleDetailReads, 0)
  assert.equal(media.disconnects, 1)
  assert.equal(recovery.rejections, 1)
  assert.equal(persistedCallIDs.at(-1), undefined)
  assert.equal(persistedMediaTokens.at(-1), undefined)

  hangupResponse.resolve(
    call({
      ...restored,
      state: "CONNECTED",
      endRequested: true,
      version: restored.version + 1,
    }),
  )
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(persistedCallIDs.at(-1), undefined)

  media.disconnectDeferred.resolve()
  recoveryReject.resolve()
  await Promise.all([roleChanged, hangingUp])
  await runtime.signalRefresh()
  assert.equal(runtime.getSnapshot().activeCall, undefined)
})

test("a fresh runtime restores exact inbound media while its answer is bridge-pending", async () => {
  const backend = new DeterministicBackend()
  const restored = call({
    id: "call-restored-bridge-pending",
    direction: "INBOUND",
    state: "CONNECTING",
    version: 4,
  })
  const pendingLeg = offer({
    callId: restored.id,
    callLegId: "restored-bridge-pending-leg",
    mediaToken: "restored-bridge-pending-token",
    state: "BRIDGE_PENDING",
    version: restored.version,
  })
  backend.lease = lease({ owner: true, activeCallId: restored.id })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [pendingLeg],
  })
  backend.calls.set(restored.id, restored)
  let outboundConfirmations = 0
  backend.confirmMediaHandler = async () => {
    outboundConfirmations += 1
    throw new Error("inbound recovery must not use outbound media confirmation")
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    loadCallRecoveryID: () => restored.id,
    loadMediaCorrelation: () => ({
      callID: restored.id,
      callLegID: pendingLeg.callLegId,
      providerLegID: "restored-bridge-pending-provider",
      mediaToken: pendingLeg.mediaToken,
    }),
  })
  await runtime.start()

  const recovery = mediaLeg({
    providerLegID: "restored-bridge-pending-provider",
    mediaToken: pendingLeg.mediaToken,
    recovery: true,
  })
  media.emitIncoming(recovery)
  await eventually(() =>
    assert.equal(
      runtime.getSnapshot().mediaAttachment?.callLegID,
      pendingLeg.callLegId,
    ),
  )

  assert.equal(recovery.answers, 1)
  assert.equal(recovery.rejections, 0)
  assert.equal(outboundConfirmations, 0)
})

test("failed or ended same-leg recovery disables media controls until exact recovery succeeds", async (context) => {
  for (const outcome of ["failed", "ended"] as const) {
    await context.test(outcome, async () => {
      const backend = new DeterministicBackend()
      const restored = call({
        id: `call-recovery-${outcome}`,
        direction: "INBOUND",
        state: "CONNECTED",
        version: 7,
      })
      const correlation = {
        callID: restored.id,
        callLegID: `leg-recovery-${outcome}`,
        providerLegID: `provider-recovery-${outcome}`,
        mediaToken: `media-recovery-${outcome}`,
      }
      backend.lease = lease({ owner: true, activeCallId: restored.id })
      backend.state = callingState({
        softphone: backend.lease,
        bridged: stateCall(restored.id, correlation.callLegID, restored.version),
      })
      backend.calls.set(restored.id, restored)
      const persistedMediaTokens: Array<string | undefined> = []
      const media = new DeterministicMedia()
      const runtime = createSoftphoneRuntime({
        sessionID: "session-1",
        backend,
        media,
        microphone: readyMicrophone(),
        clock: new ManualClock(),
        visibility: visible(),
        loadMediaCorrelation: () => correlation,
        persistMediaCorrelation: (value) =>
          persistedMediaTokens.push(value?.mediaToken),
      })
      await runtime.start()

      const attached = mediaLeg({
        providerLegID: correlation.providerLegID,
        mediaToken: correlation.mediaToken,
        recovery: true,
      })
      media.emitIncoming(attached)
      await eventually(() =>
        assert.equal(
          runtime.getSnapshot().mediaAttachment?.mediaToken,
          correlation.mediaToken,
        ),
      )

      media.emitAudioIssue()
      assert.equal(runtime.getSnapshot().failure?.kind, "media")
      const reauthorized = mediaLeg({
        providerLegID: correlation.providerLegID,
        mediaToken: correlation.mediaToken,
        recovery: true,
      })
      media.emitIncoming(reauthorized)
      await eventually(() => assert.equal(runtime.getSnapshot().failure, undefined))
      assert.equal(reauthorized.answers, 1)
      assert.equal(
        runtime.getSnapshot().mediaAttachment?.mediaToken,
        correlation.mediaToken,
      )

      const answerDeferred =
        outcome === "ended" ? deferred<"attached" | "ended">() : undefined
      answerDeferred?.resolve("ended")
      const interrupted = mediaLeg({
        providerLegID: correlation.providerLegID,
        mediaToken: correlation.mediaToken,
        recovery: true,
        failAnswers: outcome === "failed" ? 1 : undefined,
        answerDeferred,
      })
      media.emitIncoming(interrupted)
      await eventually(() =>
        assert.equal(runtime.getSnapshot().mediaAttachment, undefined),
      )

      assert.equal(interrupted.answers, 1)
      assert.equal(
        runtime.getSnapshot().recoveryMedia?.mediaToken,
        correlation.mediaToken,
      )
      assert.equal(persistedMediaTokens.at(-1), correlation.mediaToken)
      assert.equal(runtime.getSnapshot().controls.canMute, false)
      assert.equal(runtime.getSnapshot().controls.canKeypad, false)
      assert.equal(runtime.getSnapshot().failure?.kind, "media")
      assert.equal(runtime.getSnapshot().failure?.recoverable, true)
      assert.match(runtime.getSnapshot().failure?.message ?? "", /Reconnect calling/)

      const retried = mediaLeg({
        providerLegID: correlation.providerLegID,
        mediaToken: correlation.mediaToken,
        recovery: true,
      })
      media.emitIncoming(retried)
      await eventually(() =>
        assert.equal(
          runtime.getSnapshot().mediaAttachment?.mediaToken,
          correlation.mediaToken,
        ),
      )
      assert.equal(retried.answers, 1)
      assert.equal(runtime.getSnapshot().failure, undefined)
    })
  }
})

test("an exact outbound leg queued before the command response drains after correlation", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const committed = call({ id: "call-response-late", state: "PREPARING", version: 1 })
  const response = deferred<CallingCall>()
  backend.startOutboundHandler = async () => response.promise
  backend.confirmMediaHandler = async (input) => {
    assert.equal(input.callID, committed.id)
    assert.equal(input.mediaToken, "response-late-token")
    return call({ id: committed.id, state: "CONNECTED", version: 2 })
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const starting = runtime.startOutbound({
    idempotencyKey: "response-late",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  await drainMicrotasks()
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [
      offer({
        callId: committed.id,
        callLegId: "response-late-leg",
        mediaToken: "response-late-token",
      }),
    ],
  })
  const queued = mediaLeg({
    providerLegID: "response-late-provider-leg",
    mediaToken: "response-late-token",
  })
  media.emitIncoming(queued)
  await drainMicrotasks()
  assert.equal(queued.answers, 0)
  assert.equal(runtime.getSnapshot().pendingCall?.state, "PREPARING")

  backend.calls.set(committed.id, committed)
  response.resolve(committed)
  await starting
  await eventually(() => assert.equal(queued.answers, 1))

  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().activeCall?.state, "CONNECTED")
  assert.equal(runtime.getSnapshot().mediaAttachment?.mediaToken, queued.mediaToken)
})

test("lease loss rejects media whose Answer resolves after ownership moved", async () => {
  const backend = new DeterministicBackend()
  const restored = call({
    id: "call-stale-answer",
    direction: "INBOUND",
    state: "CONNECTED",
    version: 4,
  })
  backend.lease = lease({ owner: true, activeCallId: restored.id })
  backend.state = callingState({
    softphone: backend.lease,
    bridged: stateCall(restored.id, "stale-answer-leg", restored.version),
  })
  backend.calls.set(restored.id, restored)
  const answerDeferred = deferred<"attached" | "ended">()
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
    loadMediaCorrelation: () => ({
      callID: restored.id,
      callLegID: "stale-answer-leg",
      providerLegID: "stale-answer-provider-leg",
      mediaToken: "stale-answer-media-token",
    }),
  })
  await runtime.start()

  const stale = mediaLeg({
    providerLegID: "stale-answer-provider-leg",
    mediaToken: "stale-answer-media-token",
    recovery: true,
    answerDeferred,
  })
  media.emitIncoming(stale)
  await eventually(() => assert.equal(stale.answers, 1))

  backend.lease = lease({ sessionId: "other-session", owner: true })
  backend.state = callingState({ softphone: backend.lease })
  await runtime.signalRefresh()
  answerDeferred.resolve("attached")
  await eventually(() => assert.equal(stale.rejections, 1))

  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)
})

test("stop rejects incoming media whose synchronization resolves after cleanup", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const refreshDeferred = deferred<{
    status: "modified"
    state: CallingState
    etag: string
  }>()
  let refreshStarted = false
  backend.readStateHandler = async () => {
    refreshStarted = true
    return refreshDeferred.promise
  }
  const stale = mediaLeg({
    providerLegID: "stale-refresh-provider-leg",
    mediaToken: "stale-refresh-media-token",
  })
  media.emitIncoming(stale)
  await eventually(() => assert.equal(refreshStarted, true))

  await runtime.stop()
  refreshDeferred.resolve({
    status: "modified",
    state: backend.state,
    etag: backend.etag,
  })
  await eventually(() => assert.equal(stale.rejections, 1))

  assert.equal(runtime.getSnapshot().phase, "stopped")
  assert.equal(runtime.getSnapshot().mediaAttachment, undefined)
})

test("a rejected Answer cannot repaint an offer after lease loss", async () => {
  const backend = new DeterministicBackend()
  const incoming = offer({
    callId: "call-stale-answer-rejection",
    callLegId: "leg-stale-answer-rejection",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease, ringing: [incoming] })
  const answerDeferred = deferred<"attached" | "ended">()
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  const stale = mediaLeg({
    providerLegID: "provider-stale-answer-rejection",
    mediaToken: incoming.mediaToken,
    answerDeferred,
  })
  media.emitIncoming(stale)
  await eventually(() =>
    assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true),
  )

  const answering = runtime.answer(incoming.callLegId)
  await eventually(() => assert.equal(stale.answers, 1))
  backend.lease = lease({ sessionId: "other-session", owner: true })
  backend.state = callingState({ softphone: backend.lease })
  await runtime.signalRefresh()
  answerDeferred.reject(new Error("stale media answer failed"))
  await answering

  assert.equal(stale.rejections, 1)
  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
})

test("concurrent Answer intents invoke the provider for only one CallLeg", async () => {
  const backend = new DeterministicBackend()
  const firstOffer = offer({ callId: "call-first", callLegId: "leg-first" })
  const secondOffer = offer({
    callId: "call-second",
    callLegId: "leg-second",
    mediaToken: "media-token-second",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [firstOffer, secondOffer],
  })
  const firstAnswer = deferred<"attached" | "ended">()
  const firstLeg = mediaLeg({
    providerLegID: "provider-first",
    mediaToken: firstOffer.mediaToken,
    answerDeferred: firstAnswer,
  })
  const secondLeg = mediaLeg({
    providerLegID: "provider-second",
    mediaToken: secondOffer.mediaToken,
  })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()
  media.emitIncoming(firstLeg)
  media.emitIncoming(secondLeg)
  await eventually(() =>
    assert.deepEqual(
      runtime.getSnapshot().offers.map((candidate) => candidate.answerReady),
      [true, true],
    ),
  )

  const answeringFirst = runtime.answer(firstOffer.callLegId)
  await eventually(() => assert.equal(firstLeg.answers, 1))
  await runtime.answer(secondOffer.callLegId)

  assert.equal(firstLeg.answers, 1)
  assert.equal(secondLeg.answers, 0)
  firstAnswer.resolve("ended")
  await answeringFirst
})

test("Answer quarantines simultaneous media until the exact winner commits", async () => {
  const backend = new DeterministicBackend()
  const selectedOffer = offer({
    callId: "call-selected",
    callLegId: "leg-selected",
    mediaToken: "media-selected",
  })
  const losingOffer = offer({
    callId: "call-losing",
    callLegId: "leg-losing",
    mediaToken: "media-losing",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [selectedOffer, losingOffer],
  })
  backend.calls.set(
    selectedOffer.callId,
    call({
      id: selectedOffer.callId,
      direction: "INBOUND",
      state: "CONNECTED",
      version: 2,
    }),
  )
  const selectedLeg = mediaLeg({
    providerLegID: "provider-selected",
    mediaToken: selectedOffer.mediaToken,
  })
  const losingLeg = mediaLeg({
    providerLegID: "provider-losing",
    mediaToken: losingOffer.mediaToken,
  })
  const media = new DeterministicMedia()
  const visibility = new DeterministicVisibility()
  visibility.hidden = true
  const attention = new DeterministicAttention()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility,
    attention,
  })
  await runtime.start()
  media.emitIncoming(selectedLeg)
  media.emitIncoming(losingLeg)
  await eventually(() =>
    assert.deepEqual(
      runtime.getSnapshot().offers.map((candidate) => candidate.answerReady),
      [true, true],
    ),
  )

  await runtime.answer(selectedOffer.callLegId)

  assert.equal(selectedLeg.answers, 1)
  assert.equal(selectedLeg.rejections, 0)
  assert.equal(losingLeg.answers, 0)
  assert.equal(backend.state.bridged, undefined)
  assert.deepEqual(
    backend.state.ringing.map((candidate) => candidate.callLegId),
    [selectedOffer.callLegId, losingOffer.callLegId],
  )
  assert.equal(losingLeg.rejections, 0)
  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(runtime.getSnapshot().expectedCallID, selectedOffer.callId)
  assert.equal(
    runtime.getSnapshot().mediaAttachment?.mediaToken,
    selectedOffer.mediaToken,
  )
  assert.equal(attention.ringtoneStops, attention.ringtoneStarts)
  assert.equal(attention.notificationStops, attention.notificationStarts)

  backend.lease = lease({ owner: true, activeCallId: selectedOffer.callId })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [losingOffer],
    bridged: stateCall(selectedOffer.callId, selectedOffer.callLegId, 2),
  })
  await runtime.signalRefresh()

  assert.equal(runtime.getSnapshot().activeCall?.id, selectedOffer.callId)
  assert.equal(runtime.getSnapshot().offers.length, 0)
  assert.equal(losingLeg.rejections, 1)
  assert.equal(attention.ringtoneStops, attention.ringtoneStarts)
})

test("a selected Answer that loses restores another still-ringing exact leg", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  const selectedOffer = offer({
    callId: "call-selected-loses",
    callLegId: "leg-selected-loses",
    mediaToken: "media-selected-loses",
    deadline: "1970-01-01T00:00:20.000Z",
  })
  const remainingOffer = offer({
    callId: "call-remains-ringing",
    callLegId: "leg-remains-ringing",
    mediaToken: "media-remains-ringing",
    deadline: "1970-01-01T00:00:20.000Z",
  })
  backend.lease = lease({ owner: true })
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [selectedOffer, remainingOffer],
  })
  backend.calls.set(
    selectedOffer.callId,
    call({
      id: selectedOffer.callId,
      direction: "INBOUND",
      state: "CONNECTING",
    }),
  )
  backend.calls.set(
    remainingOffer.callId,
    call({
      id: remainingOffer.callId,
      direction: "INBOUND",
      state: "CONNECTING",
    }),
  )
  const selectedReject = deferred<void>()
  const selectedLeg = mediaLeg({
    providerLegID: "provider-selected-loses",
    mediaToken: selectedOffer.mediaToken,
    rejectDeferred: selectedReject,
  })
  const remainingLeg = mediaLeg({
    providerLegID: "provider-remains-ringing",
    mediaToken: remainingOffer.mediaToken,
  })
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  media.emitIncoming(selectedLeg)
  media.emitIncoming(remainingLeg)
  await eventually(() =>
    assert.deepEqual(
      runtime.getSnapshot().offers.map((candidate) => candidate.answerReady),
      [true, true],
    ),
  )

  await runtime.answer(selectedOffer.callLegId)
  assert.equal(remainingLeg.rejections, 0)
  backend.readStateHandler = async () => ({
    status: "not-modified" as const,
    etag: backend.etag,
  })
  await clock.advance(5_000)
  await runtime.signalRefresh()
  assert.equal(remainingLeg.rejections, 0)

  backend.readStateHandler = undefined
  backend.state = callingState({
    softphone: backend.lease,
    ringing: [remainingOffer],
  })
  await runtime.signalRefresh()

  assert.equal(selectedLeg.rejections, 1)
  assert.equal(runtime.getSnapshot().offers.length, 1)
  assert.equal(runtime.getSnapshot().offers[0]?.callLegId, remainingOffer.callLegId)
  assert.equal(runtime.getSnapshot().offers[0]?.answerReady, true)
  assert.equal(remainingLeg.rejections, 0)
  selectedReject.resolve()
})

test("a delayed outbound response cannot repaint a Call after lease loss", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const response = deferred<CallingCall>()
  backend.startOutboundHandler = async () => response.promise
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  let repaintedAfterLeaseLoss = false
  runtime.subscribe(() => {
    const snapshot = runtime.getSnapshot()
    if (
      snapshot.lease?.owner === false &&
      (snapshot.expectedCallID || snapshot.activeCall || snapshot.pendingCall)
    ) {
      repaintedAfterLeaseLoss = true
    }
  })
  await runtime.start()

  const starting = runtime.startOutbound({
    idempotencyKey: "stale-outbound-response",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  await eventually(() => assert.notEqual(runtime.getSnapshot().pendingCall, undefined))
  backend.lease = lease({ sessionId: "other-session", owner: true })
  backend.state = callingState({ softphone: backend.lease })
  await runtime.signalRefresh()

  response.resolve(call({ id: "stale-outbound-call", state: "PREPARING" }))
  await starting

  assert.equal(runtime.getSnapshot().lease?.owner, false)
  assert.equal(runtime.getSnapshot().expectedCallID, "")
  assert.equal(runtime.getSnapshot().activeCall, undefined)
  assert.equal(runtime.getSnapshot().pendingCall, undefined)
  assert.equal(runtime.getSnapshot().failure?.kind, "ownership")
  assert.equal(repaintedAfterLeaseLoss, false)
})

test("a lost outbound response reconciles the committed Call without a false failure", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const committed = call({
    id: "call-lost-outbound-response",
    direction: "OUTBOUND",
    state: "PREPARING",
    version: 1,
  })
  backend.startOutboundHandler = async () => {
    backend.lease = lease({ owner: true, activeCallId: committed.id })
    backend.state = callingState({
      softphone: backend.lease,
      ringing: [
        offer({
          callId: committed.id,
          callLegId: "leg-lost-outbound-response",
          mediaToken: "media-lost-outbound-response",
        }),
      ],
    })
    backend.calls.set(committed.id, committed)
    throw new SoftphoneAdapterError(
      "temporary-request",
      "The committed response was lost.",
      true,
    )
  }
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  await runtime.startOutbound({
    idempotencyKey: "lost-outbound-response",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })

  assert.equal(runtime.getSnapshot().activeCall?.id, committed.id)
  assert.equal(runtime.getSnapshot().expectedCallID, committed.id)
  assert.equal(runtime.getSnapshot().pendingCall, undefined)
  assert.equal(runtime.getSnapshot().failure, undefined)
})

test("pending intent closes capacity without falsifying technical readiness", async () => {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const outbound = deferred<CallingCall>()
  backend.startOutboundHandler = async () => outbound.promise
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media: new DeterministicMedia(),
    microphone: readyMicrophone(),
    availabilityIntent: true,
    clock,
    visibility: visible(),
  })
  await runtime.start()
  const readsBeforeIntent = backend.reads.length
  const starting = runtime.startOutbound({
    idempotencyKey: "pending-capacity",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  await drainMicrotasks()

  assert.equal(backend.reads.length, readsBeforeIntent + 1)
  await runtime.signalRefresh()
  const readsAfterSignal = backend.reads.length
  await clock.advance(500)
  await eventually(() =>
    assert.equal(backend.reads.length, readsAfterSignal + 2),
  )

  await clock.advance(3_500)
  const pendingWrite = backend.readinessWrites.at(-1)!
  assert.equal(pendingWrite.available, false)
  assert.equal(pendingWrite.registered, true)
  assert.equal(pendingWrite.microphoneReady, true)
  assert.equal(pendingWrite.audioReady, true)
  assert.equal(pendingWrite.sessionHealthy, true)

  const committed = call({ id: "pending-capacity-call", state: "PREPARING" })
  backend.calls.set(committed.id, committed)
  outbound.resolve(committed)
  await starting
})

test("the stable pending Call projection spans outbound intent and inbound Answer", async () => {
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const outboundDeferred = deferred<CallingCall>()
  backend.startOutboundHandler = async () => outboundDeferred.promise
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await runtime.start()

  const starting = runtime.startOutbound({
    idempotencyKey: "outbound-intent",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  await drainMicrotasks()
  assert.equal(runtime.getSnapshot().pendingCall?.state, "PREPARING")
  assert.equal(runtime.getSnapshot().pendingCall?.phone, "+15551234567")
  assert.equal(runtime.getSnapshot().pendingCall?.practiceId, "practice-1")
  assert.equal(runtime.getSnapshot().pendingCall?.locationId, "location-1")

  const outbound = call({ id: "call-outbound", state: "PREPARING", version: 1 })
  backend.calls.set(outbound.id, outbound)
  outboundDeferred.resolve(outbound)
  await starting
  assert.equal(runtime.getSnapshot().pendingCall, undefined)
  assert.equal(runtime.getSnapshot().activeCall?.id, outbound.id)

  await runtime.stop()
  const incoming = offer({ callId: "call-inbound", callLegId: "leg-inbound" })
  const incomingBackend = new DeterministicBackend()
  incomingBackend.lease = lease({ owner: true })
  incomingBackend.state = callingState({
    softphone: incomingBackend.lease,
    ringing: [incoming],
  })
  const incomingMedia = new DeterministicMedia()
  const answerDeferred = deferred<"attached" | "ended">()
  const incomingLeg = mediaLeg({
    providerLegID: "provider-inbound",
    mediaToken: incoming.mediaToken,
    answerDeferred,
  })
  const inboundRuntime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend: incomingBackend,
    media: incomingMedia,
    microphone: readyMicrophone(),
    clock: new ManualClock(),
    visibility: visible(),
  })
  await inboundRuntime.start()
  incomingMedia.emitIncoming(incomingLeg)
  await eventually(() =>
    assert.equal(inboundRuntime.getSnapshot().offers[0]?.answerReady, true),
  )

  const answering = inboundRuntime.answer(incoming.callLegId)
  await drainMicrotasks()
  assert.equal(inboundRuntime.getSnapshot().pendingCall?.state, "CONNECTING")
  assert.equal(inboundRuntime.getSnapshot().pendingCall?.id, incoming.callId)
  assert.equal(
    inboundRuntime.getSnapshot().pendingCall?.practiceId,
    incoming.practiceId,
  )
  assert.equal(
    inboundRuntime.getSnapshot().pendingCall?.locationId,
    incoming.locationId,
  )
  assert.equal(inboundRuntime.getSnapshot().offers.length, 0)

  answerDeferred.resolve("ended")
  await answering
  assert.equal(inboundRuntime.getSnapshot().pendingCall, undefined)
})

class DeterministicBackend implements SoftphoneBackend {
  lease = lease({ owner: false })
  state = callingState({ softphone: this.lease })
  calls = new Map<string, CallingCall>()
  reads: Array<{ etag?: string; at: number }> = []
  etag = '"state-1"'
  now = 0
  readinessWrites: CallingReadinessRequest[] = []
  leaseRequests: Array<{ sessionID: string; takeover: boolean }> = []
  mediaTokenRequests = 0
  acquireLeaseHandler?: SoftphoneBackend["acquireLease"]
  issueMediaTokenHandler?: SoftphoneBackend["issueMediaToken"]
  writeReadinessHandler?: (
    input: CallingReadinessRequest,
    signal?: AbortSignal,
  ) => Promise<SoftphoneState> | SoftphoneState
  readStateHandler?: (
    input: { etag?: string },
    signal?: AbortSignal,
  ) => ReturnType<SoftphoneBackend["readState"]>
  readCallHandler?: SoftphoneBackend["readCall"]
  confirmMediaHandler?: SoftphoneBackend["confirmMedia"]
  startOutboundHandler?: SoftphoneBackend["startOutbound"]
  hangupHandler?: SoftphoneBackend["hangup"]
  retryHandler?: SoftphoneBackend["retry"]
  disposeHandler?: SoftphoneBackend["dispose"]

  async acquireLease(
    input: { sessionID: string; takeover: boolean },
    signal?: AbortSignal,
  ) {
    this.leaseRequests.push(input)
    if (this.acquireLeaseHandler) return this.acquireLeaseHandler(input, signal)
    return this.lease
  }

  async writeReadiness(input: CallingReadinessRequest, signal?: AbortSignal) {
    this.readinessWrites.push(input)
    this.lease = this.writeReadinessHandler
      ? await this.writeReadinessHandler(input, signal)
      : { ...this.lease, available: input.available }
    this.state = { ...this.state, softphone: this.lease }
    return this.lease
  }

  async issueMediaToken(_input?: { sessionID: string }, signal?: AbortSignal) {
    this.mediaTokenRequests += 1
    if (this.issueMediaTokenHandler) {
      return this.issueMediaTokenHandler({ sessionID: "session-1" }, signal)
    }
    return "media-credential"
  }

  async readState(input: { etag?: string }, signal?: AbortSignal) {
    if (this.readStateHandler) return this.readStateHandler(input, signal)
    this.reads.push({ etag: input.etag, at: this.now })
    return { status: "modified" as const, state: this.state, etag: this.etag }
  }

  async readCall(callID: string, signal?: AbortSignal) {
    if (this.readCallHandler) return this.readCallHandler(callID, signal)
    const found = this.calls.get(callID)
    if (!found) throw new Error(`missing Call ${callID}`)
    return found
  }

  async confirmMedia(input: {
    callID: string
    sessionID: string
    mediaToken: string
  }, signal?: AbortSignal): Promise<CallingCall> {
    if (this.confirmMediaHandler) return this.confirmMediaHandler(input, signal)
    throw new Error("not implemented")
  }

  async startOutbound(
    input: StartOutboundCallRequest,
    signal?: AbortSignal,
  ): Promise<CallingCall> {
    if (this.startOutboundHandler) return this.startOutboundHandler(input, signal)
    throw new Error("not implemented")
  }

  async hangup(
    input: { callID: string; sessionID: string },
    signal?: AbortSignal,
  ): Promise<CallingCall> {
    if (this.hangupHandler) return this.hangupHandler(input, signal)
    throw new Error("not implemented")
  }

  async retry(input: {
    callID: string
    sessionID: string
    idempotencyKey: string
  }, signal?: AbortSignal): Promise<CallingCall> {
    if (this.retryHandler) return this.retryHandler(input, signal)
    throw new Error("not implemented")
  }

  async dispose(input: {
    callID: string
    sessionID: string
    outcome: "RESOLVED" | "FOLLOW_UP_REQUIRED" | "COMPLETE_TASK" | "KEEP_OPEN" | "CREATE_TASK" | "NO_FOLLOW_UP"
  }, signal?: AbortSignal): Promise<CallingDispositionResult> {
    if (this.disposeHandler) return this.disposeHandler(input, signal)
    throw new Error("not implemented")
  }
}

class DeterministicMedia implements CallingMediaAdapter {
  connects = 0
  disconnects = 0
  connectDeferred?: ReturnType<typeof deferred<void>>
  disconnectDeferred?: ReturnType<typeof deferred<void>>
  connectError?: Error
  callbacks?: {
    onState: (state: MediaState) => void
    onIncoming: (leg: IncomingMediaLeg) => void
    onEnded?: (leg: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">) => void
    onAudioIssue?: () => void
    onFailure?: (failure: "authentication" | "network" | "provider") => void
    refreshToken?: () => Promise<string | undefined>
  }

  async connect(
    _token: string,
    _remoteElement: string,
    callbacks: NonNullable<DeterministicMedia["callbacks"]>,
  ) {
    this.connects += 1
    this.callbacks = callbacks
    if (this.connectDeferred) await this.connectDeferred.promise
    if (this.connectError) throw this.connectError
    callbacks.onState("ready")
  }

  async disconnect() {
    this.disconnects += 1
    if (this.disconnectDeferred) await this.disconnectDeferred.promise
  }

  emitState(state: MediaState) {
    this.callbacks?.onState(state)
  }

  emitIncoming(leg: IncomingMediaLeg) {
    this.callbacks?.onIncoming(leg)
  }

  async refreshCredential() {
    return this.callbacks?.refreshToken?.()
  }

  emitEnded(leg: Pick<IncomingMediaLeg, "providerLegID" | "mediaToken">) {
    this.callbacks?.onEnded?.(leg)
  }

  emitAudioIssue() {
    this.callbacks?.onAudioIssue?.()
  }

  emitFailure(failure: "authentication" | "network" | "provider") {
    this.callbacks?.onFailure?.(failure)
  }
}

class ManualClock implements SoftphoneClock {
  now = 0
  private nextID = 1
  private timers = new Map<number, { at: number; callback: () => void }>()

  setTimeout(callback: () => void, milliseconds: number) {
    const id = this.nextID++
    this.timers.set(id, { at: this.now + milliseconds, callback })
    return id
  }

  clearTimeout(id: number) {
    this.timers.delete(id)
  }

  get pendingTimers() {
    return this.timers.size
  }

  async advance(milliseconds: number) {
    const target = this.now + milliseconds
    while (true) {
      const next = [...this.timers.entries()]
        .filter(([, timer]) => timer.at <= target)
        .sort((left, right) => left[1].at - right[1].at)[0]
      if (!next) break
      this.now = next[1].at
      this.timers.delete(next[0])
      next[1].callback()
      await drainMicrotasks()
    }
    this.now = target
    await drainMicrotasks()
  }
}

function lease(overrides: Partial<SoftphoneState>): SoftphoneState {
  return {
    sessionId: "session-1",
    leaseExpiresAt: "2026-08-27T00:01:00Z",
    owner: true,
    available: false,
    activeCallId: "",
    pendingOutcomeCallId: "",
    ...overrides,
  }
}

function callingState(overrides: Partial<CallingState>): CallingState {
  return {
    softphone: lease({ owner: true }),
    ringing: [],
    ...overrides,
  }
}

function stateCall(callID: string, callLegID: string, version: number) {
  return {
    callId: callID,
    callLegId: callLegID,
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Main",
    state: "BRIDGED",
    version,
  }
}

function call(overrides: Partial<CallingCall>): CallingCall {
  return {
    id: "call-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Main",
    direction: "OUTBOUND",
    entryPoint: "STANDALONE",
    state: "CONNECTED",
    phone: "+15551234567",
    phoneSource: "STAFF",
    displayName: "Taylor",
    nameSource: "STAFF",
    transferReason: "",
    reasonSource: "",
    providerTermination: "",
    endRequested: false,
    callerId: "+15557654321",
    retryAllowed: false,
    version: 1,
    ...overrides,
  }
}

function offer(overrides: Partial<CallingState["ringing"][number]>) {
  return {
    callId: "call-inbound",
    callLegId: "durable-leg-1",
    mediaToken: "media-token-1",
    practiceId: "practice-1",
    locationId: "location-1",
    locationName: "Main",
    displayName: "Morgan",
    phone: "+15551234567",
    transferReason: "Scheduling help",
    state: "RINGING" as const,
    version: 1,
    createdAt: "2026-08-27T00:00:00Z",
    deadline: "2026-08-27T00:00:20Z",
    ...overrides,
  }
}

async function outboundMediaFixture() {
  const clock = new ManualClock()
  const backend = new DeterministicBackend()
  backend.lease = lease({ owner: true })
  backend.state = callingState({ softphone: backend.lease })
  const outbound = call({
    id: "call-outbound-media",
    direction: "OUTBOUND",
    state: "PREPARING",
    version: 1,
  })
  const expected = offer({
    callId: outbound.id,
    callLegId: "leg-outbound-media",
    mediaToken: "media-outbound-exact",
    state: "RINGING",
  })
  backend.startOutboundHandler = async () => {
    backend.lease = lease({ owner: true, activeCallId: outbound.id })
    backend.state = callingState({
      softphone: backend.lease,
      ringing: [expected],
    })
    backend.calls.set(outbound.id, outbound)
    return outbound
  }
  const media = new DeterministicMedia()
  const runtime = createSoftphoneRuntime({
    sessionID: "session-1",
    backend,
    media,
    microphone: readyMicrophone(),
    clock,
    visibility: visible(),
  })
  await runtime.start()
  await runtime.startOutbound({
    idempotencyKey: "outbound-media",
    practiceId: "practice-1",
    locationId: "location-1",
    destination: "+15551234567",
  })
  assert.equal(
    runtime.getSnapshot().expectedMedia?.mediaToken,
    expected.mediaToken,
  )
  return { backend, clock, expected, media, outbound, runtime }
}

async function attachedOutboundMediaFixture(providerLegID: string) {
  const fixture = await outboundMediaFixture()
  const connected = call({
    id: fixture.outbound.id,
    direction: "OUTBOUND",
    state: "CONNECTED",
    version: 2,
  })
  fixture.backend.confirmMediaHandler = async () => {
    fixture.backend.lease = lease({
      owner: true,
      available: false,
      activeCallId: connected.id,
    })
    fixture.backend.state = callingState({
      softphone: fixture.backend.lease,
      bridged: stateCall(connected.id, fixture.expected.callLegId, connected.version),
    })
    fixture.backend.calls.set(connected.id, connected)
    return connected
  }
  const exact = mediaLeg({
    providerLegID,
    mediaToken: fixture.expected.mediaToken,
  })
  fixture.media.emitIncoming(exact)
  await eventually(() =>
    assert.equal(
      fixture.runtime.getSnapshot().mediaAttachment?.mediaToken,
      exact.mediaToken,
    ),
  )
  return { ...fixture, connected, exact }
}

function setAttachedOutboundTerminal(
  fixture: Awaited<ReturnType<typeof attachedOutboundMediaFixture>>,
) {
  const terminal = call({
    id: fixture.connected.id,
    direction: "OUTBOUND",
    state: "NEEDS_DISPOSITION",
    version: fixture.connected.version + 1,
  })
  fixture.backend.lease = lease({
    owner: true,
    available: false,
    pendingOutcomeCallId: terminal.id,
  })
  fixture.backend.state = callingState({
    softphone: fixture.backend.lease,
    disposition: {
      ...stateCall(terminal.id, fixture.expected.callLegId, terminal.version),
      state: "NEEDS_DISPOSITION",
    },
  })
  fixture.backend.calls.set(terminal.id, terminal)
  return terminal
}

function mediaLeg(overrides: {
  providerLegID: string
  mediaToken: string
  recovery?: boolean
  failAnswers?: number
  answerDeferred?: ReturnType<typeof deferred<"attached" | "ended">>
  rejectDeferred?: ReturnType<typeof deferred<void>>
  failRejects?: number
}) {
  return {
    ...overrides,
    recovery: overrides.recovery ?? false,
    failAnswers: overrides.failAnswers ?? 0,
    failRejects: overrides.failRejects ?? 0,
    answers: 0,
    rejections: 0,
    mutes: 0,
    digits: [] as string[],
    async answer() {
      this.answers += 1
      if (this.failAnswers > 0) {
        this.failAnswers -= 1
        throw new Error("microphone could not attach")
      }
      if (this.answerDeferred) return this.answerDeferred.promise
      return "attached" as const
    },
    async reject() {
      this.rejections += 1
      if (this.failRejects > 0) {
        this.failRejects -= 1
        throw new Error("provider media could not purge")
      }
      if (this.rejectDeferred) await this.rejectDeferred.promise
    },
    mute() {
      this.mutes += 1
    },
    unmute() {
      this.mutes -= 1
    },
    sendDTMF(digit: string) {
      this.digits.push(digit)
      return true
    },
  }
}

function readyMicrophone() {
  return {
    start: async () => ({ stop: () => {} }),
  }
}

function visible() {
  return {
    isHidden: () => false,
    subscribe: () => () => {},
  }
}

function controllableMicrophone() {
  return {
    starts: 0,
    stops: 0,
    unavailable: undefined as (() => void) | undefined,
    async start(onUnavailable: () => void) {
      this.starts += 1
      this.unavailable = onUnavailable
      return { stop: () => (this.stops += 1) }
    },
    lose() {
      this.unavailable?.()
    },
  }
}

class DeterministicVisibility {
  hidden = false
  private listeners = new Set<() => void>()

  isHidden = () => this.hidden

  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  get listenerCount() {
    return this.listeners.size
  }

  emit() {
    for (const listener of this.listeners) listener()
  }
}

class DeterministicAttention {
  ringtoneStarts = 0
  ringtoneStops = 0
  notificationStarts = 0
  notificationStops = 0

  startRingtone = () => {
    this.ringtoneStarts += 1
    return () => {
      this.ringtoneStops += 1
    }
  }

  showIncomingNotification = () => {
    this.notificationStarts += 1
    return () => {
      this.notificationStops += 1
    }
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}

async function drainMicrotasks() {
  for (let count = 0; count < 12; count += 1) await Promise.resolve()
}

async function eventually(assertion: () => void) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    try {
      assertion()
      return
    } catch (error) {
      if (attempt === 99) throw error
      await drainMicrotasks()
    }
  }
}
