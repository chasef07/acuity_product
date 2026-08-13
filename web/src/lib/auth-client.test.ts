import assert from "node:assert/strict"
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
