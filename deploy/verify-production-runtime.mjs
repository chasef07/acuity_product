#!/usr/bin/env node

import { spawnSync } from "node:child_process"
import { readFileSync } from "node:fs"

const [contractPath, project, region] = process.argv.slice(2)
if (!contractPath || !project || !region) {
  throw new Error(
    "usage: verify-production-runtime.mjs CONTRACT PROJECT REGION",
  )
}

const contract = JSON.parse(readFileSync(contractPath, "utf8"))
if (!Array.isArray(contract.runtimes)) {
  throw new Error("production runtime contract must define runtimes")
}

const describe = (kind, name) => {
  const args = [
    "run",
    kind,
    "describe",
    name,
    "--project",
    project,
    "--region",
    region,
    "--format",
    "json",
  ]
  const result = spawnSync("gcloud", args, {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "inherit"],
  })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    throw new Error(`gcloud run ${kind} describe ${name} failed`)
  }
  return JSON.parse(result.stdout)
}

const integer = (value) => {
  if (Number.isInteger(value)) {
    return value
  }
  if (typeof value === "string" && /^\d+$/.test(value)) {
    return Number(value)
  }
  return undefined
}

const assertValue = (runtimeName, field, expected, actualValue) => {
  const actual = integer(actualValue)
  if (actual !== expected) {
    throw new Error(
      `${runtimeName} runtime contract drift: ${field} expected ${expected}, got ${actual ?? "missing"}`,
    )
  }
}

const templateSpec = (runtime) =>
  runtime.spec?.template?.spec ?? runtime.spec?.template
const poolValue = (runtime, environmentName) => {
  const containers = templateSpec(runtime)?.containers
  const environment = Array.isArray(containers) ? containers[0]?.env : undefined
  if (!Array.isArray(environment)) {
    return undefined
  }
  return environment.find((entry) => entry.name === environmentName)?.value
}

for (const runtime of contract.runtimes) {
  const runtimeName = `acuity-${runtime.name}`
  if (runtime.kind === "service") {
    const live = describe("services", runtimeName)
    if (live.metadata?.name !== runtimeName) {
      throw new Error(`${runtimeName} did not resolve exactly`)
    }
    const annotations = live.metadata?.annotations ?? {}
    const serviceScaling = live.spec?.scaling ?? live.scaling ?? {}
    assertValue(
      runtimeName,
      "concurrency",
      runtime.concurrency,
      templateSpec(live)?.containerConcurrency ??
        live.spec?.template?.maxInstanceRequestConcurrency,
    )
    assertValue(
      runtimeName,
      "minimumInstances",
      runtime.minimumInstances,
      serviceScaling.minInstanceCount ??
        annotations["run.googleapis.com/minScale"],
    )
    assertValue(
      runtimeName,
      "maximumInstances",
      runtime.maximumInstances,
      serviceScaling.maxInstanceCount ??
        annotations["run.googleapis.com/maxScale"],
    )
    assertValue(
      runtimeName,
      "poolMaximum",
      runtime.poolMaximum,
      poolValue(
        live,
        runtime.name === "web" ? "AUTH_DB_POOL_MAX" : "DATABASE_POOL_MAX",
      ),
    )
    continue
  }
  if (runtime.kind !== "worker-pool") {
    throw new Error(`${runtimeName} has unsupported kind ${runtime.kind}`)
  }
  const live = describe("worker-pools", runtimeName)
  if (live.metadata?.name !== runtimeName) {
    throw new Error(`${runtimeName} did not resolve exactly`)
  }
  const instances =
    live.scaling?.manualInstanceCount ??
    live.spec?.scaling?.manualInstanceCount ??
    live.spec?.template?.scaling?.manualInstanceCount ??
    live.metadata?.annotations?.["run.googleapis.com/manualInstanceCount"]
  assertValue(
    runtimeName,
    "minimumInstances",
    runtime.minimumInstances,
    instances,
  )
  assertValue(
    runtimeName,
    "maximumInstances",
    runtime.maximumInstances,
    instances,
  )
  assertValue(
    runtimeName,
    "concurrency",
    runtime.concurrency,
    templateSpec(live)?.containerConcurrency,
  )
  assertValue(
    runtimeName,
    "poolMaximum",
    runtime.poolMaximum,
    poolValue(live, "DATABASE_POOL_MAX"),
  )
}
