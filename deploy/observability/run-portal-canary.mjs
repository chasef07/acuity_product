const baseURL = required("PORTAL_API_URL")
const token = required("PORTAL_CANARY_BEARER_TOKEN")
const allowHTTP = process.env.PORTAL_CANARY_ALLOW_HTTP === "1"
const parsedBaseURL = new URL(baseURL)
if (parsedBaseURL.protocol !== "https:" && !(allowHTTP && parsedBaseURL.protocol === "http:")) {
  throw new Error("PORTAL_API_URL must use HTTPS")
}
if (parsedBaseURL.username || parsedBaseURL.password || parsedBaseURL.search || parsedBaseURL.hash) {
  throw new Error("PORTAL_API_URL must not contain credentials, query, or fragment")
}

const timeoutMilliseconds = positiveInteger(
  process.env.PORTAL_CANARY_TIMEOUT_MS ?? "5000",
  "PORTAL_CANARY_TIMEOUT_MS",
)
const stages = []

try {
  await readStage("access", "/v1/access", [200])
  await readStage("calling_state", "/v1/calling/state", [200])
  process.stdout.write(`${JSON.stringify({
    status: "ok",
    journey: "portal-critical-read",
    stages,
  })}\n`)
} catch (error) {
  const message = error instanceof CanaryError
    ? error.message
    : "portal canary network failure"
  process.stderr.write(`${message}\n`)
  process.exitCode = 1
}

async function readStage(name, path, acceptedStatuses) {
  const started = performance.now()
  let response
  try {
    response = await fetch(new URL(path, parsedBaseURL), {
      method: "GET",
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: "application/json",
        "X-Acuity-Canary": "portal-critical-read",
      },
      redirect: "error",
      signal: AbortSignal.timeout(timeoutMilliseconds),
    })
  } catch {
    throw new CanaryError(`portal canary ${name} network failure`)
  }
  if (!acceptedStatuses.includes(response.status)) {
    throw new CanaryError(`portal canary ${name} returned HTTP ${response.status}`)
  }
  if (!(response.headers.get("content-type") ?? "").toLowerCase().startsWith("application/json")) {
    throw new CanaryError(`portal canary ${name} returned an invalid content type`)
  }
  try {
    await response.json()
  } catch {
    throw new CanaryError(`portal canary ${name} returned invalid JSON`)
  }
  stages.push({
    name,
    status: response.status,
    durationMilliseconds: Math.round(performance.now() - started),
  })
}

function required(name) {
  const value = (process.env[name] ?? "").trim()
  if (!value) {
    throw new Error(`${name} is required`)
  }
  return value
}

function positiveInteger(raw, name) {
  const value = Number(raw)
  if (!Number.isSafeInteger(value) || value <= 0 || value > 60000) {
    throw new Error(`${name} must be an integer from 1 through 60000`)
  }
  return value
}

class CanaryError extends Error {}
