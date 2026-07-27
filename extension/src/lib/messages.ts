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

export type Message = SessionBootstrapped | ConnectRequest | StatusRequest;

/** Reply to connect and status. */
export interface SessionStatus {
  connected: boolean;
  email?: string;
  error?: string;
}
