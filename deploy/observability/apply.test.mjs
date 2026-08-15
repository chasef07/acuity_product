import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
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
