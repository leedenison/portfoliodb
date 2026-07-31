/**
 * Turns a thrown value into something renderable.
 *
 * The API layer throws rather than returning a result union, and every call site
 * used to repeat `e instanceof Error ? e.message : String(e)`. `fallback` covers
 * an Error with an empty message and the callers that want their own wording
 * ("Failed to load counters") in place of whatever the transport produced.
 */
export function errorMessage(e: unknown, fallback = "Something went wrong"): string {
  if (e instanceof Error) return e.message || fallback;
  return e == null ? fallback : String(e);
}
