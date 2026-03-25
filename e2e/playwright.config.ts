import { defineConfig } from "@playwright/test";

// Test timeout tiers. Use SLOW for tests that wait for background workers
// (e.g. rate-limited API calls in record mode).
export const TIMEOUT_FAST = 15_000;
export const TIMEOUT_REGULAR = 30_000;
export const TIMEOUT_SLOW = 180_000;

export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  retries: 0,
  workers: 1, // Serial: tests may share DB state within a run.

  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://envoy:8080",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },

  reporter: [
    ["list"],
    ["html", { outputFolder: "test-results/html", open: "never" }],
  ],

  outputDir: "test-results/artifacts",

  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
});
