import { readFileSync } from "node:fs"
import { createHash } from "node:crypto"
import { isDeepStrictEqual } from "node:util"

const [contractPath, evidencePath] = process.argv.slice(2)
if (!contractPath || !evidencePath) {
  throw new Error("usage: check-telnyx-callleg-cutover.mjs CONTRACT EVIDENCE|--print-provider-provenance")
}

const readJSON = (path) => JSON.parse(readFileSync(path, "utf8"))
const contract = readJSON(contractPath)
const provenanceOnly = evidencePath === "--print-provider-provenance"
const evidence = provenanceOnly ? null : readJSON(evidencePath)

const fail = (message) => {
  throw new Error(message)
}
const forbiddenEvidenceKey = /(api.?key|password|private.?key|signing.?key|phone.?number|sip.?user.?name)/i
const e164Material = /(^|[^0-9])\+\d{7,15}([^0-9]|$)/
const inspectEvidence = (value, path = "evidence") => {
  if (typeof value === "string") {
    if (e164Material.test(value)) fail(`${path} contains forbidden phone material`)
    return
  }
  if (!value || typeof value !== "object") return
  for (const [key, child] of Object.entries(value)) {
    if (forbiddenEvidenceKey.test(key)) {
      fail(`${path}.${key} contains forbidden secret or phone material`)
    }
    inspectEvidence(child, `${path}.${key}`)
  }
}
if (evidence) inspectEvidence(evidence)

if (contract.schemaVersion !== 2 || contract.provider !== "telnyx") {
  throw new Error("unsupported Telnyx CallLeg cutover contract")
}
if (!provenanceOnly && evidence.schemaVersion !== contract.schemaVersion) {
  throw new Error("cutover evidence schemaVersion does not match the contract")
}

const equal = (actual, expected, label) => {
  if (!isDeepStrictEqual(actual, expected)) {
    fail(`${label} does not match the required value`)
  }
}
const requireString = (value, label) => {
  if (typeof value !== "string" || value.trim() === "") {
    fail(`${label} must be a non-empty string`)
  }
  return value.trim()
}
const requireNumber = (value, label) => {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    fail(`${label} must be a finite number`)
  }
  return value
}
const requireRecentTime = (value, label) => {
  const timestamp = Date.parse(requireString(value, label))
  const age = Date.now() - timestamp
  if (!Number.isFinite(timestamp) || age < -5 * 60_000 || age > 24 * 60 * 60_000) {
    fail(`${label} must be within the cutover's last 24 hours`)
  }
}
const requireHash = (value, label) => {
  if (!/^[0-9a-f]{64}$/.test(requireString(value, label))) {
    fail(`${label} must be a lowercase SHA-256 digest`)
  }
}

const sha256 = (value) => createHash("sha256").update(value).digest("hex")
const canonicalJSON = (value) => {
  if (Array.isArray(value)) return `[${value.map(canonicalJSON).join(",")}]`
  if (value && typeof value === "object") {
    return `{${Object.keys(value).sort().map((key) =>
      `${JSON.stringify(key)}:${canonicalJSON(value[key])}`).join(",")}}`
  }
  return JSON.stringify(value)
}
const forbiddenProviderKey = /(api.?key|credential|password|private.?key|signing.?key|phone.?number|sip.?username|user.?name|external.?pin|customer.?reference|translated.?number|call.?forward)/i
const sanitizeProviderValue = (value) => {
  if (Array.isArray(value)) return value.map(sanitizeProviderValue)
  if (!value || typeof value !== "object") {
    if (typeof value === "string" && /^\+\d{7,15}$/.test(value)) return "[redacted-phone]"
    return value
  }
  return Object.fromEntries(Object.entries(value)
    .filter(([key]) => !forbiddenProviderKey.test(key))
    .map(([key, child]) => [key, sanitizeProviderValue(child)]))
}
const requireEnvironment = (name) => requireString(process.env[name], name)
const officialAPIBase = "https://api.telnyx.com/v2"
const apiBase = officialAPIBase
const apiKey = requireEnvironment("TELNYX_API_KEY")
const callControlApplicationID = requireEnvironment("TELNYX_CALL_CONTROL_ID")
const webRTCConnectionID = requireEnvironment("TELNYX_CREDENTIAL_CONNECTION_ID")
const productDID = requireEnvironment("TELNYX_FROM_NUMBER")
if (!/^\+\d{7,15}$/.test(productDID)) fail("TELNYX_FROM_NUMBER must use E.164")
const fetchData = async (path, label) => {
  const response = await fetch(`${apiBase}${path}`, {
    headers: { Authorization: `Bearer ${apiKey}`, Accept: "application/json" },
    signal: AbortSignal.timeout(10_000),
  })
  if (!response.ok) fail(`${label} read failed with HTTP ${response.status}`)
  const body = await response.json()
  if (!body || !("data" in body)) fail(`${label} read omitted data`)
  return body.data
}
const fetchResource = async (path, label) => {
  const data = await fetchData(path, label)
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    fail(`${label} read omitted a resource object`)
  }
  return data
}
const callControlApplication = await fetchResource(
  `/call_control_applications/${encodeURIComponent(callControlApplicationID)}`,
  "Call Control Application",
)
const webRTCConnection = await fetchResource(
  `/credential_connections/${encodeURIComponent(webRTCConnectionID)}`,
  "WebRTC Connection",
)
equal(callControlApplication.id, callControlApplicationID, "Call Control Application id")
equal(webRTCConnection.id, webRTCConnectionID, "WebRTC Connection id")
equal(callControlApplication.active, true, "Call Control Application active")
equal(webRTCConnection.active, true, "WebRTC Connection active")
const outboundVoiceProfileID = requireString(
  callControlApplication.outbound?.outbound_voice_profile_id,
  "Call Control Application outbound voice profile id",
)
equal(
  webRTCConnection.outbound?.outbound_voice_profile_id,
  outboundVoiceProfileID,
  "connection outbound voice profile id",
)
const outboundVoiceProfile = await fetchResource(
  `/outbound_voice_profiles/${encodeURIComponent(outboundVoiceProfileID)}`,
  "outbound voice profile",
)
equal(outboundVoiceProfile.id, outboundVoiceProfileID, "outbound voice profile id")
const phoneNumberResults = await fetchData(
  `/phone_numbers?filter%5Bphone_number%5D=${encodeURIComponent(productDID)}`,
  "Product DID",
)
if (!Array.isArray(phoneNumberResults)) fail("Product DID read omitted a resource list")
const matchingPhoneNumbers = phoneNumberResults.filter((item) => item?.phone_number === productDID)
if (matchingPhoneNumbers.length !== 1) fail("Product DID read must return exactly one exact E.164 match")
const phoneNumber = matchingPhoneNumbers[0]
const phoneNumberVoice = await fetchResource(
  `/phone_numbers/${encodeURIComponent(requireString(phoneNumber.id, "Product DID id"))}/voice`,
  "Product DID voice settings",
)
equal(phoneNumberVoice.id, phoneNumber.id, "Product DID voice settings id")
equal(phoneNumber.connection_id, callControlApplicationID, "Product DID connection id")
equal(phoneNumberVoice.connection_id, callControlApplicationID, "Product DID voice connection id")
equal(phoneNumber.status, contract.voice.inboundDIDStatus, "Product DID status")
const didForwardingState = phoneNumberVoice.call_forwarding?.call_forwarding_enabled === false
  ? "disabled"
  : "enabled"
equal(didForwardingState, contract.voice.inboundDIDCallForwarding, "Product DID call forwarding")
const didRecordingState = phoneNumber.call_recording_enabled === false &&
  phoneNumberVoice.call_recording?.inbound_call_recording_enabled === false
  ? "disabled"
  : "enabled"
equal(didRecordingState, contract.voice.inboundDIDRecording, "Product DID inbound recording")

const providerSnapshot = sanitizeProviderValue({
  callControlApplication,
  didSummary: phoneNumber,
  didVoice: phoneNumberVoice,
  outboundVoiceProfile,
  webRTCConnection,
})
const observedProvenance = {
  source: "telnyx-v2-read-only",
  snapshotSha256: sha256(canonicalJSON(providerSnapshot)),
  resources: [
    { type: "call_control_application", idHash: sha256(callControlApplicationID) },
    { type: "webrtc_connection", idHash: sha256(webRTCConnectionID) },
    { type: "outbound_voice_profile", idHash: sha256(outboundVoiceProfileID) },
    { type: "product_did", idHash: sha256(phoneNumber.id) },
  ],
}
if (provenanceOnly) {
  process.stdout.write(`${JSON.stringify(observedProvenance, null, 2)}\n`)
  process.exit(0)
}
equal(
  evidence.provenance?.snapshotSha256,
  observedProvenance.snapshotSha256,
  "provenance.snapshotSha256",
)
const resourceHashes = new Map((evidence.provenance?.resources || [])
  .map((resource) => [resource.type, resource.idHash]))
for (const resource of observedProvenance.resources) {
  equal(resourceHashes.get(resource.type), resource.idHash, `provenance resource ${resource.type}`)
}

requireRecentTime(evidence.capturedAt, "capturedAt")
equal(evidence.provider, contract.provider, "provider")
equal(evidence.provenance?.source, "telnyx-v2-read-only", "provenance.source")
requireHash(evidence.provenance?.snapshotSha256, "provenance.snapshotSha256")
if (!Array.isArray(evidence.provenance?.resources) || evidence.provenance.resources.length < 4) {
  fail("provenance.resources must identify the sanitized live resources")
}
for (const [index, resource] of evidence.provenance.resources.entries()) {
  requireString(resource.type, `provenance.resources[${index}].type`)
  requireHash(resource.idHash, `provenance.resources[${index}].idHash`)
}

const expectedGates = contract.cutoverGates
const actualGates = evidence.cutoverGates
if (!actualGates || typeof actualGates !== "object" || Array.isArray(actualGates)) {
  fail("cutover evidence must contain cutoverGates")
}
equal(Object.keys(actualGates).sort(), Object.keys(expectedGates).sort(), "cutover gate names")
for (const [key, expected] of Object.entries(expectedGates)) {
  equal(actualGates[key], expected, `cutover gate ${key}`)
}

const live = evidence.liveConfiguration
if (!live || typeof live !== "object" || Array.isArray(live)) {
  fail("cutover evidence must contain liveConfiguration")
}
const app = live.callControlApplication
const webrtc = live.webRTCConnection
const outbound = live.outboundVoiceProfile
for (const [label, resource] of Object.entries({ app, webrtc, outbound })) {
  if (!resource || typeof resource !== "object" || Array.isArray(resource)) {
    fail(`liveConfiguration.${label} is required`)
  }
}

const disabledState = (value) => {
  if (value === undefined || value === null || value === false || value === "" ||
      value === "disabled" || value === "none") return "disabled"
  if (Array.isArray(value) && value.length === 0) return "disabled"
  if (typeof value === "object" && Object.keys(value).length === 0) return "disabled"
  return "enabled"
}
const recordingState = disabledState(outboundVoiceProfile.call_recording)
const providerLiveConfiguration = {
  callControlApplication: {
    active: callControlApplication.active,
    webhookEventUrl: callControlApplication.webhook_event_url,
    webhookFailoverUrl: callControlApplication.webhook_event_failover_url,
    webhookApiVersion: String(callControlApplication.webhook_api_version),
    webhookTimeoutSeconds: callControlApplication.webhook_timeout_secs,
    firstCommandTimeout: callControlApplication.first_command_timeout,
    outboundChannelLimit: callControlApplication.outbound?.channel_limit,
    automaticRecording: recordingState,
    siprec: disabledState(callControlApplication.inbound?.siprec),
    rtcpCapture: disabledState(callControlApplication.rtcp_settings?.capture_enabled),
    mediaFork: disabledState(callControlApplication.media_fork),
    connectedCallRecording: recordingState,
  },
  webRTCConnection: {
    active: webRTCConnection.active,
    webhookEventUrl: webRTCConnection.webhook_event_url,
    webhookFailoverUrl: webRTCConnection.webhook_event_failover_url,
    webhookApiVersion: String(webRTCConnection.webhook_api_version),
    webhookTimeoutSeconds: webRTCConnection.webhook_timeout_secs,
    sipUriCallingPreference: webRTCConnection.sip_uri_calling_preference,
    simultaneousRinging: typeof webRTCConnection.inbound?.simultaneous_ringing === "boolean"
      ? (webRTCConnection.inbound.simultaneous_ringing ? "enabled" : "disabled")
      : webRTCConnection.inbound?.simultaneous_ringing,
    srtpRequired: webRTCConnection.encrypted_media === "SRTP",
    outboundChannelLimit: webRTCConnection.outbound?.channel_limit,
    automaticRecording: recordingState,
    siprec: disabledState(webRTCConnection.inbound?.siprec),
    rtcpCapture: disabledState(webRTCConnection.rtcp_settings?.capture_enabled),
    mediaFork: disabledState(webRTCConnection.media_fork),
    connectedCallRecording: recordingState,
  },
  outboundVoiceProfile: {
    enabled: outboundVoiceProfile.enabled,
    outboundChannelLimit: outboundVoiceProfile.concurrent_call_limit,
    destinations: outboundVoiceProfile.whitelisted_destinations,
    trafficType: outboundVoiceProfile.traffic_type,
    dailySpendLimitUSD: outboundVoiceProfile.daily_spend_limit_enabled
      ? Number(outboundVoiceProfile.daily_spend_limit)
      : null,
    maximumDestinationRateUSD: Number(outboundVoiceProfile.max_destination_rate),
    automaticRecording: recordingState,
    siprec: "disabled",
    rtcpCapture: "disabled",
    mediaFork: "disabled",
    connectedCallRecording: recordingState,
  },
}
for (const [label, expected] of Object.entries(providerLiveConfiguration)) {
  equal(live[label], expected, `provider-backed liveConfiguration.${label}`)
}

const primary = new URL(requireString(app.webhookEventUrl, "callControlApplication.webhookEventUrl"))
const webrtcPrimary = new URL(requireString(webrtc.webhookEventUrl, "webRTCConnection.webhookEventUrl"))
const failover = new URL(requireString(app.webhookFailoverUrl, "callControlApplication.webhookFailoverUrl"))
const webrtcFailover = new URL(requireString(webrtc.webhookFailoverUrl, "webRTCConnection.webhookFailoverUrl"))
for (const [label, url] of Object.entries({ primary, webrtcPrimary, failover, webrtcFailover })) {
  if (url.protocol !== "https:") fail(`${label} must use HTTPS`)
  if (url.pathname !== contract.webhooks.primaryPath) {
    fail(`${label} must use ${contract.webhooks.primaryPath}`)
  }
}
equal(webrtcPrimary.href, primary.href, "primary provider-ingress URL")
equal(webrtcFailover.href, failover.href, "failover provider-ingress URL")
if (primary.origin === failover.origin) {
  fail("failover ingress must be operationally independent from primary ingress")
}
for (const [label, resource] of Object.entries({ app, webrtc })) {
  equal(resource.webhookApiVersion, contract.webhooks.apiVersion, `${label}.webhookApiVersion`)
  equal(resource.webhookTimeoutSeconds, contract.webhooks.timeoutSeconds, `${label}.webhookTimeoutSeconds`)
}
equal(app.firstCommandTimeout, contract.voice.firstCommandTimeout, "firstCommandTimeout")
equal(webrtc.sipUriCallingPreference, contract.voice.sipUriCallingPreference, "sipUriCallingPreference")
equal(webrtc.simultaneousRinging, contract.voice.simultaneousRinging, "simultaneousRinging")
equal(webrtc.srtpRequired, contract.voice.srtpRequired, "srtpRequired")

for (const [label, resource] of Object.entries({ app, webrtc, outbound })) {
  equal(resource.automaticRecording, contract.voice.automaticRecording, `${label}.automaticRecording`)
  equal(resource.siprec, contract.voice.siprec, `${label}.siprec`)
  equal(resource.rtcpCapture, contract.voice.rtcpCapture, `${label}.rtcpCapture`)
  equal(resource.mediaFork, contract.voice.mediaFork, `${label}.mediaFork`)
  equal(resource.connectedCallRecording, contract.voice.connectedCallRecording, `${label}.connectedCallRecording`)
}

const declaredLimit = requireNumber(live.declaredChannelLimit, "declaredChannelLimit")
if (declaredLimit < contract.voice.minimumDeclaredChannelLimit) {
  fail(`declaredChannelLimit must be at least ${contract.voice.minimumDeclaredChannelLimit}`)
}
equal(app.outboundChannelLimit, declaredLimit, "callControlApplication.outboundChannelLimit")
equal(webrtc.outboundChannelLimit, declaredLimit, "webRTCConnection.outboundChannelLimit")
equal(outbound.outboundChannelLimit, declaredLimit, "outboundVoiceProfile.outboundChannelLimit")
if (requireNumber(live.accountCapacity?.concurrentCalls, "accountCapacity.concurrentCalls") < declaredLimit) {
  fail("account concurrency is below the declared channel limit")
}
if (requireNumber(live.accountCapacity?.callsPerSecond, "accountCapacity.callsPerSecond") <= 0) {
  fail("account CPS must be positive")
}
requireString(live.accountCapacity?.approvalReference, "accountCapacity.approvalReference")

equal(outbound.enabled, true, "outboundVoiceProfile.enabled")
equal(outbound.destinations, contract.voice.outboundDestinations, "outboundVoiceProfile.destinations")
equal(outbound.trafficType, contract.voice.outboundTrafficType, "outboundVoiceProfile.trafficType")
equal(outbound.dailySpendLimitUSD, contract.voice.dailySpendLimitUSD, "outboundVoiceProfile.dailySpendLimitUSD")
equal(outbound.maximumDestinationRateUSD, contract.voice.maximumDestinationRateUSD, "outboundVoiceProfile.maximumDestinationRateUSD")

const acknowledgement = live.acknowledgement
if (requireNumber(acknowledgement?.sampleCount, "acknowledgement.sampleCount") < 10) {
  fail("acknowledgement proof requires at least ten signed requests")
}
if (requireNumber(acknowledgement?.p99Millis, "acknowledgement.p99Millis") >=
  contract.webhooks.maximumAcknowledgementP99Millis) {
  fail("provider acknowledgement p99 exceeds the safe objective")
}
requireString(acknowledgement?.evidenceRef, "acknowledgement.evidenceRef")

const credentials = live.staffCredentialRingability
const eligibleCount = requireNumber(credentials?.eligibleCount, "staffCredentialRingability.eligibleCount")
equal(credentials?.provenCount, eligibleCount, "staffCredentialRingability.provenCount")
if (eligibleCount <= 0 || !Array.isArray(credentials.proofs) || credentials.proofs.length !== eligibleCount) {
  fail("every eligible Staff credential needs one ringability proof")
}
const staffHashes = new Set()
for (const [index, proof] of credentials.proofs.entries()) {
  requireHash(proof.staffSubjectHash, `staffCredentialRingability.proofs[${index}].staffSubjectHash`)
  requireHash(proof.credentialIdHash, `staffCredentialRingability.proofs[${index}].credentialIdHash`)
  equal(proof.status, "ringable", `staffCredentialRingability.proofs[${index}].status`)
  requireRecentTime(proof.observedAt, `staffCredentialRingability.proofs[${index}].observedAt`)
  if (staffHashes.has(proof.staffSubjectHash)) fail("duplicate Staff ringability proof")
  staffHashes.add(proof.staffSubjectHash)
}

if (!Array.isArray(evidence.probes)) fail("probes must be an array")
const probes = new Map(evidence.probes.map((probe) => [probe.name, probe]))
for (const name of contract.requiredProbes) {
  const probe = probes.get(name)
  if (!probe) fail(`missing required live probe ${name}`)
  equal(probe.status, "passed", `probe ${name}.status`)
  requireRecentTime(probe.observedAt, `probe ${name}.observedAt`)
  requireString(probe.evidenceRef, `probe ${name}.evidenceRef`)
}

process.stdout.write("Telnyx CallLeg cutover gates satisfied\n")
