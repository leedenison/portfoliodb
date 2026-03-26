// Read telemetry counters directly from Redis.
// Uses the same Redis instance as the auth helper.

import Redis from "ioredis";

const REDIS_URL = process.env.E2E_REDIS_URL ?? "redis://localhost:6381/0";
const COUNTER_PREFIX = "portfoliodb:counters:";

let redis: Redis | null = null;

function getRedis(): Redis {
  if (!redis) {
    redis = new Redis(REDIS_URL);
  }
  return redis;
}

export async function closeCountersRedis(): Promise<void> {
  if (redis) {
    await redis.quit();
    redis = null;
  }
}

// Get the current value of a telemetry counter.
export async function getCounter(name: string): Promise<number> {
  const r = getRedis();
  const val = await r.get(COUNTER_PREFIX + name);
  return val ? parseInt(val, 10) : 0;
}
