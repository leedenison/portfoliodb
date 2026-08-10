import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Broker } from "@/gen/type/v1/type_pb";
import { PostingSchema } from "@/gen/archive/v1/txs_pb";
import {
  UpsertTxsResponseSchema,
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
          UpsertTxsResponseSchema,
          create(UpsertTxsResponseSchema, { jobId: "job-abc" })
        )
      );

      const posting = create(PostingSchema, {
        timestamp: timestampFromDate(new Date("2024-01-15")),
        instrumentDescription: "AAPL",
        type: 5, // BUYSTOCK
        quantity: "10",
        account: "",
      });

      const result = await upsertTxs({
        window: {
          broker: Broker.IBKR,
          source: "IBKR:web:standard",
          periodFrom: timestampFromDate(new Date("2024-01-01")),
          periodBefore: timestampFromDate(new Date("2024-02-01")),
          postings: [posting],
        },
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
      expect(decoded.window?.broker).toBe(Broker.IBKR);
      expect(decoded.window?.source).toBe("IBKR:web:standard");
      expect(decoded.window?.postings).toHaveLength(1);
      expect(decoded.window?.postings[0].instrumentDescription).toBe("AAPL");
      expect(decoded.window?.postings[0].quantity).toBe("10");
      expect(decoded.window?.postings[0].account).toBe("");
    });
  });
});
