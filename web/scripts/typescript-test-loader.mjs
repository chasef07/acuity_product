import { readFile, stat } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath, pathToFileURL } from "node:url"

import { loadBindings, transform } from "next/dist/build/swc/index.js"

await loadBindings()

const sourceRoot = fileURLToPath(new URL("../src/", import.meta.url))
const sourceExtensions = [".ts", ".tsx"]

export async function resolve(specifier, context, nextResolve) {
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
    const resolved = await resolveSource(candidate)
    if (resolved) {
      return { url: pathToFileURL(resolved).href, shortCircuit: true }
    }
  }
  return nextResolve(specifier, context)
}

export async function load(url, context, nextLoad) {
  if (url.startsWith("file:") && /\.tsx?$/.test(url)) {
    const filename = fileURLToPath(url)
    const source = await readFile(filename, "utf8")
    const result = await transform(source, {
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

async function resolveSource(candidate) {
  for (const pathCandidate of [
    candidate,
    ...sourceExtensions.map((extension) => `${candidate}${extension}`),
    ...sourceExtensions.map((extension) => path.join(candidate, `index${extension}`)),
  ]) {
    if (await exists(pathCandidate)) return pathCandidate
  }
}

async function exists(candidate) {
  return stat(candidate).then(
    (value) => value.isFile(),
    () => false,
  )
}
