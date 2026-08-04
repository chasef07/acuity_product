import { generateKeyPairSync, sign } from "node:crypto"
import { writeFileSync } from "node:fs"
import { createServer } from "node:http"

const callingIngressURL =
  process.env.TELNYX_FIXTURE_INGRESS_URL ??
  "http://127.0.0.1:18082/v1/provider/telnyx/webhooks"
const messagingIngressURL =
  process.env.TELNYX_FIXTURE_MESSAGING_INGRESS_URL ??
  "http://127.0.0.1:18082/v1/provider/telnyx/messaging-webhooks"
const publicKeyOutput = process.env.TELNYX_FIXTURE_PUBLIC_KEY_OUTPUT
if (!publicKeyOutput) throw new Error("TELNYX_FIXTURE_PUBLIC_KEY_OUTPUT is required")

const { privateKey, publicKey } = generateKeyPairSync("ed25519")
const publicDER = publicKey.export({ format: "der", type: "spki" })
writeFileSync(publicKeyOutput, publicDER.subarray(publicDER.length - 32).toString("base64"), {
  mode: 0o600,
})

let credentialSequence = 0
let callSequence = 0
let messageSequence = 0
const credentials = new Map()
const messages = new Map()

async function deliverWebhook(url, event) {
  const raw = Buffer.from(JSON.stringify({
    data: {
      record_type: "event",
      event_type: event.eventType,
      id: event.eventId,
      occurred_at: event.occurredAt,
      payload: event.payload,
    },
    meta: { fixture: true },
  }))
  const timestamp = String(Math.floor(Date.now() / 1000))
  const signature = sign(
    null,
    Buffer.concat([Buffer.from(`${timestamp}|`), raw]),
    privateKey,
  ).toString("base64")
  return fetch(url, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "telnyx-signature-ed25519": signature,
      "telnyx-timestamp": timestamp,
    },
    body: raw,
  })
}

createServer(async (request, response) => {
  if (request.url === "/health") {
    response.writeHead(204).end()
    return
  }
  const chunks = []
  for await (const chunk of request) chunks.push(chunk)
  const requestBody = Buffer.concat(chunks)

  const recordingAudioMatch = request.url?.match(
    /^\/fixture\/recordings\/([^/]+)\.mp3$/,
  )
  if (request.method === "GET" && recordingAudioMatch) {
    if (request.headers.authorization) {
      response.writeHead(400).end()
      return
    }
    const audio = Buffer.from("synthetic-mp3-audio")
    const range = request.headers.range?.match(/^bytes=(\d+)-(\d*)$/)
    if (range) {
      const start = Number(range[1])
      const requestedEnd = range[2] === "" ? audio.length - 1 : Number(range[2])
      const end = Math.min(requestedEnd, audio.length - 1)
      if (start > end || start >= audio.length) {
        response.writeHead(416, { "content-range": `bytes */${audio.length}` }).end()
        return
      }
      const partial = audio.subarray(start, end + 1)
      response.writeHead(206, {
        "accept-ranges": "bytes",
        "content-length": String(partial.length),
        "content-range": `bytes ${start}-${end}/${audio.length}`,
        "content-type": "audio/mpeg",
      }).end(partial)
      return
    }
    response.writeHead(200, {
      "accept-ranges": "bytes",
      "content-length": String(audio.length),
      "content-type": "audio/mpeg",
    }).end(audio)
    return
  }

  if (request.url === "/fixture/webhook") {
    if (request.headers.authorization !== "Bearer fixture-control") {
      response.writeHead(401).end()
      return
    }
    const event = JSON.parse(requestBody.toString("utf8"))
    const delivered = await deliverWebhook(callingIngressURL, event)
    response.writeHead(delivered.status, { "content-type": "application/json" })
    response.end(await delivered.text())
    return
  }
  if (
    request.method === "GET" &&
    request.url === "/fixture/messages"
  ) {
    if (request.headers.authorization !== "Bearer fixture-control") {
      response.writeHead(401).end()
      return
    }
    response.writeHead(200, { "content-type": "application/json" })
    response.end(JSON.stringify({ data: [...messages.values()] }))
    return
  }
  const statusMatch = request.url?.match(
    /^\/fixture\/messages\/([^/]+)\/status$/,
  )
  if (request.method === "POST" && statusMatch) {
    if (request.headers.authorization !== "Bearer fixture-control") {
      response.writeHead(401).end()
      return
    }
    const message = messages.get(decodeURIComponent(statusMatch[1]))
    if (!message) {
      response.writeHead(404).end()
      return
    }
    const status = JSON.parse(requestBody.toString("utf8")).status
    const callbackToken = new URL(message.webhook_url).pathname.split("/").pop()
    const delivered = await deliverWebhook(
      `${messagingIngressURL}/${encodeURIComponent(callbackToken)}`,
      {
      eventType: "message.finalized",
      eventId: `fixture-status-${message.id}-${status}`,
      occurredAt: new Date().toISOString(),
      payload: {
        id: message.id,
        from: { phone_number: message.from },
        to: [{ phone_number: message.to, status }],
        delivery_status: status,
      },
    })
    response.writeHead(delivered.status, { "content-type": "application/json" })
    response.end(await delivered.text())
    return
  }
  if (request.method === "POST" && request.url === "/fixture/message-inbound") {
    if (request.headers.authorization !== "Bearer fixture-control") {
      response.writeHead(401).end()
      return
    }
    const payload = JSON.parse(requestBody.toString("utf8"))
    const eventID = payload.eventId ?? `fixture-inbound-${Date.now()}`
    const delivered = await deliverWebhook(messagingIngressURL, {
      eventType: "message.received",
      eventId: eventID,
      occurredAt: payload.occurredAt ?? new Date().toISOString(),
      payload: {
        id: payload.providerMessageId ?? eventID,
        from: { phone_number: payload.from },
        to: [{ phone_number: payload.to, status: "delivered" }],
        text: payload.text ?? "",
        media: payload.media ?? [],
      },
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
  const recordingMetadataMatch = request.url?.match(/^\/v2\/recordings\/([^/]+)$/)
  if (request.method === "GET" && recordingMetadataMatch) {
    const recordingID = decodeURIComponent(recordingMetadataMatch[1])
    response.writeHead(200).end(JSON.stringify({
      data: {
        download_urls: {
          mp3: `http://127.0.0.1:19000/fixture/recordings/${encodeURIComponent(recordingID)}.mp3`,
        },
      },
    }))
    return
  }
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
    const payload = JSON.parse(requestBody.toString("utf8"))
    if (
      !payload.media_prep &&
      payload.answering_machine_detection !== "disabled"
    ) {
      response.writeHead(200).end(JSON.stringify({
        data: {
          call_control_id: "fixture-staff-control",
          call_leg_id: "fixture-staff-leg",
        },
      }))
      return
    }
    callSequence += 1
    const leg = payload.media_prep ? "outbound-staff" : "outbound-destination"
    response.writeHead(200).end(JSON.stringify({
      data: {
        call_control_id: `fixture-${leg}-control-${callSequence}`,
        call_leg_id: `fixture-${leg}-leg-${callSequence}`,
      },
    }))
    return
  }
  if (request.method === "POST" && request.url === "/v2/messages") {
    const payload = JSON.parse(requestBody.toString("utf8"))
    messageSequence += 1
    const message = {
      id: `fixture-message-${messageSequence}`,
      ...payload,
    }
    messages.set(message.id, message)
    response.writeHead(200).end(JSON.stringify({ data: { id: message.id } }))
    return
  }
  if (request.method === "POST" && request.url?.includes("/actions/")) {
    response.writeHead(200).end('{"data":{"result":"ok"}}')
    return
  }
  response.writeHead(404).end()
}).listen(19000, "127.0.0.1")
