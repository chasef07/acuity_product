import { NextResponse } from "next/server"

import { getAuth } from "@/lib/auth"

export async function POST(request: Request) {
  if (Number(request.headers.get("content-length") ?? 0) > 4096) {
    return new NextResponse(null, { status: 400 })
  }
  const body = (await request.json().catch(() => undefined)) as
    | { token?: unknown }
    | undefined
  if (
    typeof body?.token !== "string" ||
    body.token.length < 32 ||
    body.token.length > 4096
  ) {
    return new NextResponse(null, { status: 400 })
  }
  try {
    const result = await getAuth().api.verifyEmail({
      query: { token: body.token },
    })
    if (!result?.status) {
      return new NextResponse(null, { status: 400 })
    }
    return NextResponse.json({ verified: true })
  } catch {
    return new NextResponse(null, { status: 400 })
  }
}
