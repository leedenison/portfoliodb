// ESLint for the client tree. It lives here, inside the npm project it lints,
// rather than at the repo root: eslint-config-next installs a parser that needs
// `next` itself to be resolvable, and the Next rules locate the app relative to
// the config, so both work without configuration only from within client/.
//
// extension/ carries its own config for the same reason. The two trees share no
// linted files -- the modules the extension imports through its path alias live
// here and are linted here -- so there is nothing for the split to drift on.
import { defineConfig, globalIgnores } from "eslint/config";
import next from "eslint-config-next/core-web-vitals";

export default defineConfig([
  globalIgnores([
    // Generated protobuf bindings and build output: not ours to fix.
    "gen/",
    ".next/",
  ]),
  // Bundles next/typescript, so typescript-eslint's recommended rules come with it.
  ...next,
  {
    files: ["**/*.{ts,tsx}"],
    rules: {
      // A leading underscore marks a binding that exists only to satisfy a
      // signature -- a converter that ignores its options argument, say.
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_", caughtErrorsIgnorePattern: "^_" },
      ],
      // A Pages Router rule: the client is App Router only, so there is no
      // pages directory for it to check against.
      "@next/next/no-html-link-for-pages": "off",
    },
  },
]);
