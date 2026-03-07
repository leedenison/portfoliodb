/**
 * Minimal gRPC-Web unary transport.
 * Wire format: 1 byte 0x00 (uncompressed), 4 bytes big-endian length, then protobuf message.
 */

import { notifySessionLost } from "./session-lost";

const GRPC_WEB = 0x00;

/** Thrown when the server returns HTTP 401 or 403 (session invalid / forbidden). */
export class SessionLostError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "SessionLostError";
  }
}

/** Send gRPC-Web unary request and return response message bytes. */
export async function unaryFetch(
  baseUrl: string,
  serviceMethod: string,
  requestBytes: Uint8Array,
  options: { credentials?: RequestCredentials } = {}
): Promise<Uint8Array> {
  const body = new Uint8Array(5 + requestBytes.length);
  body[0] = GRPC_WEB;
  new DataView(body.buffer).setUint32(1, requestBytes.length, false);
  body.set(requestBytes, 5);

  const res = await fetch(`${baseUrl}/${serviceMethod}`, {
    method: "POST",
    headers: {
      "Content-Type": "application/grpc-web",
      "X-Grpc-Web": "1",
    },
    body,
    credentials: options.credentials ?? "include",
  });
  if (!res.ok) {
    if (res.status === 401 || res.status === 403) {
      notifySessionLost();
      throw new SessionLostError(`HTTP ${res.status}`);
    }
    throw new Error(`HTTP ${res.status}`);
  }
  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.length < 5) throw new Error("response too short");
  const len = new DataView(buf.buffer).getUint32(1, false);
  if (5 + len > buf.length) throw new Error("response truncated");
  return buf.subarray(5, 5 + len);
}

export const AuthServiceMethod = "portfoliodb.auth.v1.AuthService/Auth";
export const GetSessionServiceMethod = "portfoliodb.auth.v1.AuthService/GetSession";
export const LogoutServiceMethod = "portfoliodb.auth.v1.AuthService/Logout";
