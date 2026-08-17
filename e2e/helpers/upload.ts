// Shared helper for uploading an archive fixture through the upload modal and
// waiting for all background workers to finish processing.

import path from "path";
import { expect, type Page, type Browser } from "@playwright/test";
import { waitForWorkersIdle } from "./workers";

// A document built in the test rather than read from disk. Dates a cassette
// replays against have to be relative to the run, so a suite whose fetch window
// would otherwise grow past the provider's chunk size supplies its postings
// here instead of committing them with the dates they were recorded on.
export type ArchiveUpload = { name: string; document: unknown };

// Upload an archive via the upload modal, either a fixture by name or a
// document built in the test. The page must already be authenticated. After the
// modal auto-closes on SUCCESS the function waits for all background workers to
// reach idle.
export async function uploadArchiveAndWait(
  page: Page,
  browser: Browser,
  archive: string | ArchiveUpload,
  opts?: { expectedPostingCount?: number }
): Promise<void> {
  await page.goto("/uploads");
  await expect(
    page.locator("[data-testid='page-uploads']")
  ).toBeVisible();

  await page.locator("[data-testid='btn-upload-transactions']").click();
  await expect(
    page.locator("[data-testid='upload-modal']")
  ).toBeVisible();

  // Step 1: broker is pre-selected (Fidelity). Click Next.
  await page.getByRole("button", { name: "Next" }).click();

  // Step 2: set the archive file.
  const fileInput = page.locator("#upload-file");
  await fileInput.setInputFiles(
    typeof archive === "string"
      ? path.resolve(__dirname, "../fixtures", archive)
      : {
          name: archive.name,
          mimeType: "application/json",
          buffer: Buffer.from(JSON.stringify(archive.document)),
        }
  );

  // Wait for parse preview.
  await expect(
    page.locator("[data-testid='upload-parse-preview']")
  ).toBeVisible();

  if (opts?.expectedPostingCount != null) {
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toContainText(`${opts.expectedPostingCount} posting(s)`);
  }

  // Submit.
  await page.locator("[data-testid='btn-upload-submit']").click();

  // Modal auto-closes on SUCCESS.
  await expect(
    page.locator("[data-testid='upload-modal']")
  ).not.toBeVisible({ timeout: 30_000 });

  // Wait for all background workers to finish.
  await waitForWorkersIdle(browser);
}
