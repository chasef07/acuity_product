import { readFileSync } from "node:fs";
import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const transportMessage =
  "Components never handle transport. Use a hook or command from @/lib/clients/ (see web/README.md).";
const transportBoundary = {
  patterns: [
    {
      regex: String.raw`^(@/|(\.\./)+)lib/api/client(\.ts)?$`,
      message: transportMessage,
    },
    {
      regex: String.raw`^(@/|(\.\./)+)lib/api/generated(/(?!types\.gen(\.ts)?$).*)?$`,
      allowTypeImports: true,
      message: transportMessage,
    },
    {
      regex: String.raw`^(@/|(\.\./)+)lib/auth-client(\.ts)?$`,
      importNames: ["getAccessToken", "getAccessTokenResult"],
      message: transportMessage,
    },
  ],
};

const dataBoundary = {
  patterns: [
    {
      group: ["@tanstack/*"],
      message:
        "Only src/lib/data may import TanStack Query. Read through a hook from @/lib/clients/ or @/lib/data/ (see web/README.md).",
    },
  ],
};

const directiveComment =
  /^\s*(?:eslint-disable|eslint-enable|@ts-expect-error|@ts-ignore|@ts-nocheck|\/\s*<reference\s)/;

export const commentPolicy = {
  rules: {
    "no-comments": {
      meta: {
        type: "suggestion",
        schema: [],
        messages: {
          comment:
            "Acuity Portal code carries no comments. Only tool directives (eslint-disable, @ts-expect-error, /// <reference>) are allowed.",
        },
      },
      create(context) {
        return {
          Program() {
            for (const comment of context.sourceCode.getAllComments()) {
              if (comment.type === "Shebang" || directiveComment.test(comment.value)) {
                continue;
              }
              context.report({ loc: comment.loc, messageId: "comment" });
            }
          },
        };
      },
    },
  },
};

const generatedEnums = [
  ...readFileSync(new URL("src/lib/api/generated/types.gen.ts", import.meta.url), "utf8")
    .matchAll(/'[^']*'(?:\s*\|\s*'[^']*')+/g),
].map(([union]) => new Set(union.split("|").map((member) => member.trim().slice(1, -1))));
if (generatedEnums.length === 0) {
  throw new Error("vocabulary/no-redeclared-enum found no enums in src/lib/api/generated/types.gen.ts");
}

function stringLiterals(nodes) {
  const values = nodes.map((node) => {
    const literal = node.type === "TSLiteralType" ? node.literal : node;
    return literal?.type === "Literal" && typeof literal.value === "string" ? literal.value : undefined;
  });
  return values.length >= 2 && values.every((value) => value !== undefined) ? new Set(values) : undefined;
}

function duplicatedEnum(values, minimumSubset) {
  return generatedEnums.find((generated) =>
    [...values].every((value) => generated.has(value)) &&
    (values.size === generated.size || values.size >= minimumSubset),
  );
}

const vocabularyPolicy = {
  rules: {
    "no-redeclared-enum": {
      meta: {
        type: "problem",
        schema: [],
        messages: {
          redeclared:
            "These values re-declare a generated API enum ({{values}}). Use the generated type from @/lib/api/generated/types.gen and its labels in @/lib/vocabulary.",
        },
      },
      create(context) {
        function check(node, members, minimumSubset) {
          const values = stringLiterals(members);
          const generated = values && duplicatedEnum(values, minimumSubset);
          if (generated) {
            context.report({ node, messageId: "redeclared", data: { values: [...generated].join(" | ") } });
          }
        }
        return {
          TSUnionType: (node) => check(node, node.types, 3),
          ArrayExpression: (node) => check(node, node.elements, Infinity),
        };
      },
    },
  },
};

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    plugins: { acuity: commentPolicy },
    rules: {
      "acuity/no-comments": "error",
    },
  },
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/**/*.test.{ts,tsx}", "src/lib/vocabulary.ts"],
    plugins: { vocabulary: vocabularyPolicy },
    rules: {
      "vocabulary/no-redeclared-enum": "error",
    },
  },
  {
    files: ["src/components/**", "src/app/**"],
    rules: {
      "@typescript-eslint/no-restricted-imports": ["error", transportBoundary],
    },
  },
  {
    ignores: ["src/lib/data/**"],
    rules: {
      "no-restricted-imports": ["error", dataBoundary],
    },
  },
  globalIgnores([
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    "src/lib/api/generated/**",
  ]),
]);

export default eslintConfig;
