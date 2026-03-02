import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Broker } from "@/gen/api/v1/api_pb";
import { TxSchema } from "@/gen/api/v1/api_pb";
import {
  IngestionResponseSchema,
  UpsertTxsRequestSchema,
} from "@/gen/ingestion/v1/ingestion_pb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { upsertTxs } from "./ingestion-api";
import * as grpcWeb from "./grpc-web";

vi.mock("./grpc-web", async (importOriginal) => {
  const actual = await importOriginal<typeof grpcWeb>();
  return {
    ...actual,
    unaryFetch: vi.fn(),
  };
});

const mockUnaryFetch = vi.mocked(grpcWeb.unaryFetch);

describe("ingestion-api", () => {
  beforeEach(() => {
    mockUnaryFetch.mockReset();
  });

  describe("upsertTxs", () => {
    it("sends UpsertTxs request and returns job_id", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          IngestionResponseSchema,
          create(IngestionResponseSchema, { jobId: "job-abc" })
        )
      );

      const tx = create(TxSchema, {
        timestamp: timestampFromDate(new Date("2024-01-15")),
        instrumentDescription: "AAPL",
        type: 5, // BUYSTOCK
        quantity: 10,
        account: "",
      });

      const result = await upsertTxs({
        broker: Broker.IBKR,
        source: "IBKR:web:standard",
        periodFrom: timestampFromDate(new Date("2024-01-01")),
        periodTo: timestampFromDate(new Date("2024-01-31")),
        txs: [tx],
      });

      expect(result.jobId).toBe("job-abc");
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.ingestion.v1.IngestionService/UpsertTxs",
        expect.any(Uint8Array),
        { credentials: "include" }
      );

      const reqBytes = mockUnaryFetch.mock.calls[0]?.[2];
      expect(reqBytes).toBeDefined();
      const decoded = fromBinary(UpsertTxsRequestSchema, reqBytes!);
      expect(decoded.broker).toBe(Broker.IBKR);
      expect(decoded.source).toBe("IBKR:web:standard");
      expect(decoded.txs).toHaveLength(1);
      expect(decoded.txs[0].instrumentDescription).toBe("AAPL");
      expect(decoded.txs[0].quantity).toBe(10);
      expect(decoded.txs[0].account).toBe("");
    });
  });
});
