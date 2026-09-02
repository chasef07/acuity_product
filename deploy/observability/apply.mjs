import {
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { spawnSync } from "node:child_process"
import {
  preparePolicy,
  validateDesiredPolicies,
} from "./policy-identity.mjs"

const apply = process.argv.includes("--apply")
const metricsOnly = process.argv.includes("--metrics-only")
const project = process.env.GCP_PROJECT
if (!project) {
  throw new Error("GCP_PROJECT is required")
}
const notificationChannels = (process.env.MONITORING_NOTIFICATION_CHANNELS ?? "")
  .split(",")
  .map((channel) => channel.trim())
  .filter(Boolean)
if (apply && !metricsOnly && notificationChannels.length === 0) {
  throw new Error("MONITORING_NOTIFICATION_CHANNELS is required with --apply")
}

const directory = dirname(fileURLToPath(import.meta.url))
const metrics = JSON.parse(
  readFileSync(join(directory, "log-metrics.json"), "utf8"),
)
const policies = JSON.parse(
  readFileSync(join(directory, "alert-policies.json"), "utf8"),
)
if (!Array.isArray(metrics) || !Array.isArray(policies)) {
  throw new Error("observability definitions must be arrays")
}

for (const metric of metrics) {
  if (!/^[a-z][a-z0-9_]{0,99}$/.test(metric.name)) {
    throw new Error(`invalid log metric name ${metric.name}`)
  }
}
validateDesiredPolicies(policies)

const shellQuote = (value) => `'${String(value).replaceAll("'", "'\\''")}'`
const run = (args, capture = false) => {
  if (!apply) {
    console.log(["gcloud", ...args].map(shellQuote).join(" "))
    return ""
  }
  const result = spawnSync("gcloud", args, {
    encoding: "utf8",
    stdio: capture ? ["ignore", "pipe", "inherit"] : "inherit",
  })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    throw new Error(`gcloud ${args.slice(0, 3).join(" ")} failed`)
  }
  return capture ? result.stdout.trim() : ""
}

if (apply && !metricsOnly) {
  for (const channelName of new Set(notificationChannels)) {
    const channelJSON = run(
      [
        "beta",
        "monitoring",
        "channels",
        "describe",
        channelName,
        "--format=json",
        "--project",
        project,
      ],
      true,
    )
    const channel = JSON.parse(channelJSON)
    if (channel.name !== channelName) {
      throw new Error(
        `notification channel ${channelName} did not resolve exactly`,
      )
    }
    if (channel.enabled !== true) {
      throw new Error(`notification channel ${channelName} is disabled`)
    }
    if (channel.verificationStatus === "UNVERIFIED") {
      throw new Error(`notification channel ${channelName} is unverified`)
    }
  }
}

const existingMetricsJSON = run(
  [
    "logging",
    "metrics",
    "list",
    "--format=json",
    "--project",
    project,
  ],
  apply,
)
const existingMetrics = apply ? JSON.parse(existingMetricsJSON) : []
if (!Array.isArray(existingMetrics)) {
  throw new Error("Google Cloud returned an invalid log metric list")
}
const existingMetricNames = new Set(existingMetrics.map((metric) =>
  String(metric.name ?? "").split("/").at(-1),
))

const existingPoliciesJSON = metricsOnly ? "[]" : run(
  [
    "monitoring",
    "policies",
    "list",
    "--format=json",
    "--project",
    project,
  ],
  apply,
)
const existingPolicies = apply && !metricsOnly
  ? JSON.parse(existingPoliciesJSON)
  : []
if (!Array.isArray(existingPolicies)) {
  throw new Error("Google Cloud returned an invalid alert policy list")
}

const temporaryDirectory = mkdtempSync(join(tmpdir(), "acuity-observability-"))
try {
  for (const metric of metrics) {
    const path = join(temporaryDirectory, `${metric.name}.json`)
    writeFileSync(path, `${JSON.stringify(metric, null, 2)}\n`)
    const metricAction = existingMetricNames.has(metric.name)
      ? "update"
      : "create"
    run([
      "logging",
      "metrics",
      metricAction,
      metric.name,
      "--config-from-file",
      path,
      "--project",
      project,
      "--quiet",
    ])
    if (!apply) {
      run([
        "logging",
        "metrics",
        "update",
        metric.name,
        "--config-from-file",
        path,
        "--project",
        project,
        "--quiet",
      ])
    }
  }

  for (const policy of metricsOnly ? [] : policies) {
    const policyKey = policy.userLabels.acuity_policy
    const { existing, rendered: renderedPolicy } = preparePolicy(
      policy,
      existingPolicies,
    )
    const path = join(temporaryDirectory, `${policyKey}-alert-policy.json`)
    writeFileSync(path, `${JSON.stringify(renderedPolicy, null, 2)}\n`)
    if (!apply) {
      run([
        "monitoring",
        "policies",
        "create",
        "--policy-from-file",
        path,
        `--notification-channels=${notificationChannels.join(",") || "<required-on-apply>"}`,
        "--project",
        project,
      ])
      run([
        "monitoring",
        "policies",
        "update",
        "<existing-policy-name>",
        "--policy-from-file",
        path,
        `--set-notification-channels=${notificationChannels.join(",") || "<required-on-apply>"}`,
        "--project",
        project,
      ])
      continue
    }

    if (!existing) {
      run([
        "monitoring",
        "policies",
        "create",
        "--policy-from-file",
        path,
        `--notification-channels=${notificationChannels.join(",")}`,
        "--project",
        project,
        "--quiet",
      ])
      continue
    }
    run([
      "monitoring",
      "policies",
      "update",
      existing.name,
      "--policy-from-file",
      path,
      `--set-notification-channels=${notificationChannels.join(",")}`,
      "--project",
      project,
      "--quiet",
    ])
  }
} finally {
  rmSync(temporaryDirectory, { recursive: true, force: true })
}
