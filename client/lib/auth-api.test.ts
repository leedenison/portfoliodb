import { create, toBinary } from "@bufbuild/protobuf";
import { AuthResponseSchema } from "@/gen/auth/v1/auth_pb";
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

function authResponseBytes(payload: {
  user?: { id: string; email: string; name: string };
  userExists: boolean;
  sessionId: string;
}): Uint8Array {
  const msg = create(AuthResponseSchema, payload);
  return toBinary(AuthResponseSchema, msg);
}

describe("auth-api", () => {
  beforeEach(() => {
    mockUnaryFetch.mockReset();
  });

  describe("auth", () => {
    it("sends Auth request and returns user and session", async () => {
      mockUnaryFetch.mockResolvedValue(
        authResponseBytes({
          user: { id: "u1", email: "a@b.com", name: "Alice" },
          userExists: true,
          sessionId: "sess-123",
        })
      );

      const result = await auth("google-id-token");

      expect(result).toEqual({
        user: { id: "u1", email: "a@b.com", name: "Alice" },
        userExists: true,
        sessionId: "sess-123",
      });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.stringContaining(""),
        "portfoliodb.auth.v1.AuthService/Auth",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("returns null user when response has no user", async () => {
      mockUnaryFetch.mockResolvedValue(
        authResponseBytes({
          userExists: false,
          sessionId: "sess-456",
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
        authResponseBytes({
          user: { id: "u2", email: "b@c.com", name: "Bob" },
          userExists: true,
          sessionId: "sess-789",
        })
      );

      const result = await getSession();

      expect(result.user).toEqual({
        id: "u2",
        email: "b@c.com",
        name: "Bob",
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
