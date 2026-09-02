import assert from "node:assert/strict"
import { createServer } from "node:http"
import test from "node:test"

import {
  clearAccessToken,
  getAccessToken,
  getAccessTokenResult,
} from "./auth-client.ts"

test("one valid access token serves concurrent callers until it is invalidated", async () => {
  const originalFetch = globalThis.fetch
  const firstToken = tokenWithExpiration(4_102_444_800, "first")
  const secondToken = tokenWithExpiration(4_102_444_800, "second")
  let requests = 0
  let releaseFirstRequest = () => {}
  const firstRequestPending = new Promise<void>((resolve) => {
    releaseFirstRequest = resolve
  })

  globalThis.fetch = async () => {
    requests += 1
    if (requests === 1) await firstRequestPending
    return Response.json({ token: requests === 1 ? firstToken : secondToken })
  }
  clearAccessToken()

  try {
    const concurrent = Array.from({ length: 30 }, () => getAccessToken())
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.equal(requests, 1)

    releaseFirstRequest()
    assert.deepEqual(await Promise.all(concurrent), Array(30).fill(firstToken))
    assert.equal(await getAccessToken(), firstToken)
    assert.equal(requests, 1)

    clearAccessToken()
    assert.equal(await getAccessToken(), secondToken)
    assert.equal(requests, 2)
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("a transient refresh failure keeps using an unexpired access token", async () => {
  const originalFetch = globalThis.fetch
  const token = tokenWithExpiration(
    Math.ceil(Date.now() / 1_000) + 30,
    "near-expiration",
  )
  let requests = 0

  globalThis.fetch = async () => {
    requests += 1
    return requests === 1
      ? Response.json({ token })
      : new Response(null, { status: 503 })
  }
  clearAccessToken()

  try {
    assert.equal(await getAccessToken(), token)
    assert.equal(await getAccessToken(), token)
    assert.equal(await getAccessToken(), token)
    assert.equal(requests, 2)
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("a stalled token refresh releases concurrent callers with the unexpired token", async () => {
  await assertStalledRefreshRecovery(false)
})

test("a stalled token response body keeps the unexpired token after headers arrive", async () => {
  await assertStalledRefreshRecovery(true)
})

async function assertStalledRefreshRecovery(stallBody: boolean) {
  const originalFetch = globalThis.fetch
  const token = tokenWithExpiration(Math.ceil(Date.now() / 1_000) + 30, "stalled-refresh")
  let requests = 0
  let fallback: ReturnType<typeof setTimeout> | undefined
  const server = createServer((_request, response) => {
    requests += 1
    if (requests === 1) {
      response.writeHead(200, { "content-type": "application/json" })
      response.end(JSON.stringify({ token }))
      return
    }
    if (stallBody) {
      response.writeHead(200, { "content-type": "application/json" })
      response.flushHeaders()
    }
    // Keep the pre-fix failure bounded too: it waits for this response instead
    // of ending the stalled request within the Calling readiness grace period.
    fallback = setTimeout(() => {
      if (!stallBody) response.writeHead(503)
      response.end()
    }, 6_500)
  })
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve))
  const address = server.address()
  assert.ok(address && typeof address !== "string")
  globalThis.fetch = (_input, init) =>
    originalFetch(`http://127.0.0.1:${address.port}/api/auth/token`, init)
  clearAccessToken()

  try {
    assert.equal(await getAccessToken(), token)
    const started = performance.now()
    const results = await Promise.all([getAccessTokenResult(), getAccessTokenResult()])
    assert.deepEqual(results, Array(2).fill({ status: "authenticated", token }))
    assert.ok(performance.now() - started < 6_000, "token acquisition must not wait indefinitely")
    assert.equal(requests, 2, "concurrent callers share one bounded refresh")
    assert.equal(await getAccessToken(), token, "backoff keeps the still-valid token")
    assert.equal(requests, 2)
  } finally {
    clearTimeout(fallback)
    clearAccessToken()
    globalThis.fetch = originalFetch
    server.closeAllConnections()
    await new Promise<void>((resolve) => server.close(() => resolve()))
  }
}

test("invalidation during a token response body cannot restore the old token", async () => {
  const originalFetch = globalThis.fetch
  const oldToken = tokenWithExpiration(4_102_444_800, "old-session")
  const newToken = tokenWithExpiration(4_102_444_800, "new-session")
  let body: ReadableStreamDefaultController<Uint8Array> | undefined
  let requests = 0
  globalThis.fetch = async () => {
    requests += 1
    return requests === 1
      ? new Response(new ReadableStream<Uint8Array>({ start(controller) { body = controller } }))
      : Response.json({ token: newToken })
  }
  clearAccessToken()
  try {
    const pending = getAccessTokenResult()
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.ok(body)
    clearAccessToken()
    body.enqueue(new TextEncoder().encode(JSON.stringify({ token: oldToken })))
    body.close()
    assert.deepEqual(await pending, { status: "unauthenticated" })
    assert.equal(await getAccessToken(), newToken)
    assert.equal(requests, 2)
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("malformed token JSON stays unavailable instead of using the cached token", async () => {
  const originalFetch = globalThis.fetch
  const token = tokenWithExpiration(Math.ceil(Date.now() / 1_000) + 30, "malformed-refresh")
  let requests = 0
  globalThis.fetch = async () => {
    requests += 1
    return requests === 1 ? Response.json({ token }) : new Response("not json")
  }
  clearAccessToken()
  try {
    assert.equal(await getAccessToken(), token)
    assert.deepEqual(await getAccessTokenResult(), { status: "unavailable" })
    assert.equal(requests, 2)
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("initial token acquisition retries one transient failure", async () => {
  const originalFetch = globalThis.fetch
  const token = tokenWithExpiration(4_102_444_800, "retried")
  let requests = 0

  globalThis.fetch = async () => {
    requests += 1
    return requests === 1
      ? new Response(null, {
          status: 503,
          headers: { "X-Retry-After": "0.001" },
        })
      : Response.json({ token })
  }
  clearAccessToken()

  try {
    const firstCaller = getAccessToken()
    await new Promise((resolve) => setTimeout(resolve, 10))
    assert.equal(requests, 1)
    const staggeredCaller = getAccessToken()
    assert.deepEqual(await Promise.all([firstCaller, staggeredCaller]), [
      token,
      token,
    ])
    assert.equal(requests, 2)
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("transient token failures are not reported as authentication loss", async () => {
  const originalFetch = globalThis.fetch

  try {
    for (const failure of ["network", "rate-limit", "server"] as const) {
      globalThis.fetch = async () => {
        if (failure === "network") throw new TypeError("network unavailable")
        return new Response(null, {
          status: failure === "rate-limit" ? 429 : 503,
          headers: { "X-Retry-After": "0.001" },
        })
      }
      clearAccessToken()
      assert.deepEqual(
        await getAccessTokenResult(),
        { status: "unavailable" },
        failure,
      )
    }
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

test("rejected token sessions are reported as authentication loss", async () => {
  const originalFetch = globalThis.fetch

  try {
    for (const status of [401, 403]) {
      globalThis.fetch = async () => new Response(null, { status })
      clearAccessToken()
      assert.deepEqual(await getAccessTokenResult(), {
        status: "unauthenticated",
      })
    }
  } finally {
    clearAccessToken()
    globalThis.fetch = originalFetch
  }
})

function tokenWithExpiration(expiration: number, marker: string) {
  const payload = Buffer.from(
    JSON.stringify({ exp: expiration, marker }),
  ).toString("base64url")
  return `header.${payload}.signature`
}
