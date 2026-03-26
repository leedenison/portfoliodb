// Shared helper for uploading a CSV fixture through the upload modal and
// waiting for all background workers to finish processing.

import path from "path";
import { expect, type Page, type Browser } from "@playwright/test";
import { waitForWorkersIdle } from "./workers";

// Upload a CSV fixture via the upload modal. The page must already be
// authenticated. After the modal auto-closes on SUCCESS the function waits
// for all background workers to reach idle.
export async function uploadCSVAndWait(
  page: Page,
  browser: Browser,
  fixtureName: string,
  opts?: { expectedTxCount?: number }
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

  // Step 2: set the CSV file.
  const fileInput = page.locator("#upload-file");
  await fileInput.setInputFiles(
    path.resolve(__dirname, "../fixtures", fixtureName)
  );

  // Wait for parse preview.
  await expect(
    page.locator("[data-testid='upload-parse-preview']")
  ).toBeVisible();

  if (opts?.expectedTxCount != null) {
    await expect(
      page.locator("[data-testid='upload-parse-preview']")
    ).toContainText(`${opts.expectedTxCount} transaction(s)`);
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
