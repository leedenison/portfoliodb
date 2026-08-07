/**
 * Session bootstrap, injected into a PortfolioDB tab.
 *
 * A content script runs in the page's own origin, so the HttpOnly SameSite
 * session cookie is attached to its requests. It calls GetSession, which returns
 * the opaque session id in the response body, and hands it to the service worker
 * -- which cannot obtain it itself, because its own requests to the PortfolioDB
 * origin are cross-site and carry no cookie.
 *
 * Injected programmatically rather than declared in the manifest, because the
 * PortfolioDB origin is configured by the user.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { GetSessionRequestSchema, GetSessionResponseSchema } from "@/gen/auth/v1/auth_pb";
import { GetSessionServiceMethod, unaryFetch } from "@/lib/grpc-web";
import type { SessionBootstrapped } from "../lib/messages";

async function bootstrap(): Promise<SessionBootstrapped> {
  try {
    const bytes = await unaryFetch(
      window.location.origin,
      GetSessionServiceMethod,
      toBinary(GetSessionRequestSchema, create(GetSessionRequestSchema)),
      { credentials: "include" }
    );
    const res = fromBinary(GetSessionResponseSchema, bytes);
    const sessionId = res.session?.sessionId ?? "";
    if (!sessionId) {
      return { type: "session-bootstrapped", sessionId: "", error: "no session in response" };
    }
    return { type: "session-bootstrapped", sessionId };
  } catch (e) {
    return {
      type: "session-bootstrapped",
      sessionId: "",
      error: e instanceof Error ? e.message : String(e),
    };
  }
}

void bootstrap().then((msg) => chrome.runtime.sendMessage(msg));
