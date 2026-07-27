import { generateKeyPairSync, sign } from "node:crypto"
import { writeFileSync } from "node:fs"
import { createServer } from "node:http"

const ingressURL =
  process.env.TELNYX_FIXTURE_INGRESS_URL ??
  "http://127.0.0.1:18082/v1/provider/telnyx/webhooks"
const publicKeyOutput = process.env.TELNYX_FIXTURE_PUBLIC_KEY_OUTPUT
if (!publicKeyOutput) throw new Error("TELNYX_FIXTURE_PUBLIC_KEY_OUTPUT is required")

const { privateKey, publicKey } = generateKeyPairSync("ed25519")
const publicDER = publicKey.export({ format: "der", type: "spki" })
writeFileSync(publicKeyOutput, publicDER.subarray(publicDER.length - 32).toString("base64"), {
  mode: 0o600,
})

let credentialSequence = 0
const credentials = new Map()

createServer(async (request, response) => {
  if (request.url === "/health") {
    response.writeHead(204).end()
    return
  }
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  const requestBody = Buffer.concat(chunks)

  if (request.url === "/fixture/webhook") {
    if (request.headers.authorization !== "Bearer fixture-control") {
      response.writeHead(401).end()
      return
    }
    const event = JSON.parse(requestBody.toString("utf8"))
    const raw = Buffer.from(JSON.stringify({
      data: {
        record_type: "event",
        event_type: event.eventType,
        id: event.eventId,
        occurred_at: event.occurredAt,
        payload: event.payload,
      },
    }))
    const timestamp = String(Math.floor(Date.now() / 1000))
    const signature = sign(
      null,
      Buffer.concat([Buffer.from(`${timestamp}|`), raw]),
      privateKey,
    ).toString("base64")
    const delivered = await fetch(ingressURL, {
      method: "POST",
      headers: {
        "content-type": "application/json",
        "telnyx-signature-ed25519": signature,
        "telnyx-timestamp": timestamp,
      },
      body: raw,
    })
    response.writeHead(delivered.status, { "content-type": "application/json" })
    response.end(await delivered.text())
    return
  }

  if (request.headers.authorization !== "Bearer KEY_e2e") {
    response.writeHead(401).end()
    return
  }
  response.setHeader("content-type", "application/json")
  if (request.method === "POST" && request.url === "/v2/telephony_credentials") {
    const payload = JSON.parse(requestBody.toString("utf8"))
    credentialSequence += 1
    const credential = {
      id: `fixture-credential-${credentialSequence}`,
      name: payload.name,
      sip_username: `fixture-sip-${credentialSequence}`,
      expired: false,
    }
    credentials.set(credential.id, credential)
    response.writeHead(201).end(JSON.stringify({ data: credential }))
    return
  }
  if (
    request.method === "GET" &&
    request.url?.startsWith("/v2/telephony_credentials?")
  ) {
    const name = new URL(request.url, "http://fixture").searchParams.get("filter[name]")
    response.writeHead(200).end(JSON.stringify({
      data: [...credentials.values()].filter((credential) => credential.name === name),
    }))
    return
  }
  if (
    request.method === "POST" &&
    request.url?.startsWith("/v2/telephony_credentials/") &&
    request.url.endsWith("/token")
  ) {
    const header = Buffer.from('{"alg":"none","typ":"JWT"}').toString("base64url")
    const payload = Buffer.from(JSON.stringify({
      exp: Math.floor(Date.now() / 1000) + 3600,
    })).toString("base64url")
    response.writeHead(201).end(JSON.stringify(`${header}.${payload}.fixture`))
    return
  }
  if (request.method === "DELETE" && request.url?.startsWith("/v2/telephony_credentials/")) {
    credentials.delete(request.url.split("/").pop())
    response.writeHead(204).end()
    return
  }
  if (request.method === "POST" && request.url === "/v2/calls") {
    response.writeHead(200).end(JSON.stringify({
      data: {
        call_control_id: "fixture-staff-control",
        call_leg_id: "fixture-staff-leg",
      },
    }))
    return
  }
  if (request.method === "POST" && request.url?.includes("/actions/")) {
    response.writeHead(200).end('{"data":{"result":"ok"}}')
    return
  }
  response.writeHead(404).end()
}).listen(19000, "127.0.0.1")
