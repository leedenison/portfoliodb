// VCR mode helpers mirroring the Go server/testutil/vcr package.

// isRecording returns true when VCR_MODE is non-empty, meaning at least
// one suite is being recorded.
export function isRecording(): boolean {
  return (process.env.VCR_MODE ?? "") !== "";
}

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
