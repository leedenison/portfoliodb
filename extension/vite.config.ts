import { fileURLToPath } from "node:url";
// vitest/config re-exports Vite's defineConfig widened to accept the test block,
// so build and test configuration stay in one file with one alias declaration.
import { defineConfig } from "vitest/config";

const here = fileURLToPath(new URL("./", import.meta.url));
const clientDir = fileURLToPath(new URL("../client/", import.meta.url));

export default defineConfig({
  // Extension pages load from chrome-extension://<id>/, so emit relative asset
  // references rather than root-absolute ones.
  base: "./",
  resolve: {
    // Mirrors the "@/*" path mapping in tsconfig.json; both must be updated together.
    alias: { "@": clientDir },
    // Client sources resolve these from client/node_modules and extension sources
    // from extension/node_modules. Without dedupe both copies land in the bundle,
    // and two protobuf runtimes means registries that do not recognise each
    // other's descriptors. See the version pinning note in package.json.
    dedupe: ["@bufbuild/protobuf", "papaparse"],
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    // Chrome cannot load an extension whose entry points have hashed names, since
    // manifest.json refers to them by fixed path.
    rollupOptions: {
      input: {
        popup: `${here}popup.html`,
        background: `${here}src/background/index.ts`,
      },
      output: {
        entryFileNames: "[name].js",
        chunkFileNames: "chunks/[name]-[hash].js",
        assetFileNames: "assets/[name]-[hash][extname]",
      },
    },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
});
