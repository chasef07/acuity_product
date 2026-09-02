import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"

const directory = dirname(fileURLToPath(import.meta.url))

test("metrics-only dry run excludes alert policies", () => {
  const result = spawnSync(
    process.execPath,
    [join(directory, "apply.mjs"), "--metrics-only"],
    {
      encoding: "utf8",
      env: { ...process.env, GCP_PROJECT: "test-project" },
    },
  )

  assert.equal(result.status, 0, result.stderr)
  assert.match(result.stdout, /'logging' 'metrics' 'create'/)
  assert.match(result.stdout, /'logging' 'metrics' 'update'/)
  assert.doesNotMatch(result.stdout, /'monitoring' 'policies'/)
})

for (const channel of [
  { enabled: false, verificationStatus: "VERIFIED", problem: "disabled" },
  { enabled: true, verificationStatus: "UNVERIFIED", problem: "unverified" },
]) {
  test(`apply rejects a ${channel.problem} notification channel before mutation`, () => {
    const temporaryDirectory = mkdtempSync(
      join(tmpdir(), "acuity-observability-test-"),
    )
    try {
      const capture = join(temporaryDirectory, "gcloud.txt")
      const gcloud = join(temporaryDirectory, "gcloud")
      writeFileSync(gcloud, `#!/bin/sh
set -eu
printf '%s\\n' "$*" >>"$GCLOUD_CAPTURE"
case "$*" in
  "beta monitoring channels describe "*)
    printf '%s\\n' '${JSON.stringify({
      name: "projects/test-project/notificationChannels/123",
      ...channel,
    })}'
    ;;
  "logging metrics list "*|"monitoring policies list "*)
    printf '%s\\n' '[]'
    ;;
esac
`)
      chmodSync(gcloud, 0o755)
      const result = spawnSync(
        process.execPath,
        [join(directory, "apply.mjs"), "--apply"],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            PATH: `${temporaryDirectory}:${process.env.PATH}`,
            GCLOUD_CAPTURE: capture,
            GCP_PROJECT: "test-project",
            MONITORING_NOTIFICATION_CHANNELS:
              "projects/test-project/notificationChannels/123",
          },
        },
      )

      assert.notEqual(result.status, 0, result.stdout)
      assert.match(result.stderr, new RegExp(channel.problem, "i"))
      const commands = readFileSync(capture, "utf8")
      assert.match(commands, /beta monitoring channels describe/)
      assert.doesNotMatch(commands, /logging metrics (create|update)/)
      assert.doesNotMatch(commands, /monitoring policies (create|update)/)
    } finally {
      rmSync(temporaryDirectory, { recursive: true, force: true })
    }
  })
}
