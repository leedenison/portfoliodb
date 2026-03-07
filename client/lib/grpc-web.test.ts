import { afterEach, describe, expect, it, vi } from "vitest";
import {
  unaryFetch,
  SessionLostError,
  AuthServiceMethod,
  GetSessionServiceMethod,
  LogoutServiceMethod,
} from "./grpc-web";

describe("grpc-web", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("unaryFetch", () => {
    it("builds gRPC-Web body: 0x00, 4-byte big-endian length, then payload", async () => {
      const payload = new Uint8Array([1, 2, 3]);
      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(
          new Uint8Array([
            0x00,
            0,
            0,
            0,
            3,
            1,
            2,
            3,
          ]).buffer
        )
      );

      await unaryFetch("https://api.example.com", "Service/Method", payload);

      expect(fetchSpy).toHaveBeenCalledTimes(1);
      const [url, init] = fetchSpy.mock.calls[0];
      expect(url).toBe("https://api.example.com/Service/Method");
      expect(init?.method).toBe("POST");
      expect(init?.headers).toEqual({
        "Content-Type": "application/grpc-web",
        "X-Grpc-Web": "1",
      });
      const body = init?.body as Uint8Array;
      expect(body.byteLength).toBe(5 + 3);
      expect(body[0]).toBe(0x00);
      expect(new DataView(body.buffer).getUint32(1, false)).toBe(3);
      expect(body.subarray(5)).toEqual(new Uint8Array([1, 2, 3]));
    });

    it("uses credentials from options", async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(new Uint8Array([0, 0, 0, 0, 0]).buffer)
      );

      await unaryFetch("https://api.example.com", "S/M", new Uint8Array(0), {
        credentials: "omit",
      });

      expect(fetchSpy).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({ credentials: "omit" })
      );
    });

    it("defaults credentials to include", async () => {
      const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(new Uint8Array([0, 0, 0, 0, 0]).buffer)
      );

      await unaryFetch("https://api.example.com", "S/M", new Uint8Array(0));

      expect(fetchSpy).toHaveBeenCalledWith(
        expect.any(String),
        expect.objectContaining({ credentials: "include" })
      );
    });

    it("returns response message bytes (after 5-byte header)", async () => {
      const responsePayload = new Uint8Array([10, 20, 30]);
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(
          new Uint8Array([
            0x00,
            0,
            0,
            0,
            3,
            10,
            20,
            30,
          ]).buffer
        )
      );

      const result = await unaryFetch(
        "https://api.example.com",
        "S/M",
        new Uint8Array(0)
      );

      expect(result).toEqual(new Uint8Array([10, 20, 30]));
    });

    it("throws on HTTP error", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(null, { status: 500 })
      );

      await expect(
        unaryFetch("https://api.example.com", "S/M", new Uint8Array(0))
      ).rejects.toThrow("HTTP 500");
    });

    it("throws when response body is too short", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(new Uint8Array([0, 0, 0]).buffer)
      );

      await expect(
        unaryFetch("https://api.example.com", "S/M", new Uint8Array(0))
      ).rejects.toThrow("response too short");
    });

    it("throws when response is truncated", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(
          new Uint8Array([0x00, 0, 0, 0, 10, 1, 2]).buffer
        )
      );

      await expect(
        unaryFetch("https://api.example.com", "S/M", new Uint8Array(0))
      ).rejects.toThrow("response truncated");
    });

    it("throws SessionLostError on HTTP 401", async () => {
      vi.spyOn(globalThis, "fetch").mockResolvedValue(
        new Response(null, { status: 401 })
      );

      const p = unaryFetch("https://api.example.com", "S/M", new Uint8Array(0));
      await expect(p).rejects.toThrow(SessionLostError);
      await expect(p).rejects.toThrow("HTTP 401");
    });
  });

  describe("service method constants", () => {
    it("exports auth service method names", () => {
      expect(AuthServiceMethod).toBe("portfoliodb.auth.v1.AuthService/Auth");
      expect(GetSessionServiceMethod).toBe(
        "portfoliodb.auth.v1.AuthService/GetSession"
      );
      expect(LogoutServiceMethod).toBe(
        "portfoliodb.auth.v1.AuthService/Logout"
      );
    });
  });
});
