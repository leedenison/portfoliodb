// Direct gRPC-Web calls for E2E tests.
// Bypasses the browser UI for rapid, concurrent API calls.

const BASE_URL = process.env.E2E_BASE_URL ?? "http://envoy:8080";
const API_PREFIX = "portfoliodb.api.v1.ApiService/";
const COOKIE_NAME = "portfoliodb_session";

// gRPC-Web envelope: 1 byte flag (0x00 = uncompressed) + 4 byte big-endian length + payload.
function envelope(payload: Uint8Array): Uint8Array {
  const buf = new Uint8Array(5 + payload.length);
  buf[0] = 0x00;
  new DataView(buf.buffer).setUint32(1, payload.length, false);
  buf.set(payload, 5);
  return buf;
}

// Encode SetDisplayCurrencyRequest manually.
// Proto: message SetDisplayCurrencyRequest { string display_currency = 1; }
// Field 1, wire type 2 (length-delimited): tag = 0x0a.
function encodeSetDisplayCurrency(currency: string): Uint8Array {
  const currencyBytes = new TextEncoder().encode(currency);
  const proto = new Uint8Array(2 + currencyBytes.length);
  proto[0] = 0x0a;
  proto[1] = currencyBytes.length;
  proto.set(currencyBytes, 2);
  return proto;
}

// Call SetDisplayCurrency via gRPC-Web with the given session cookie.
export async function setDisplayCurrency(
  sessionId: string,
  currency: string
): Promise<void> {
  const body = envelope(encodeSetDisplayCurrency(currency));
  const res = await fetch(`${BASE_URL}/${API_PREFIX}SetDisplayCurrency`, {
    method: "POST",
    headers: {
      "Content-Type": "application/grpc-web",
      "X-Grpc-Web": "1",
      Cookie: `${COOKIE_NAME}=${sessionId}`,
    },
    body,
  });
  if (!res.ok) {
    throw new Error(`SetDisplayCurrency(${currency}): HTTP ${res.status}`);
  }
}
