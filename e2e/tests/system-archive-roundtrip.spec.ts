// E2E test: the consolidated archive page.
//
// Exports a system archive through the UI, then imports the same file back and
// reads the per-part results. The round trip is the point: a file this instance
// produced has to be one it can consume, and the per-part results are what tell
// an admin what an import actually applied.

import { test, expect } from "@playwright/test";
import path from "path";
import { readFile, writeFile } from "fs/promises";
import { tmpdir } from "os";
import { TIMEOUT_SLOW } from "../helpers/timeouts";
import { seedSession, injectSession, closeRedis } from "../helpers/auth";
import { resetAndSeedBase, closeDB } from "../helpers/db";
import { importPricesAndWait } from "../helpers/api";
import { IdentifierType } from "../gen/type/v1/type_pb";

test.beforeAll(async () => {
  await resetAndSeedBase();
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("system archive page", () => {
  let adminSessionId: string;

  test.beforeAll(async () => {
    adminSessionId = await seedSession("admin");
    // Something to export. Prices create the instrument they name, so this
    // seeds the instrument and price parts at once.
    await importPricesAndWait(adminSessionId, [
      {
        instrument: { type: IdentifierType.MIC_TICKER, value: "AAPL", domain: "XNAS" },
        currency: "USD",
        coverage: [{ from: "2024-01-02", before: "2024-01-04" }],
        rows: [
          { priceDate: "2024-01-02", close: "185.64" },
          { priceDate: "2024-01-03", close: "184.25" },
        ],
      },
    ]);
  });

  test("exports a file and imports it back, reporting a result per part", async ({
    context,
    page,
  }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");
    await expect(page.getByRole("heading", { name: "Archive" })).toBeVisible();

    // Parts the format does not carry yet are visible but not selectable, so the
    // menu says what the archive will hold rather than only what it holds today.
    const pluginConfig = page.getByLabel(/Plugin config/);
    await expect(pluginConfig).toBeDisabled();

    const downloadPromise = page.waitForEvent("download");
    await page.locator("[data-testid='export-archive']").click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe("system-archive.json");

    const exported = path.join(tmpdir(), "system-archive-e2e.json");
    await download.saveAs(exported);
    const doc = JSON.parse(await readFile(exported, "utf8"));

    // The envelope says what the file is, and the parts asked for are present.
    expect(doc.envelope.kind).toBe("SYSTEM");
    expect(doc.instruments).toBeDefined();
    expect(doc.prices).toBeDefined();
    expect(doc.prices.groups[0].rows).toHaveLength(2);

    // Import the same file back.
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(exported);
    await expect(page.locator("[data-testid='archive-import']")).toContainText("Carries");
    await page.locator("[data-testid='start-archive-import']").click();

    // A row per part, each finishing. The import runs on the server, so this is
    // watching a job rather than driving it.
    const parts = page.locator("[data-testid='job-parts']");
    await expect(parts).toBeVisible({ timeout: TIMEOUT_SLOW });
    await expect(parts).toContainText("Instruments");
    await expect(parts).toContainText("Prices");
    await expect(parts.getByText("Done")).toHaveCount(3, { timeout: TIMEOUT_SLOW });
  });

  test("shows a running import after the page is left and returned to", async ({
    context,
    page,
  }) => {
    await injectSession(context, adminSessionId);
    // The job is found rather than remembered, so a fresh visit shows the last
    // import even though this page never started one.
    await page.goto("/admin/archive");
    await expect(page.locator("[data-testid='archive-job']")).toBeVisible({
      timeout: TIMEOUT_SLOW,
    });
    await expect(page.locator("[data-testid='job-parts']")).toContainText("Prices");
  });

  test("refuses a file that is not a system archive", async ({ context, page }) => {
    await injectSession(context, adminSessionId);
    await page.goto("/admin/archive");

    const notAnArchive = path.join(tmpdir(), "not-an-archive.json");
    await writeFile(
      notAnArchive,
      JSON.stringify({ envelope: { format_version: 1, exported_at: "2026-07-30T00:00:00Z", kind: "USER" } }),
    );
    await page.locator("[data-testid='choose-archive-file']").click();
    await page.locator("input[aria-label='Choose archive file']").setInputFiles(notAnArchive);

    // Whether a file is a valid archive is answered at parse time, so this never
    // reaches a job.
    await expect(page.locator("[data-testid='archive-import']")).toContainText(
      "This is a user archive",
    );
    await expect(page.locator("[data-testid='start-archive-import']")).toHaveCount(0);
  });
});
