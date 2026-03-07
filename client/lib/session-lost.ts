/**
 * Central handler for session-lost (401/403) from API calls.
 * The app registers a callback (invalidate session + redirect); the gRPC-Web
 * layer calls it before throwing SessionLostError so every API call gets
 * consistent behavior without each page handling it.
 */

let handler: (() => void) | null = null;

export function registerSessionLostHandler(fn: () => void): () => void {
  handler = fn;
  return () => {
    handler = null;
  };
}

export function notifySessionLost(): void {
  handler?.();
}
