import { createHash } from "node:crypto"

import { getAuth } from "@/lib/auth"
import { isSignUpEligible } from "@/lib/auth-eligibility"
import { required } from "@/lib/server-env"

type TestCookie = {
  name: string
  value: string
  path: string
  httpOnly?: boolean
  secure?: boolean
  sameSite?: "Lax" | "Strict" | "None"
  expires?: number
}

type TestHelpers = {
  createUser(overrides: Record<string, unknown>): Record<string, unknown>
  saveUser(user: Record<string, unknown>): Promise<unknown>
  login(options: { userId: string }): Promise<{ cookies: TestCookie[] }>
}

export async function createTestSession(email: string, name: string) {
  if (process.env.AUTH_ALLOW_TEST_SESSION !== "true") {
    throw new Error("test sessions are disabled")
  }
  const normalizedEmail = email.trim().toLowerCase()
  if (!normalizedEmail || !name.trim()) {
    throw new Error("test session identity is required")
  }

  const userID = `e2e-${createHash("sha256").update(normalizedEmail).digest("hex").slice(0, 24)}`
  const context = (await getAuth().$context) as unknown as {
    test?: TestHelpers
  }
  if (!context.test) {
    throw new Error("Better Auth test helpers are unavailable")
  }

  try {
    return await context.test.login({ userId: userID })
  } catch {
    const eligible = await isSignUpEligible({
      email: normalizedEmail,
      portalAPIURL: required("PORTAL_API_INTERNAL_URL"),
    })
    if (!eligible) {
      throw new Error("test session identity is not provisioned")
    }
    const user = context.test.createUser({
      id: userID,
      email: normalizedEmail,
      name: name.trim(),
      emailVerified: true,
    })
    await context.test.saveUser(user)
    return context.test.login({ userId: userID })
  }
}
