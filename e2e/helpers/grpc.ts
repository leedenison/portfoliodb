// Minimal gRPC-Web unary transport for Node.js.
// Adapted from client/lib/grpc-web.ts for use outside the browser.

const BASE_URL = process.env.E2E_BASE_URL ?? "http://envoy:8080";
const COOKIE_NAME = "portfoliodb_session";

// Send a gRPC-Web unary request and return the response message bytes.
export async function unaryFetch(
  serviceMethod: string,
  requestBytes: Uint8Array,
  sessionCookie?: string
): Promise<Uint8Array> {
  const body = new Uint8Array(5 + requestBytes.length);
  body[0] = 0x00; // uncompressed
  new DataView(body.buffer).setUint32(1, requestBytes.length, false);
  body.set(requestBytes, 5);

  const headers: Record<string, string> = {
    "Content-Type": "application/grpc-web",
    "X-Grpc-Web": "1",
  };
  if (sessionCookie) {
    headers["Cookie"] = `${COOKIE_NAME}=${sessionCookie}`;
  }

  const res = await fetch(`${BASE_URL}/${serviceMethod}`, {
    method: "POST",
    headers,
    body,
  });
  if (!res.ok) {
    throw new Error(`gRPC ${serviceMethod}: HTTP ${res.status}`);
  }

  const buf = new Uint8Array(await res.arrayBuffer());
  if (buf.length < 5) throw new Error(`gRPC ${serviceMethod}: response too short`);
  const len = new DataView(buf.buffer).getUint32(1, false);
  if (5 + len > buf.length) throw new Error(`gRPC ${serviceMethod}: response truncated`);
  return buf.subarray(5, 5 + len);
}
