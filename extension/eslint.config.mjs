// ESLint for the extension tree. Deliberately separate from client/'s config:
// the extension has no React and no pages, so the React and Next rule sets that
// dominate the client config would be inert here.
//
// The baseline below is duplicated from client/eslint.config.mjs rather than
// shared through a repo-root package. A root npm project is what this replaced:
// its lockfile made Next infer the repo root as the workspace root, and the few
// lines of duplication are cheaper than that coupling.
import { defineConfig, globalIgnores } from "eslint/config";
import tseslint from "typescript-eslint";

export default defineConfig([
  globalIgnores(["dist/"]),
  {
    files: ["**/*.ts"],
    extends: [tseslint.configs.recommended],
    languageOptions: {
      globals: {
        chrome: "readonly",
      },
    },
    rules: {
      // A leading underscore marks a binding that exists only to satisfy a
      // signature -- a converter that ignores its options argument, say.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" },
      ],
    },
  },
]);
