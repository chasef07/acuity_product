import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

// Components and routes read feature hooks and commands from src/lib/clients/.
// Only those modules touch the access token, portalClient, or the generated SDK.
// Generated types stay importable. Each pattern also matches relative paths.
const transportMessage =
  "Components never handle transport. Use a hook or command from @/lib/clients/ (see src/lib/clients/portal-request.ts).";
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

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    files: ["src/components/**", "src/app/**"],
    rules: {
      "@typescript-eslint/no-restricted-imports": ["error", transportBoundary],
    },
  },
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    "src/lib/api/generated/**",
  ]),
]);

export default eslintConfig;
