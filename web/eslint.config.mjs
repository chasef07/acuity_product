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
    files: ["src/components/**", "src/app/**"],
    rules: {
      "@typescript-eslint/no-restricted-imports": ["error", transportBoundary],
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
