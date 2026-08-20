// VCR mode helpers mirroring the Go server/testutil/vcr package.
//
// Only the per-suite question is asked here. "Is anything being recorded" has no
// good answer for a harness that runs every spec in one process: recording one
// suite must leave the rest replaying untouched, so every decision that depends
// on record mode -- API keys, provider rate limits -- has to name the suite it
// is deciding for.

// isRecordingSuite returns true when the given suite identifier appears
// in the comma-separated VCR_MODE list.
export function isRecordingSuite(suite: string): boolean {
  const mode = process.env.VCR_MODE ?? "";
  if (mode === "") return false;
  return mode
    .split(",")
    .map((s) => s.trim())
    .includes(suite);
}
