import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const clientDir = fileURLToPath(new URL("../client/", import.meta.url));
const entry = fileURLToPath(new URL("./src/content/portfoliodb.ts", import.meta.url));

/**
 * Content scripts are built separately from the popup and service worker.
 *
 * chrome.scripting.executeScript injects files as classic scripts, so a content
 * script must be a single self-contained IIFE with no import statements. Rollup
 * cannot emit IIFE for a multi-entry build, so each content script needs its own
 * invocation of this config; emptyOutDir is off so it does not wipe the main
 * build's output.
 */
export default defineConfig({
  resolve: {
    alias: { "@": clientDir },
  },
  build: {
    outDir: "dist",
    emptyOutDir: false,
    lib: {
      entry,
      formats: ["iife"],
      name: "portfoliodbBootstrap",
      fileName: () => "content/portfoliodb.js",
    },
  },
});
