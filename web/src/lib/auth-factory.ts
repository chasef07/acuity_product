import { APIError, createAuthMiddleware } from "better-auth/api"
import { betterAuth } from "better-auth"
import { jwt } from "better-auth/plugins"
import { Pool } from "pg"

import {
  createUserEligibilityGate,
  isSignUpEligible,
} from "@/lib/auth-eligibility"
import { googleProviderConfiguration } from "@/lib/auth-providers"
import { getAuthEmailSender, type AuthEmailKind } from "@/lib/email"
import {
  PASSWORD_MAX_LENGTH,
  PASSWORD_MIN_LENGTH,
} from "@/lib/password-policy"
import { positiveInteger, required } from "@/lib/server-env"

export function createAuth() {
  const baseURL = required("BETTER_AUTH_URL")
  const portalAPIURL = required("PORTAL_API_INTERNAL_URL")
  const audience = required("PORTAL_API_AUDIENCE")
  const emailSender = getAuthEmailSender()
  const pool = new Pool({
    connectionString: required("AUTH_DATABASE_URL"),
    max: positiveInteger("AUTH_DB_POOL_MAX"),
    connectionTimeoutMillis: positiveInteger("AUTH_DB_ACQUIRE_TIMEOUT_MS"),
    idleTimeoutMillis: 30_000,
    maxLifetimeSeconds: 300,
    options: "-c search_path=auth",
  })

  const deliver = async (
    kind: AuthEmailKind,
    to: string,
    url: string,
    token: string,
  ) => {
    try {
      await emailSender.send({
        kind,
        to,
        url: browserActionURL(kind, url, token),
      })
    } catch (error) {
      console.error("auth_email_delivery_failed", { kind })
      throw error
    }
  }

  return betterAuth({
    appName: "Acuity Portal",
    baseURL,
    secret: authSecret(),
    trustedOrigins: commaSeparated("BETTER_AUTH_TRUSTED_ORIGINS"),
    database: pool,
    socialProviders: googleProviderConfiguration(),
    databaseHooks: {
      user: {
        create: {
          before: createUserEligibilityGate({ portalAPIURL }),
        },
      },
    },
    rateLimit: {
      enabled:
        process.env.AUTH_EMAIL_MODE !== "test" ||
        process.env.AUTH_ALLOW_TEST_EMAIL !== "true",
    },
    emailAndPassword: {
      enabled: true,
      autoSignIn: false,
      requireEmailVerification: true,
      minPasswordLength: PASSWORD_MIN_LENGTH,
      maxPasswordLength: PASSWORD_MAX_LENGTH,
      revokeSessionsOnPasswordReset: true,
      sendResetPassword: async ({ user, url, token }) => {
        await deliver("password-reset", user.email, url, token)
      },
    },
    emailVerification: {
      sendOnSignUp: true,
      sendVerificationEmail: async ({ user, url, token }) => {
        await deliver("verification", user.email, url, token)
      },
    },
    hooks: {
      before: createAuthMiddleware(async (context) => {
        if (context.path !== "/sign-up/email") {
          return
        }
        const email =
          typeof context.body?.email === "string" ? context.body.email : ""
        const invitationToken =
          context.headers?.get("x-acuity-invitation-token") ?? undefined
        if (
          !(await isSignUpEligible({
            email,
            invitationToken,
            portalAPIURL,
          }))
        ) {
          throw new APIError("FORBIDDEN", {
            message: "A current Acuity invitation is required.",
          })
        }
      }),
    },
    plugins: [
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

function browserActionURL(
  kind: AuthEmailKind,
  sourceURL: string,
  token: string,
): string {
  const source = new URL(sourceURL)
  const target = new URL(
    kind === "verification" ? "/verify-email" : "/reset-password",
    source.origin,
  )
  const fragment = new URLSearchParams({ token })
  const callbackURL = source.searchParams.get("callbackURL")
  if (callbackURL) {
    fragment.set("callbackURL", callbackURL)
  }
  target.hash = fragment.toString()
  return target.toString()
}

function commaSeparated(name: string): string[] {
  return required(name)
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean)
}
