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

/** Thrown when the gRPC server returns a non-OK status (carried in headers or trailers). */
export class GrpcError extends Error {
  code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = "GrpcError";
    this.code = code;
  }
}

const GRPC_STATUS_UNAUTHENTICATED = 16;

/** Check response headers for gRPC error status (trailers-only responses). */
function checkHeadersForGrpcError(res: Response): void {
  const status = res.headers.get("grpc-status");
  if (status != null && status !== "0") {
    const code = parseInt(status, 10);
    const msg = res.headers.get("grpc-message") ?? `gRPC error ${code}`;
    if (code === GRPC_STATUS_UNAUTHENTICATED) {
      notifySessionLost();
      throw new SessionLostError(decodeURIComponent(msg));
    }
    throw new GrpcError(code, decodeURIComponent(msg));
  }
}

/** Parse a trailers frame (flag 0x80) from the body and throw the gRPC error it contains. */
function throwFromTrailersFrame(buf: Uint8Array): void {
  if (buf.length >= 5 && buf[0] === 0x80) {
    const len = new DataView(buf.buffer, buf.byteOffset, buf.byteLength).getUint32(1, false);
    if (buf.length >= 5 + len) {
      const text = new TextDecoder().decode(buf.subarray(5, 5 + len));
      let code = 0;
      let msg = "";
      for (const line of text.split("\r\n")) {
        if (line.startsWith("grpc-status:")) code = parseInt(line.slice(12).trim(), 10);
        else if (line.startsWith("grpc-message:")) msg = decodeURIComponent(line.slice(13).trim());
      }
      if (code !== 0) {
        if (code === GRPC_STATUS_UNAUTHENTICATED) {
          notifySessionLost();
          throw new SessionLostError(msg || `gRPC error ${code}`);
        }
        throw new GrpcError(code, msg || `gRPC error ${code}`);
      }
    }
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
  // Trailers-only gRPC responses surface errors in HTTP headers with an empty body.
  checkHeadersForGrpcError(res);
  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.length < 5) throw new Error("response too short");
  // Body may start with a trailers frame (0x80) instead of a data frame on error.
  throwFromTrailersFrame(buf);
  const len = new DataView(buf.buffer).getUint32(1, false);
  if (5 + len > buf.length) throw new Error("response truncated");
  return buf.subarray(5, 5 + len);
}

/** Send gRPC-Web server-streaming request; yields each response message's bytes. */
export async function* streamingFetch(
  baseUrl: string,
  serviceMethod: string,
  requestBytes: Uint8Array,
  options: { credentials?: RequestCredentials } = {}
): AsyncGenerator<Uint8Array> {
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
  checkHeadersForGrpcError(res);
  if (!res.body) throw new Error("no response body");

  const reader = res.body.getReader();
  let buf = new Uint8Array(0);

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;

    // Append new chunk to buffer.
    const merged = new Uint8Array(buf.length + value.length);
    merged.set(buf);
    merged.set(value, buf.length);
    buf = merged;

    // Consume all complete frames from the buffer.
    while (buf.length >= 5) {
      const flag = buf[0];
      const len = new DataView(buf.buffer, buf.byteOffset, buf.byteLength).getUint32(1, false);
      if (buf.length < 5 + len) break; // wait for more data
      const payload = buf.slice(5, 5 + len);
      buf = buf.slice(5 + len);
      if (flag === 0x80) return; // trailers frame
      yield payload;
    }
  }
}

export const AuthUserServiceMethod = "portfoliodb.auth.v1.AuthService/AuthUser";
export const GetSessionServiceMethod = "portfoliodb.auth.v1.AuthService/GetSession";
export const LogoutServiceMethod = "portfoliodb.auth.v1.AuthService/Logout";
