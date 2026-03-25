import Redis from "ioredis";
import { randomUUID } from "crypto";
import type { BrowserContext } from "@playwright/test";

const REDIS_URL = process.env.E2E_REDIS_URL ?? "redis://localhost:6381/0";
const SESSION_PREFIX = "portfoliodb:session:";
const COOKIE_NAME = "portfoliodb_session";

// Must match server/auth/session/store.go Data struct (JSON-serialised).
interface SessionData {
  Kind: string;
  UserID: string;
  Email: string;
  GoogleSub: string;
  Role: string;
  ServiceAccountID: string;
  CreatedAt: string;
  ExpiresAt: string;
  LastSeenAt: string;
}

export const TEST_USER_ID = "e2e00000-0000-0000-0000-000000000001";
export const TEST_ADMIN_ID = "e2e00000-0000-0000-0000-000000000002";

let redis: Redis | null = null;

function getRedis(): Redis {
  if (!redis) {
    redis = new Redis(REDIS_URL);
  }
  return redis;
}

export async function closeRedis(): Promise<void> {
  if (redis) {
    await redis.quit();
    redis = null;
  }
}

// Seed a user session into Redis. Returns the session ID (cookie value).
export async function seedSession(
  role: "user" | "admin" = "user"
): Promise<string> {
  const r = getRedis();
  const sessionId = randomUUID();
  const now = new Date();
  const expires = new Date(now.getTime() + 3600_000); // 1 hour

  const isAdmin = role === "admin";
  const data: SessionData = {
    Kind: "user",
    UserID: isAdmin ? TEST_ADMIN_ID : TEST_USER_ID,
    Email: isAdmin ? "e2e-admin@test.example.com" : "e2e@test.example.com",
    GoogleSub: isAdmin ? "e2e-admin-sub-001" : "e2e-test-sub-001",
    Role: role,
    ServiceAccountID: "",
    CreatedAt: now.toISOString(),
    ExpiresAt: expires.toISOString(),
    LastSeenAt: now.toISOString(),
  };

  await r.set(SESSION_PREFIX + sessionId, JSON.stringify(data), "EX", 3600);
  return sessionId;
}

// Delete a session from Redis (for session-expiry tests).
export async function deleteSession(sessionId: string): Promise<void> {
  const r = getRedis();
  await r.del(SESSION_PREFIX + sessionId);
}

// Inject the session cookie into a Playwright browser context.
export async function injectSession(
  context: BrowserContext,
  sessionId: string
): Promise<void> {
  await context.addCookies([
    {
      name: COOKIE_NAME,
      value: sessionId,
      domain: new URL(
        process.env.E2E_BASE_URL ?? "http://envoy:8080"
      ).hostname,
      path: "/",
      httpOnly: true,
      sameSite: "Lax",
    },
  ]);
}
