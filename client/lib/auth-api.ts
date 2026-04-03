import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { EmptySchema } from "@bufbuild/protobuf/wkt";
import {
  AuthUserRequestSchema,
  AuthUserResponseSchema,
} from "@/gen/auth/v1/auth_pb";
import {
  AuthUserServiceMethod,
  GetSessionServiceMethod,
  LogoutServiceMethod,
  unaryFetch,
} from "./grpc-web";

export interface AuthResponsePayload {
  user: { id: string; email: string; name: string; role: string } | null;
  userExists: boolean;
  sessionId: string;
}

function getBaseUrl(): string {
  if (typeof window === "undefined") return "http://localhost:8080";
  return (process.env.NEXT_PUBLIC_GRPC_WEB_BASE ?? window.location.origin).replace(/\/$/, "");
}

function authUserResponseToPayload(res: { user?: { id: string; email: string; name: string; role?: string } | undefined; userExists: boolean; session?: { sessionId: string } | undefined }): AuthResponsePayload {
  return {
    user: res.user
      ? { id: res.user.id, email: res.user.email, name: res.user.name, role: res.user.role ?? "user" }
      : null,
    userExists: res.userExists,
    sessionId: res.session?.sessionId ?? "",
  };
}

export async function auth(googleIdToken: string): Promise<AuthResponsePayload> {
  const base = getBaseUrl();
  const req = create(AuthUserRequestSchema, { googleIdToken });
  const resBytes = await unaryFetch(base, AuthUserServiceMethod, toBinary(AuthUserRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(AuthUserResponseSchema, resBytes);
  return authUserResponseToPayload(res);
}

/** Returns current user when the request has a valid session cookie; throws if unauthenticated. */
export async function getSession(): Promise<AuthResponsePayload> {
  const base = getBaseUrl();
  const req = create(EmptySchema, {});
  const resBytes = await unaryFetch(base, GetSessionServiceMethod, toBinary(EmptySchema, req), {
    credentials: "include",
  });
  const res = fromBinary(AuthUserResponseSchema, resBytes);
  return authUserResponseToPayload(res);
}

export async function logout(): Promise<void> {
  const base = getBaseUrl();
  const req = create(EmptySchema, {});
  await unaryFetch(base, LogoutServiceMethod, toBinary(EmptySchema, req), {
    credentials: "include",
  });
}
