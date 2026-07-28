/** Messages exchanged between the popup, the service worker, and content scripts. */

/** Sent by the bootstrap content script after it reads the session from the page. */
export interface SessionBootstrapped {
  type: "session-bootstrapped";
  sessionId: string;
  /** Set instead of sessionId when the page itself has no valid session. */
  error?: string;
}

/** Popup asks the service worker to run the bootstrap. */
export interface ConnectRequest {
  type: "connect";
}

/** Popup asks for the current session state without changing it. */
export interface StatusRequest {
  type: "status";
}

/**
 * Popup asks for an export to be captured and converted, without uploading.
 * Dates are "yyyy-MM-dd" as the date inputs produce them.
 */
export interface DryRunRequest {
  type: "dry-run";
  recipeId: string;
  from: string;
  to: string;
}

export type Message = SessionBootstrapped | ConnectRequest | StatusRequest | DryRunRequest;

/** Reply to dry-run: what the export produced, with nothing sent to the server. */
export interface DryRunResult {
  ok: boolean;
  error?: string;
  /** The window actually requested, in the broker's own date format. */
  requested?: { from: string; to: string };
  /** Rows in the captured payload, before conversion. */
  rowCount?: number;
  txCount?: number;
  /** Distinct broker transaction types the converter did not recognise. */
  droppedTypes?: string[];
  droppedRows?: number;
  /** First lines of the payload, to eyeball when conversion fails outright. */
  preview?: string;
}

/** Reply to connect and status. */
export interface SessionStatus {
  connected: boolean;
  email?: string;
  error?: string;
}
