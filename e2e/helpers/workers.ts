// Polls ListWorkers until all workers are idle with empty queues.
//
// Decodes the protobuf response manually to avoid cross-project module
// resolution issues with the generated client/gen/ code.

import { unaryFetch } from "./grpc";
import { seedSession } from "./auth";

const SERVICE_METHOD = "portfoliodb.api.v1.ApiService/ListWorkers";

// WorkerState enum values from api.proto.
const WORKER_STATE_IDLE = 1;

interface Worker {
  name: string;
  state: number;
  queueDepth: number;
}

// Minimal protobuf varint decoder.
function readVarint(buf: Uint8Array, offset: number): [number, number] {
  let result = 0;
  let shift = 0;
  let pos = offset;
  while (pos < buf.length) {
    const b = buf[pos++];
    result |= (b & 0x7f) << shift;
    if ((b & 0x80) === 0) return [result, pos];
    shift += 7;
  }
  throw new Error("varint overflows buffer");
}

// Decode a ListWorkersResponse protobuf message.
// ListWorkersResponse: repeated Worker workers = 1;
// Worker: string name = 1, WorkerState state = 2, string summary = 3,
//         int32 queue_depth = 4, Timestamp updated_at = 5;
function decodeListWorkersResponse(buf: Uint8Array): Worker[] {
  const workers: Worker[] = [];
  let pos = 0;

  while (pos < buf.length) {
    const [tag, nextPos] = readVarint(buf, pos);
    pos = nextPos;
    const fieldNumber = tag >>> 3;
    const wireType = tag & 0x07;

    if (fieldNumber === 1 && wireType === 2) {
      // Length-delimited: embedded Worker message.
      const [len, dataStart] = readVarint(buf, pos);
      const workerBuf = buf.subarray(dataStart, dataStart + len);
      workers.push(decodeWorker(workerBuf));
      pos = dataStart + len;
    } else if (wireType === 0) {
      // Varint — skip.
      const [, next] = readVarint(buf, pos);
      pos = next;
    } else if (wireType === 2) {
      // Length-delimited — skip.
      const [len, dataStart] = readVarint(buf, pos);
      pos = dataStart + len;
    } else if (wireType === 5) {
      pos += 4; // 32-bit — skip.
    } else if (wireType === 1) {
      pos += 8; // 64-bit — skip.
    }
  }

  return workers;
}

function decodeWorker(buf: Uint8Array): Worker {
  let name = "";
  let state = 0;
  let queueDepth = 0;
  let pos = 0;

  while (pos < buf.length) {
    const [tag, nextPos] = readVarint(buf, pos);
    pos = nextPos;
    const fieldNumber = tag >>> 3;
    const wireType = tag & 0x07;

    if (wireType === 0) {
      const [val, next] = readVarint(buf, pos);
      pos = next;
      if (fieldNumber === 2) state = val;
      else if (fieldNumber === 4) queueDepth = val;
    } else if (wireType === 2) {
      const [len, dataStart] = readVarint(buf, pos);
      if (fieldNumber === 1) {
        name = new TextDecoder().decode(buf.subarray(dataStart, dataStart + len));
      }
      pos = dataStart + len;
    } else if (wireType === 5) {
      pos += 4;
    } else if (wireType === 1) {
      pos += 8;
    }
  }

  return { name, state, queueDepth };
}

// Poll ListWorkers until every worker reports IDLE and queue_depth == 0.
export async function waitForWorkersIdle(opts?: {
  pollMs?: number;
  timeoutMs?: number;
}): Promise<void> {
  const pollMs = opts?.pollMs ?? 500;
  const timeoutMs = opts?.timeoutMs ?? 120_000;

  // ListWorkers is admin-only; seed a temporary admin session.
  const adminSession = await seedSession("admin");

  // ListWorkersRequest is empty — zero-length protobuf body.
  const reqBytes = new Uint8Array(0);

  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    const resBytes = await unaryFetch(SERVICE_METHOD, reqBytes, adminSession);
    const workers = decodeListWorkersResponse(resBytes);

    const allIdle = workers.every(
      (w) => w.state === WORKER_STATE_IDLE && w.queueDepth === 0
    );
    if (allIdle) return;

    await new Promise((r) => setTimeout(r, pollMs));
  }

  // Timeout — fetch one last time for diagnostics.
  const resBytes = await unaryFetch(SERVICE_METHOD, reqBytes, adminSession);
  const workers = decodeListWorkersResponse(resBytes);
  const stateNames = ["UNSPECIFIED", "IDLE", "RUNNING"];
  const summary = workers
    .map((w) => `${w.name}: state=${stateNames[w.state] ?? w.state} queue=${w.queueDepth}`)
    .join(", ");
  throw new Error(
    `waitForWorkersIdle timed out after ${timeoutMs}ms. Workers: ${summary}`
  );
}
