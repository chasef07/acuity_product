import { betterAuth } from "better-auth"
import { bearer, jwt, oauthPopup, testUtils } from "better-auth/plugins"
import { Pool } from "pg"

import { createUserEligibilityGate } from "@/lib/auth-eligibility"
import { portalAuthenticationConfiguration } from "@/lib/auth-providers"
import { positiveInteger, required } from "@/lib/server-env"

export function createAuth() {
  const baseURL = required("BETTER_AUTH_URL")
  const portalAPIURL = required("PORTAL_API_INTERNAL_URL")
  const audience = required("PORTAL_API_AUDIENCE")
  const authentication = portalAuthenticationConfiguration()
  const pool = new Pool({
    connectionString: required("AUTH_DATABASE_URL"),
    max: positiveInteger("AUTH_DB_POOL_MAX"),
    connectionTimeoutMillis: positiveInteger("AUTH_DB_ACQUIRE_TIMEOUT_MS"),
    idleTimeoutMillis: 30_000,
    maxLifetimeSeconds: 300,
    options: "-c search_path=auth",
  })

  return betterAuth({
    appName: "Acuity Portal",
    baseURL,
    secret: authSecret(),
    trustedOrigins: commaSeparated("BETTER_AUTH_TRUSTED_ORIGINS"),
    database: pool,
    socialProviders: authentication.socialProviders,
    databaseHooks: {
      user: {
        create: {
          before: createUserEligibilityGate({ portalAPIURL }),
        },
      },
    },
    rateLimit: {
      enabled: process.env.AUTH_ALLOW_TEST_SESSION !== "true",
    },
    plugins: [
      oauthPopup(),
      bearer({ requireSignature: true }),
      jwt({
        jwt: {
          issuer: baseURL,
          audience,
          expirationTime: "15m",
          definePayload: ({ user }) => ({
            email: user.email,
            email_verified: user.emailVerified,
          }),
        },
        jwks: {
          rotationInterval: 60 * 60 * 24 * 30,
          gracePeriod: 60 * 60 * 24 * 30,
        },
      }),
      ...(process.env.AUTH_ALLOW_TEST_SESSION === "true" ? [testUtils()] : []),
    ],
    telemetry: { enabled: false },
  })
}

function authSecret(): string {
  const value = required("BETTER_AUTH_SECRET")
  if (value.length < 32) {
    throw new Error("BETTER_AUTH_SECRET must be at least 32 characters")
  }
  return value
}

function commaSeparated(name: string): string[] {
  return required(name)
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean)
}
