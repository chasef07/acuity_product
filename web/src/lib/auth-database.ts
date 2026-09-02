import { Pool } from "pg"

type AuthDatabaseConfig = {
  connectionString: string
  max: number
  connectionTimeoutMillis: number
}

export function createAuthDatabasePool(config: AuthDatabaseConfig): Pool {
  return new Pool({
    ...config,
    idleTimeoutMillis: 30_000,
    maxLifetimeSeconds: 300,
    // Auth uses its own small pool. Acquisition deadlines alone cannot stop a
    // slow statement from occupying its only available connection.
    statement_timeout: 5_000,
    options: "-c search_path=auth",
  })
}
