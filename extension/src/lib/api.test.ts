import { beforeEach, describe, expect, it, vi } from "vitest";
import { create, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { GetSessionResponseSchema } from "@/gen/auth/v1/auth_pb";
import { ListTxsRequestSchema, ListTxsResponseSchema } from "@/gen/api/v1/api_pb";
import { Broker, TxType } from "@/gen/type/v1/type_pb";
import { UpsertTxsResponseSchema, UpsertTxsRequestSchema } from "@/gen/ingestion/v1/ingestion_pb";
import { fromBinary } from "@bufbuild/protobuf";
import { getSession, listTxs, upsertTxs } from "./api";

/** Wraps message bytes in a gRPC-Web data frame, as the server would. */
function grpcWebFrame(bytes: Uint8Array): ArrayBuffer {
  const framed = new Uint8Array(5 + bytes.length);
  framed[0] = 0x00;
  new DataView(framed.buffer).setUint32(1, bytes.length, false);
  framed.set(bytes, 5);
  return framed.buffer;
}

const ORIGIN = "http://localhost:8080";
const SESSION = "session-abc";

function mockFetch(responseBytes: Uint8Array) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(grpcWebFrame(responseBytes)));
}

/** The request message the transport was handed, decoded back out of the frame. */
function sentRequest(spy: ReturnType<typeof mockFetch>): Uint8Array {
  const body = spy.mock.calls[0]![1]!.body as Uint8Array;
  const len = new DataView(body.buffer, body.byteOffset, body.byteLength).getUint32(1, false);
  return body.subarray(5, 5 + len);
}

describe("api", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("sends the session as a bearer token and omits credentials", async () => {
    const res = create(GetSessionResponseSchema, { user: { email: "user@example.com" } });
    const spy = mockFetch(toBinary(GetSessionResponseSchema, res));

    const got = await getSession(ORIGIN, SESSION);

    expect(got.user?.email).toBe("user@example.com");
    const [url, init] = spy.mock.calls[0]!;
    expect(url).toBe(`${ORIGIN}/portfoliodb.auth.v1.AuthService/GetSession`);
    expect(init?.headers).toMatchObject({ Authorization: `Bearer ${SESSION}` });
    // A cookie would be dropped as cross-site anyway; omitting is explicit about
    // the bearer token being the only credential in play.
    expect(init?.credentials).toBe("omit");
  });

  it("encodes paging on ListTxs", async () => {
    const spy = mockFetch(toBinary(ListTxsResponseSchema, create(ListTxsResponseSchema, {})));

    await listTxs(ORIGIN, SESSION, { pageSize: 1, pageToken: "MTAw" });

    const req = fromBinary(ListTxsRequestSchema, sentRequest(spy));
    expect(req.pageSize).toBe(1);
    expect(req.pageToken).toBe("MTAw");
    expect(spy.mock.calls[0]![0]).toBe(`${ORIGIN}/portfoliodb.api.v1.ApiService/ListTxs`);
  });

  it("encodes the period and source on UpsertTxs", async () => {
    const spy = mockFetch(toBinary(UpsertTxsResponseSchema, create(UpsertTxsResponseSchema, { jobId: "job-1" })));
    const from = new Date("2026-07-01T00:00:00Z");
    const before = new Date("2026-07-27T00:00:00Z");

    const res = await upsertTxs(ORIGIN, SESSION, {
      broker: Broker.FIDELITY,
      source: "Fidelity:web:fidelity-csv",
      periodFrom: timestampFromDate(from),
      periodBefore: timestampFromDate(before),
      filename: "fidelity-ext-2026-07-27.csv",
      txs: [
        {
          timestamp: timestampFromDate(new Date("2026-07-10T00:00:00Z")),
          instrumentDescription: "ISHARES II PLC INRG",
          type: TxType.BUYSTOCK,
          quantity: "10",
        },
      ],
    });

    expect(res.jobId).toBe("job-1");
    const req = fromBinary(UpsertTxsRequestSchema, sentRequest(spy));
    // The period is the window that was requested, not the range of the rows:
    // that is what makes the replace delete transactions the broker cancelled.
    expect(req.periodFrom?.seconds).toBe(BigInt(Math.floor(from.getTime() / 1000)));
    expect(req.periodBefore?.seconds).toBe(BigInt(Math.floor(before.getTime() / 1000)));
    expect(req.source).toBe("Fidelity:web:fidelity-csv");
    expect(req.txs).toHaveLength(1);
  });

  it("propagates a gRPC error status from the response headers", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(null, { headers: { "grpc-status": "16", "grpc-message": "missing or invalid session" } })
    );

    await expect(getSession(ORIGIN, SESSION)).rejects.toThrow("missing or invalid session");
  });
});
