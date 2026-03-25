// Polls ListWorkers until all workers are idle with empty queues.

import { create, toBinary, fromBinary } from "@bufbuild/protobuf";
import {
  ListWorkersRequestSchema,
  ListWorkersResponseSchema,
  WorkerState,
} from "../../client/gen/api/v1/api_pb";
import { unaryFetch } from "./grpc";
import { seedSession } from "./auth";

const SERVICE_METHOD = "portfoliodb.api.v1.ApiService/ListWorkers";

// Poll ListWorkers until every worker reports IDLE and queue_depth == 0.
export async function waitForWorkersIdle(opts?: {
  pollMs?: number;
  timeoutMs?: number;
}): Promise<void> {
  const pollMs = opts?.pollMs ?? 500;
  const timeoutMs = opts?.timeoutMs ?? 120_000;

  // ListWorkers is admin-only; seed a temporary admin session.
  const adminSession = await seedSession("admin");

  const reqBytes = toBinary(
    ListWorkersRequestSchema,
    create(ListWorkersRequestSchema)
  );

  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    const resBytes = await unaryFetch(SERVICE_METHOD, reqBytes, adminSession);
    const res = fromBinary(ListWorkersResponseSchema, resBytes);

    const allIdle = res.workers.every(
      (w) => w.state === WorkerState.IDLE && w.queueDepth === 0
    );
    if (allIdle) return;

    await new Promise((r) => setTimeout(r, pollMs));
  }

  // Timeout — fetch one last time for diagnostics.
  const resBytes = await unaryFetch(SERVICE_METHOD, reqBytes, adminSession);
  const res = fromBinary(ListWorkersResponseSchema, resBytes);
  const summary = res.workers
    .map((w) => `${w.name}: state=${WorkerState[w.state]} queue=${w.queueDepth}`)
    .join(", ");
  throw new Error(
    `waitForWorkersIdle timed out after ${timeoutMs}ms. Workers: ${summary}`
  );
}
