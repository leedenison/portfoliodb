// Typed gRPC client for the E2E cassette control service.
// Calls LoadCassette / UnloadCassette on the Go server to swap VCR
// cassettes between test suites, enabling per-suite isolation.
//
// Important: call waitForWorkersIdle() BEFORE unloadCassette() in suites
// that trigger background workers, so in-flight HTTP calls complete before
// the recorder is stopped.

import { createClient } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-node";
import { E2eService } from "../gen/e2e/v1/e2e_pb";

const transport = createGrpcWebTransport({
  baseUrl: process.env.E2E_BASE_URL ?? "http://envoy:8080",
});

const client = createClient(E2eService, transport);

export async function loadCassette(name: string): Promise<void> {
  await client.loadCassette({ name });
}

export async function unloadCassette(): Promise<void> {
  await client.unloadCassette({});
}

/**
 * Replace the server process with a fresh one and wait until the replacement is
 * answering.
 *
 * Waiting for the server to go unreachable and come back does not work: the
 * process is replaced rather than respawned, and the gap is a few milliseconds
 * -- easily missed between two polls, which makes the wait hang rather than
 * pass. The start time is the reliable signal, because it is the one thing the
 * new process cannot have inherited.
 */
export async function restartServer(timeoutMs = 60_000): Promise<void> {
  const before = (await client.processInfo({})).startedAt;
  if (!before) throw new Error("server reported no start time");

  await client.restartProcess({});

  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const now = (await client.processInfo({})).startedAt;
      // Different start time, so this is not the process we asked to leave.
      if (now && (now.seconds !== before.seconds || now.nanos !== before.nanos)) {
        return;
      }
    } catch {
      // Unreachable while the process is being replaced, which is expected.
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`server did not restart within ${timeoutMs}ms`);
}
