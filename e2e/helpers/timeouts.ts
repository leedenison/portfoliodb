// Test timeout tiers. Use SLOW for tests that wait for background workers
// (e.g. rate-limited API calls in record mode).
export const TIMEOUT_FAST = 15_000;
export const TIMEOUT_REGULAR = 30_000;
export const TIMEOUT_SLOW = 180_000;
