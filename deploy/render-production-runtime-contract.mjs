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
const booleanInteger = (value, field) => {
  if (typeof value !== "boolean") {
    throw new Error(`${field} must be a boolean`)
  }
  return value ? 1 : 0
}
const region = contract.region
if (typeof region !== "string" || !/^[a-z]+-[a-z]+\d+$/.test(region)) {
  throw new Error("region must be a Google Cloud region")
}

const runtimeNames = new Set()
let serviceConnections = 0
let oneExtraServiceInstanceConnections = 0
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
  const billingMode = runtime.billingMode
  const expectedBillingMode =
    runtime.kind === "service" ? "request-based" : "instance-based"
  if (billingMode !== expectedBillingMode) {
    throw new Error(`${prefix}.billingMode must be ${expectedBillingMode}`)
  }
  const vcpus = integer(runtime.vCPUs, `${prefix}.vCPUs`, 1)
  const memoryMiB = integer(runtime.memoryMiB, `${prefix}.memoryMiB`, 512)
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
    oneExtraServiceInstanceConnections += pool + dedicated
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
    vcpus,
    memoryMiB,
    billingMode,
    region,
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
const autoscalerOvershootHeadroom = integer(
  contract.autoscalerOvershootHeadroom,
  "autoscalerOvershootHeadroom",
)
if (autoscalerOvershootHeadroom !== oneExtraServiceInstanceConnections) {
  throw new Error(
    `autoscalerOvershootHeadroom=${autoscalerOvershootHeadroom}, one-extra-instance demand=${oneExtraServiceInstanceConnections}`,
  )
}
const migrationTasks = integer(contract.migration.tasks, "migration.tasks", 1)
if (contract.migration.billingMode !== "instance-based") {
  throw new Error("migration.billingMode must be instance-based")
}
const migrationVCPUs = integer(contract.migration.vCPUs, "migration.vCPUs", 1)
const migrationMemoryMiB = integer(
  contract.migration.memoryMiB,
  "migration.memoryMiB",
  512,
)
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
  autoscalerOvershootHeadroom +
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

const database = contract.database
if (!database || typeof database !== "object") {
  throw new Error("database is required")
}
const databaseVCPUs = integer(database.vCPUs, "database.vCPUs", 1)
const databaseMemoryMiB = integer(database.memoryMiB, "database.memoryMiB", 1)
const databaseStorageGiB = integer(database.storageGiB, "database.storageGiB", 1)
const retainedTransactionLogDays = integer(
  database.retainedTransactionLogDays,
  "database.retainedTransactionLogDays",
  1,
)
const retainedBackups = integer(
  database.retainedBackups,
  "database.retainedBackups",
  1,
)
for (const [field, value, expected] of [
  ["version", database.version, "POSTGRES_16"],
  ["edition", database.edition, "ENTERPRISE"],
  ["availabilityType", database.availabilityType, "ZONAL"],
  ["storageType", database.storageType, "SSD"],
]) {
  if (value !== expected) {
    throw new Error(`database.${field} must be ${expected}`)
  }
}
if (
  typeof database.backupStartTimeUTC !== "string" ||
  !/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(database.backupStartTimeUTC)
) {
  throw new Error("database.backupStartTimeUTC must be HH:MM")
}
if (database.backupLocation !== region) {
  throw new Error("database.backupLocation must match region")
}
const automatedBackups = booleanInteger(
  database.automatedBackups,
  "database.automatedBackups",
)
const pointInTimeRecovery = booleanInteger(
  database.pointInTimeRecovery,
  "database.pointInTimeRecovery",
)
const deletionProtection = booleanInteger(
  database.deletionProtection,
  "database.deletionProtection",
)
const dataCache = booleanInteger(database.dataCache, "database.dataCache")
const storageAutoIncrease = booleanInteger(
  database.storageAutoIncrease,
  "database.storageAutoIncrease",
)
if (
  automatedBackups !== 1 ||
  pointInTimeRecovery !== 1 ||
  deletionProtection !== 1 ||
  dataCache !== 0 ||
  storageAutoIncrease !== 1
) {
  throw new Error(
    "database must enable backups, PITR, deletion protection, and storage auto-increase without data cache",
  )
}

console.log(
  [
    "capacity",
    "meta",
    0,
    0,
    requiredConnections,
    0,
    0,
    0,
    0,
    0,
    0,
    "meta",
    region,
  ].join("\t"),
)
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
    migrationVCPUs,
    migrationMemoryMiB,
    contract.migration.billingMode,
    region,
  ].join("\t"),
)
console.log(
  [
    "database",
    "database",
    0,
    0,
    1,
    0,
    0,
    0,
    0,
    databaseVCPUs,
    databaseMemoryMiB,
    "instance-based",
    region,
    database.version,
    database.edition,
    database.availabilityType,
    databaseStorageGiB,
    database.storageType,
    database.backupStartTimeUTC,
    retainedTransactionLogDays,
    retainedBackups,
    automatedBackups,
    pointInTimeRecovery,
    deletionProtection,
    dataCache,
    storageAutoIncrease,
    database.backupLocation,
  ].join("\t"),
)
