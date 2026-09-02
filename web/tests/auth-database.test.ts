import assert from "node:assert/strict"
import test from "node:test"

import { createAuthDatabasePool } from "../src/lib/auth-database.ts"

const connectionString = process.env.TEST_DATABASE_URL
if (!connectionString || !new URL(connectionString).pathname.endsWith("_test")) {
  throw new Error("TEST_DATABASE_URL must name a disposable database ending in _test")
}

test("a slow auth statement releases the only connection for the next request", async () => {
  const pool = createAuthDatabasePool({
    connectionString,
    max: 1,
    connectionTimeoutMillis: 1_500,
  })
  try {
    const started = performance.now()
    await assert.rejects(pool.query("SELECT pg_sleep(7)"), { code: "57014" })
    assert.ok(performance.now() - started < 6_500, "the server must cancel the slow statement")
    const result = await pool.query("SELECT 1 AS ready")
    assert.equal(result.rows[0].ready, 1)
    assert.equal(pool.totalCount, 1)
    assert.equal(pool.idleCount, 1)
  } finally {
    await pool.end()
  }
})
