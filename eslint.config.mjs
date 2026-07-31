// One flat config for both TypeScript trees. client/ and extension/ are separate
// npm projects, but the extension imports the client's React-free modules through
// a path alias, so linting them under one config keeps the rules that apply to
// shared source identical.
//
// The React and Next rules come from the underlying plugins rather than the
// eslint-config-next umbrella: that config also installs a parser which requires
// `next` itself to be resolvable, which it is not from the repo root.
import { defineConfig, globalIgnores } from "eslint/config";
import next from "@next/eslint-plugin-next";
import react from "eslint-plugin-react";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";

export default defineConfig([
  globalIgnores([
    "**/node_modules/",
    // Generated protobuf bindings and build output: not ours to fix.
    "client/gen/",
    "client/.next/",
    "extension/dist/",
  ]),
  {
    // The conservative starting set: typescript-eslint's non-type-aware
    // recommended rules. Type-aware linting needs both projects wired up to
    // their tsconfigs and is a separate step.
    files: ["client/**/*.{ts,tsx}", "extension/**/*.ts"],
    extends: [tseslint.configs.recommended],
    rules: {
      // A leading underscore marks a binding that exists only to satisfy a
      // signature -- a converter that ignores its options argument, say.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" },
      ],
    },
  },
  {
    // React, hooks and Next rules apply to the client only. The extension has no
    // React and no pages, so these would be inert there at best.
    files: ["client/**/*.{ts,tsx}"],
    extends: [
      react.configs.flat.recommended,
      react.configs.flat["jsx-runtime"],
      reactHooks.configs.flat["recommended-latest"],
      next.configs["core-web-vitals"],
    ],
    settings: {
      react: { version: "detect" },
      // The Next rules look for the app relative to the config, which sits at
      // the repo root rather than inside the project.
      next: { rootDir: "client/" },
    },
    rules: {
      // A Pages Router rule: the client is App Router only, so there is no
      // pages directory for it to check against.
      "@next/next/no-html-link-for-pages": "off",
    },
  },
  {
    files: ["extension/**/*.ts"],
    languageOptions: {
      globals: {
        chrome: "readonly",
      },
    },
  },
]);
