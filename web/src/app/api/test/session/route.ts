import { NextResponse } from "next/server"

import { createTestSession } from "@/lib/test-session"

export async function POST(request: Request) {
  if (process.env.AUTH_ALLOW_TEST_SESSION !== "true") {
    return new NextResponse(null, { status: 404 })
  }
  const body = (await request.json().catch(() => undefined)) as
    | { email?: unknown; name?: unknown }
    | undefined
  if (typeof body?.email !== "string" || typeof body.name !== "string") {
    return NextResponse.json({ error: "invalid test identity" }, { status: 400 })
  }

  try {
    const session = await createTestSession(body.email, body.name)
    const response = NextResponse.json({ ok: true })
    for (const cookie of session.cookies) {
      response.cookies.set({
        name: cookie.name,
        value: cookie.value,
        path: cookie.path,
        httpOnly: cookie.httpOnly,
        secure: cookie.secure,
        sameSite: cookie.sameSite?.toLowerCase() as
          | "lax"
          | "strict"
          | "none"
          | undefined,
        expires: cookie.expires
          ? new Date(cookie.expires * 1_000)
          : undefined,
      })
    }
    return response
  } catch {
    return NextResponse.json({ error: "test session denied" }, { status: 403 })
  }
}
