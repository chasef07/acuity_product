import { readFileSync } from "node:fs"

const contractPath = process.argv[2]
if (!contractPath) {
  throw new Error("production runtime contract path is required")
}

const contract = JSON.parse(readFileSync(contractPath, "utf8"))
const integer = (value, field, minimum = 0) => {
  if (!Number.isSafeInteger(value) || value < minimum) {
    throw new Error(`${field} must be an integer >= ${minimum}`)
  }
  return value
}

const runtimeNames = new Set()
let serviceConnections = 0
let workerPoolConnectionsPerRevision = 0
const rows = contract.runtimes.map((runtime, index) => {
  const prefix = `runtimes[${index}]`
  if (!/^[a-z][a-z0-9-]*$/.test(runtime.name) || runtimeNames.has(runtime.name)) {
    throw new Error(`${prefix}.name must be unique and shell-safe`)
  }
  runtimeNames.add(runtime.name)
  if (runtime.kind !== "service" && runtime.kind !== "worker-pool") {
    throw new Error(`${prefix}.kind is unsupported`)
  }
  const concurrency = integer(runtime.concurrency, `${prefix}.concurrency`)
  const minimum = integer(runtime.minimumInstances, `${prefix}.minimumInstances`)
  const maximum = integer(runtime.maximumInstances, `${prefix}.maximumInstances`, 1)
  const pool = integer(runtime.poolMaximum, `${prefix}.poolMaximum`, 1)
  const dedicated = integer(
    runtime.dedicatedConnections,
    `${prefix}.dedicatedConnections`,
  )
  const timeout = integer(
    runtime.acquisitionTimeoutMilliseconds,
    `${prefix}.acquisitionTimeoutMilliseconds`,
    1,
  )
  if (minimum > maximum) {
    throw new Error(`${prefix} minimumInstances exceeds maximumInstances`)
  }
  if (runtime.kind === "service" && concurrency < 1) {
    throw new Error(`${prefix}.concurrency must be positive for a service`)
  }
  if (runtime.kind === "worker-pool" && minimum !== maximum) {
    throw new Error(`${prefix} worker-pool instances must be fixed`)
  }
  const runtimeConnections = maximum * (pool + dedicated)
  if (runtime.kind === "service") {
    serviceConnections += runtimeConnections
  } else {
    workerPoolConnectionsPerRevision += runtimeConnections
  }
  return [
    runtime.name,
    runtime.kind,
    concurrency,
    minimum,
    maximum,
    pool,
    dedicated,
    timeout,
    0,
  ].join("\t")
})

for (const required of [
  "web",
  "portal-api",
  "provider-ingress",
  "realtime",
  "worker",
]) {
  if (!runtimeNames.has(required)) {
    throw new Error(`runtime ${required} is required`)
  }
}

const workerPoolRevisionOverlap = integer(
  contract.workerPoolRevisionOverlap,
  "workerPoolRevisionOverlap",
  1,
)
const operatorHeadroom = integer(
  contract.operatorHeadroom,
  "operatorHeadroom",
)
const migrationTasks = integer(contract.migration.tasks, "migration.tasks", 1)
const migrationPool = integer(
  contract.migration.poolMaximum,
  "migration.poolMaximum",
  1,
)
const migrationTimeout = integer(
  contract.migration.acquisitionTimeoutMilliseconds,
  "migration.acquisitionTimeoutMilliseconds",
  1,
)
const migrationRetries = integer(
  contract.migration.maximumRetries,
  "migration.maximumRetries",
)
const calculatedConnections =
  serviceConnections +
  workerPoolConnectionsPerRevision * workerPoolRevisionOverlap +
  migrationTasks * migrationPool +
  operatorHeadroom
const requiredConnections = integer(
  contract.requiredDatabaseConnections,
  "requiredDatabaseConnections",
  1,
)
if (calculatedConnections !== requiredConnections) {
  throw new Error(
    `requiredDatabaseConnections=${requiredConnections}, calculated=${calculatedConnections}`,
  )
}

console.log(`capacity\tmeta\t0\t0\t${requiredConnections}\t0\t0\t0\t0`)
for (const row of rows) {
  console.log(row)
}
console.log(
  [
    "migrate",
    "job",
    0,
    0,
    migrationTasks,
    migrationPool,
    0,
    migrationTimeout,
    migrationRetries,
  ].join("\t"),
)
