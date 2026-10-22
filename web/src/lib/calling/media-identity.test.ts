import assert from "node:assert/strict"
import { readdirSync, readFileSync } from "node:fs"
import { join, relative } from "node:path"
import test from "node:test"
import { fileURLToPath } from "node:url"
import ts from "typescript"

const sourceRoot = fileURLToPath(new URL("../../", import.meta.url))
const runtimeFile = "lib/calling/softphone-runtime.ts"
const equalityOperators = new Set([
  ts.SyntaxKind.EqualsEqualsToken,
  ts.SyntaxKind.EqualsEqualsEqualsToken,
  ts.SyntaxKind.ExclamationEqualsToken,
  ts.SyntaxKind.ExclamationEqualsEqualsToken,
])

test("only the softphone runtime compares media tokens, and nothing compares provider leg IDs", () => {
  const comparisons = sourceFiles(sourceRoot).flatMap((path) => {
    const file = relative(sourceRoot, path)
    return identityComparisons(file, readFileSync(path, "utf8")).filter(
      (comparison) =>
        file !== runtimeFile || !comparison.endsWith(" mediaToken"),
    )
  })

  assert.deepEqual(comparisons, [])
})

test("the identity comparison check reports hand-written leg and token comparisons", () => {
  assert.deepEqual(
    identityComparisons(
      "probe.ts",
      [
        "const same = left.providerLegID === right.providerLegID",
        "const other = (left!.mediaToken) !== token",
        "const valid = typeof left.mediaToken !== \"string\"",
      ].join("\n"),
    ),
    [
      "probe.ts:1 providerLegID",
      "probe.ts:1 providerLegID",
      "probe.ts:2 mediaToken",
    ],
  )
})

function sourceFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) {
      return entry.name === "generated" ? [] : sourceFiles(path)
    }
    return /\.tsx?$/.test(entry.name) ? [path] : []
  })
}

function identityComparisons(file: string, text: string) {
  const source = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true)
  const comparisons: string[] = []
  const visit = (node: ts.Node) => {
    if (
      ts.isBinaryExpression(node) &&
      equalityOperators.has(node.operatorToken.kind)
    ) {
      for (const operand of [node.left, node.right]) {
        const field = identityField(operand)
        if (!field) continue
        const line =
          source.getLineAndCharacterOfPosition(operand.getStart()).line + 1
        comparisons.push(`${file}:${line} ${field}`)
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return comparisons
}

function identityField(operand: ts.Expression) {
  let expression = operand
  while (
    ts.isParenthesizedExpression(expression) ||
    ts.isNonNullExpression(expression)
  ) {
    expression = expression.expression
  }
  if (!ts.isPropertyAccessExpression(expression)) return undefined
  const field = expression.name.text
  return field === "providerLegID" || field === "mediaToken" ? field : undefined
}
