import fs from "node:fs"
import http from "node:http"
import https from "node:https"
import net from "node:net"

const certificate = process.env.RECORDING_FIXTURE_CERTIFICATE
const privateKey = process.env.RECORDING_FIXTURE_PRIVATE_KEY
const readyOutput = process.env.RECORDING_FIXTURE_READY_OUTPUT
const tlsPort = Number(process.env.RECORDING_FIXTURE_TLS_PORT ?? "19443")
const proxyPort = Number(process.env.RECORDING_FIXTURE_PROXY_PORT ?? "19444")

if (!certificate || !privateKey || !readyOutput) {
  throw new Error("recording fixture certificate, key, and ready output are required")
}

const audio = Buffer.from("RIFFsynthetic-voicemail-WAVE", "utf8")
const recordingServer = https.createServer(
  {
    cert: fs.readFileSync(certificate),
    key: fs.readFileSync(privateKey),
  },
  (request, response) => {
    if (request.url !== "/voicemail.wav") {
      response.writeHead(404).end()
      return
    }
    response.writeHead(200, {
      "Content-Type": "audio/wav",
      "Content-Length": audio.length,
    })
    response.end(audio)
  },
)

const proxyServer = http.createServer((_request, response) => {
  response.writeHead(405).end()
})
proxyServer.on("connect", (_request, client, head) => {
  const upstream = net.connect(tlsPort, "127.0.0.1", () => {
    client.write("HTTP/1.1 200 Connection Established\r\n\r\n")
    if (head.length > 0) upstream.write(head)
    upstream.pipe(client)
    client.pipe(upstream)
  })
  upstream.on("error", () => client.destroy())
  client.on("error", () => upstream.destroy())
})

await Promise.all([
  new Promise((resolve) =>
    recordingServer.listen(tlsPort, "127.0.0.1", resolve),
  ),
  new Promise((resolve) =>
    proxyServer.listen(proxyPort, "127.0.0.1", resolve),
  ),
])
fs.writeFileSync(readyOutput, "ready\n", { mode: 0o600 })

const close = () => {
  recordingServer.close()
  proxyServer.close()
}
process.on("SIGINT", close)
process.on("SIGTERM", close)
