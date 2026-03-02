import {
  AuthServiceMethod,
  LogoutServiceMethod,
  encodeAuthRequest,
  encodeEmpty,
  unaryFetch,
  decodeAuthResponse,
} from "./grpc-web";
import type { AuthResponsePayload } from "./grpc-web";

export type { AuthResponsePayload };

const defaultBase = typeof window !== "undefined" ? "" : "http://localhost:8080";

function getBaseUrl(): string {
  if (typeof window === "undefined") return defaultBase;
  return (process.env.NEXT_PUBLIC_GRPC_WEB_BASE ?? window.location.origin).replace(/\/$/, "");
}

export async function auth(googleIdToken: string): Promise<AuthResponsePayload> {
  const base = getBaseUrl();
  const reqBytes = encodeAuthRequest(googleIdToken);
  const resBytes = await unaryFetch(base, AuthServiceMethod, reqBytes, { credentials: "include" });
  return decodeAuthResponse(resBytes);
}

export async function logout(): Promise<void> {
  const base = getBaseUrl();
  const reqBytes = encodeEmpty();
  await unaryFetch(base, LogoutServiceMethod, reqBytes, { credentials: "include" });
}
