const officialAPIBase = "https://api.telnyx.com/v2"
const testAPIBase = process.env.TELNYX_TEST_API_BASE_URL?.replace(/\/$/, "")

if (!testAPIBase) {
  throw new Error("TELNYX_TEST_API_BASE_URL is required by the isolated test transport")
}

const fetchOfficial = globalThis.fetch
globalThis.fetch = (input, init) => {
  const url = typeof input === "string" ? input : input.url
  if (!url.startsWith(officialAPIBase)) {
    throw new Error(`unexpected Telnyx test URL: ${url}`)
  }
  return fetchOfficial(`${testAPIBase}${url.slice(officialAPIBase.length)}`, init)
}
