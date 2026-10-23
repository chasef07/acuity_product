import { readFileSync, statSync } from "node:fs"
import { registerHooks } from "node:module"
import path from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

import { loadBindings, transformSync } from "next/dist/build/swc/index.js"

await loadBindings()

const sourceRoot = fileURLToPath(new URL("../src/", import.meta.url))
const sourceExtensions = [".ts", ".tsx"]

registerHooks({ resolve, load })

function resolve(specifier, context, nextResolve) {
  if (specifier === "next/navigation") {
    return {
      url: new URL("./test-stubs/next-navigation.mjs", import.meta.url).href,
      shortCircuit: true,
    }
  }
  const candidate = specifier.startsWith("@/")
    ? path.join(sourceRoot, specifier.slice(2))
    : specifier.startsWith(".") && context.parentURL?.startsWith("file:")
      ? fileURLToPath(new URL(specifier, context.parentURL))
      : undefined
  if (candidate) {
    const resolved = resolveSource(candidate)
    if (resolved) {
      return { url: pathToFileURL(resolved).href, shortCircuit: true }
    }
  }
  return nextResolve(specifier, context)
}

function load(url, context, nextLoad) {
  if (url.startsWith("file:") && url.endsWith(".module.css")) {
    return { format: "module", source: "export default {}", shortCircuit: true }
  }
  if (url.startsWith("file:") && /\.tsx?$/.test(url)) {
    const filename = fileURLToPath(url)
    const source = readFileSync(filename, "utf8")
    const result = transformSync(source, {
      filename,
      jsc: {
        parser: { syntax: "typescript", tsx: filename.endsWith(".tsx") },
        transform: { react: { runtime: "automatic" } },
      },
      module: { type: "es6" },
      sourceMaps: "inline",
    })
    return { format: "module", source: result.code, shortCircuit: true }
  }
  return nextLoad(url, context)
}

function resolveSource(candidate) {
  for (const pathCandidate of [
    candidate,
    ...sourceExtensions.map((extension) => `${candidate}${extension}`),
    ...sourceExtensions.map((extension) => path.join(candidate, `index${extension}`)),
  ]) {
    if (statSync(pathCandidate, { throwIfNoEntry: false })?.isFile()) return pathCandidate
  }
}
