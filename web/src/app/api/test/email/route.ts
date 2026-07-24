import { NextResponse } from "next/server"

import {
  latestTestEmail,
  type AuthEmailKind,
} from "@/lib/email"

export function GET(request: Request) {
  if (
    process.env.AUTH_EMAIL_MODE !== "test" ||
    process.env.AUTH_ALLOW_TEST_EMAIL !== "true"
  ) {
    return new NextResponse(null, { status: 404 })
  }
  const url = new URL(request.url)
  const email = url.searchParams.get("email") ?? ""
  const kind = url.searchParams.get("kind") as AuthEmailKind
  if (kind !== "verification" && kind !== "password-reset") {
    return NextResponse.json({ error: "invalid email kind" }, { status: 400 })
  }
  const message = latestTestEmail(email, kind)
  if (!message) {
    return new NextResponse(null, { status: 404 })
  }
  return NextResponse.json({ url: message.url })
}
