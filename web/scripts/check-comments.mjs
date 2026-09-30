import { readdir, readFile } from "node:fs/promises"
import { fileURLToPath } from "node:url"
import { ESLint } from "eslint"
import postcss from "postcss"
import { commentPolicy } from "../eslint.config.mjs"

const web = fileURLToPath(new URL("..", import.meta.url))
const repository = fileURLToPath(new URL("../..", import.meta.url))
const failures = []

const entries = await readdir(new URL("../src/", import.meta.url), { recursive: true, withFileTypes: true })
for (const entry of entries) {
  if (!entry.isFile() || !entry.name.endsWith(".css")) continue
  const path = `${entry.parentPath}/${entry.name}`
  postcss.parse(await readFile(path, "utf8"), { from: path }).walkComments((comment) => {
    failures.push(`${path.slice(web.length)}:${comment.source.start.line} CSS comment`)
  })
}

const eslint = new ESLint({
  cwd: repository,
  overrideConfigFile: true,
  overrideConfig: {
    plugins: { acuity: commentPolicy },
    rules: { "acuity/no-comments": "error" },
  },
})
for (const result of await eslint.lintFiles(["deploy", "scripts"])) {
  for (const message of result.messages) {
    failures.push(`${result.filePath.slice(repository.length)}:${message.line} ${message.message}`)
  }
}

if (failures.length > 0) {
  console.error(failures.join("\n"))
  process.exit(1)
}
