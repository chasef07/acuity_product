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

// Local fixture launcher. This page, like the session endpoint, is absent unless
// the explicitly test-only authentication switch is enabled.
export async function GET() {
  if (process.env.AUTH_ALLOW_TEST_SESSION !== "true") return new NextResponse(null, { status: 404 })
  return new NextResponse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Local Acuity Tasks</title><body style="font:16px system-ui;max-width:640px;margin:64px auto;padding:24px"><h1>Local Acuity Tasks</h1><p>Synthetic data, real portal. Select a staff view.</p><button data-email="selected@abita.test">Optical staff</button> <button data-email="secondary@abita.test">Clinical staff</button> <button data-email="admin@abita.test">Practice admin</button><p id="status" role="status"></p><script>document.querySelectorAll('button').forEach(button=>button.onclick=async()=>{const status=document.getElementById('status');status.textContent='Signing in…';try{const response=await fetch('/api/test/session',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({email:button.dataset.email,name:'Synthetic local staff'})});if(!response.ok)throw new Error();location.href='/workspace'}catch{status.textContent='Local sign-in failed. Check the running fixture.'}})</script></body></html>`, { headers: { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" } })
}
