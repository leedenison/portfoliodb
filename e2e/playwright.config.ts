import { defineConfig } from "@playwright/test";

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
