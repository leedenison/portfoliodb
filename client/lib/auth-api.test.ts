import { create, toBinary } from "@bufbuild/protobuf";
import { AuthUserResponseSchema } from "@/gen/auth/v1/auth_pb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { auth, getSession, logout } from "./auth-api";
import * as grpcWeb from "./grpc-web";

vi.mock("./grpc-web", async (importOriginal) => {
  const actual = await importOriginal<typeof grpcWeb>();
  return {
    ...actual,
    unaryFetch: vi.fn(),
  };
});

const mockUnaryFetch = vi.mocked(grpcWeb.unaryFetch);

function authUserResponseBytes(payload: {
  user?: { id: string; email: string; name: string; role?: string };
  userExists: boolean;
  session?: { sessionId: string };
}): Uint8Array {
  const msg = create(AuthUserResponseSchema, payload);
  return toBinary(AuthUserResponseSchema, msg);
}

describe("auth-api", () => {
  beforeEach(() => {
    mockUnaryFetch.mockReset();
  });

  describe("auth", () => {
    it("sends AuthUser request and returns user and session", async () => {
      mockUnaryFetch.mockResolvedValue(
        authUserResponseBytes({
          user: { id: "u1", email: "a@b.com", name: "Alice", role: "user" },
          userExists: true,
          session: { sessionId: "sess-123" },
        })
      );

      const result = await auth("google-id-token");

      expect(result).toEqual({
        user: { id: "u1", email: "a@b.com", name: "Alice", role: "user" },
        userExists: true,
        sessionId: "sess-123",
      });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.stringContaining(""),
        "portfoliodb.auth.v1.AuthService/AuthUser",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("returns null user when response has no user", async () => {
      mockUnaryFetch.mockResolvedValue(
        authUserResponseBytes({
          userExists: false,
          session: { sessionId: "sess-456" },
        })
      );

      const result = await auth("token");

      expect(result.user).toBeNull();
      expect(result.userExists).toBe(false);
      expect(result.sessionId).toBe("sess-456");
    });
  });

  describe("getSession", () => {
    it("returns current user from session", async () => {
      mockUnaryFetch.mockResolvedValue(
        authUserResponseBytes({
          user: { id: "u2", email: "b@c.com", name: "Bob", role: "admin" },
          userExists: true,
          session: { sessionId: "sess-789" },
        })
      );

      const result = await getSession();

      expect(result.user).toEqual({
        id: "u2",
        email: "b@c.com",
        name: "Bob",
        role: "admin",
      });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.auth.v1.AuthService/GetSession",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });

  describe("logout", () => {
    it("calls Logout and returns void", async () => {
      mockUnaryFetch.mockResolvedValue(new Uint8Array(0));

      await logout();

      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.auth.v1.AuthService/Logout",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });
});
