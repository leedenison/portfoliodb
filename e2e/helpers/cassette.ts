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
