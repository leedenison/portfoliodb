/**
 * Minimal gRPC-Web unary client for Auth RPCs (no codegen).
 * Wire format: 1 byte 0x00 (uncompressed), 4 bytes big-endian length, then protobuf message.
 */

const GRPC_WEB = 0x00;

function encodeVarint(value: number): number[] {
  const bytes: number[] = [];
  let v = value;
  while (v > 0x7f) {
    bytes.push((v & 0x7f) | 0x80);
    v >>>= 7;
  }
  bytes.push(v);
  return bytes;
}

/** Encode AuthRequest { google_id_token: string } as protobuf (field 1, length-delimited). */
export function encodeAuthRequest(token: string): Uint8Array {
  const tokenBytes = new TextEncoder().encode(token);
  const tag = (1 << 3) | 2; // field 1, wire type 2 (length-delimited)
  const buf = [...encodeVarint(tag), ...encodeVarint(tokenBytes.length), ...Array.from(tokenBytes)];
  return new Uint8Array(buf);
}

/** Encode empty message for Logout. */
export function encodeEmpty(): Uint8Array {
  return new Uint8Array(0);
}

function readVarint(bytes: Uint8Array, offset: number): { value: number; next: number } {
  let value = 0;
  let shift = 0;
  let i = offset;
  while (i < bytes.length) {
    const b = bytes[i++];
    value |= (b & 0x7f) << shift;
    if ((b & 0x80) === 0) return { value, next: i };
    shift += 7;
    if (shift > 28) throw new Error("varint too long");
  }
  throw new Error("varint truncated");
}

function readLengthDelimited(bytes: Uint8Array, offset: number): { data: Uint8Array; next: number } {
  const { value: len, next } = readVarint(bytes, offset);
  if (next + len > bytes.length) throw new Error("message truncated");
  return { data: bytes.subarray(next, next + len), next: next + len };
}

export interface AuthResponseUser {
  id: string;
  email: string;
  name: string;
}

export interface AuthResponsePayload {
  user: AuthResponseUser | null;
  userExists: boolean;
  sessionId: string;
}

/** Decode AuthResponse from protobuf bytes. */
export function decodeAuthResponse(bytes: Uint8Array): AuthResponsePayload {
  const result: AuthResponsePayload = {
    user: null,
    userExists: false,
    sessionId: "",
  };
  let i = 0;
  while (i < bytes.length) {
    const { value: tag, next } = readVarint(bytes, i);
    i = next;
    const field = tag >> 3;
    const wireType = tag & 7;
    if (wireType === 2) {
      const { data, next: n } = readLengthDelimited(bytes, i);
      i = n;
      if (field === 1) {
        // User submessage
        result.user = decodeUser(data);
      } else if (field === 3) {
        result.sessionId = new TextDecoder().decode(data);
      }
    } else if (wireType === 0 && field === 2) {
      const { value, next: n } = readVarint(bytes, i);
      i = n;
      result.userExists = value !== 0;
    } else {
      break;
    }
  }
  return result;
}

function decodeUser(bytes: Uint8Array): AuthResponseUser {
  const user: AuthResponseUser = { id: "", email: "", name: "" };
  let i = 0;
  while (i < bytes.length) {
    const { value: tag, next } = readVarint(bytes, i);
    i = next;
    const field = tag >> 3;
    if ((tag & 7) === 2) {
      const { data, next: n } = readLengthDelimited(bytes, i);
      i = n;
      const s = new TextDecoder().decode(data);
      if (field === 1) user.id = s;
      else if (field === 2) user.email = s;
      else if (field === 3) user.name = s;
    }
  }
  return user;
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
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.length < 5) throw new Error("response too short");
  const len = new DataView(buf.buffer).getUint32(1, false);
  if (5 + len > buf.length) throw new Error("response truncated");
  return buf.subarray(5, 5 + len);
}

export const AuthServiceMethod = "portfoliodb.auth.v1.AuthService/Auth";
export const LogoutServiceMethod = "portfoliodb.auth.v1.AuthService/Logout";
