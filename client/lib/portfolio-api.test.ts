import { create, toBinary } from "@bufbuild/protobuf";
import {
  CountResidualBalancesResponseSchema,
  CreatePortfolioResponseSchema,
  GetJobResponseSchema,
  ListInstrumentsResponseSchema,
  ListPortfoliosResponseSchema,
  ListResidualBalancesResponseSchema,
  UpdatePortfolioResponseSchema,
  AccountType,
  AssetClass,
  Broker,
  IdentifierType,
  TxType,
} from "@/gen/api/v1/api_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { JobStatus } from "@/gen/api/v1/api_pb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  listPortfolios,
  createPortfolio,
  updatePortfolio,
  deletePortfolio,
  getJob,
  listInstruments,
  listResidualBalances,
  countResidualBalances,
} from "./portfolio-api";
import * as grpcWeb from "./grpc-web";

vi.mock("./grpc-web", async (importOriginal) => {
  const actual = await importOriginal<typeof grpcWeb>();
  return {
    ...actual,
    unaryFetch: vi.fn(),
  };
});

const mockUnaryFetch = vi.mocked(grpcWeb.unaryFetch);

describe("portfolio-api", () => {
  beforeEach(() => {
    mockUnaryFetch.mockReset();
  });

  describe("listPortfolios", () => {
    it("returns portfolios and nextPageToken", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListPortfoliosResponseSchema,
          create(ListPortfoliosResponseSchema, {
            portfolios: [
              { id: "p1", name: "Portfolio 1" },
              { id: "p2", name: "Portfolio 2" },
            ],
            nextPageToken: "token-abc",
          })
        )
      );

      const result = await listPortfolios();

      expect(result.portfolios).toHaveLength(2);
      expect(result.portfolios[0]).toEqual({ id: "p1", name: "Portfolio 1" });
      expect(result.portfolios[1]).toEqual({ id: "p2", name: "Portfolio 2" });
      expect(result.nextPageToken).toBe("token-abc");
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/ListPortfolios",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("passes pageToken when provided", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListPortfoliosResponseSchema,
          create(ListPortfoliosResponseSchema, {
            portfolios: [],
            nextPageToken: "",
          })
        )
      );

      await listPortfolios("next-token");

      const call = mockUnaryFetch.mock.calls[0];
      expect(call?.[2]).toBeDefined();
      // Request body is serialized ListPortfoliosRequest with pageToken
      expect(call?.[1]).toBe("portfoliodb.api.v1.ApiService/ListPortfolios");
    });

    it("returns null nextPageToken when empty", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListPortfoliosResponseSchema,
          create(ListPortfoliosResponseSchema, {
            portfolios: [],
            nextPageToken: "",
          })
        )
      );

      const result = await listPortfolios();

      expect(result.nextPageToken).toBeNull();
    });
  });

  describe("createPortfolio", () => {
    it("sends name and returns created portfolio", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          CreatePortfolioResponseSchema,
          create(CreatePortfolioResponseSchema, {
            portfolio: { id: "p-new", name: "My Portfolio" },
          })
        )
      );

      const result = await createPortfolio("My Portfolio");

      expect(result).toEqual({ id: "p-new", name: "My Portfolio" });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/CreatePortfolio",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });

  describe("updatePortfolio", () => {
    it("sends id and name and returns updated portfolio", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          UpdatePortfolioResponseSchema,
          create(UpdatePortfolioResponseSchema, {
            portfolio: { id: "p1", name: "Updated Name" },
          })
        )
      );

      const result = await updatePortfolio("p1", "Updated Name");

      expect(result).toEqual({ id: "p1", name: "Updated Name" });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/UpdatePortfolio",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });

  describe("deletePortfolio", () => {
    it("sends portfolio id and returns void", async () => {
      mockUnaryFetch.mockResolvedValue(new Uint8Array(0));

      await deletePortfolio("p1");

      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/DeletePortfolio",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });

  describe("listInstruments", () => {
    it("returns instruments, nextPageToken, and totalCount", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListInstrumentsResponseSchema,
          create(ListInstrumentsResponseSchema, {
            instruments: [
              {
                id: "inst-1",
                name: "Apple Inc.",
                assetClass: AssetClass.STOCK,
                exchange: "XNAS",
                currency: "USD",
                identifiers: [
                  { type: IdentifierType.MIC_TICKER, value: "AAPL", domain: "XNAS", canonical: true },
                ],
              },
            ],
            nextPageToken: "page-2",
            totalCount: 42,
          })
        )
      );

      const result = await listInstruments({ search: "AAPL" });

      expect(result.instruments).toHaveLength(1);
      expect(result.instruments[0].id).toBe("inst-1");
      expect(result.nextPageToken).toBe("page-2");
      expect(result.totalCount).toBe(42);
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/ListInstruments",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("returns null nextPageToken when empty", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListInstrumentsResponseSchema,
          create(ListInstrumentsResponseSchema, {
            instruments: [],
            nextPageToken: "",
            totalCount: 0,
          })
        )
      );

      const result = await listInstruments();

      expect(result.instruments).toHaveLength(0);
      expect(result.nextPageToken).toBeNull();
      expect(result.totalCount).toBe(0);
    });
  });

  describe("getJob", () => {
    it("sends job id and returns status and errors", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          GetJobResponseSchema,
          create(GetJobResponseSchema, {
            status: JobStatus.SUCCESS,
            validationErrors: [],
            identificationErrors: [],
          })
        )
      );

      const result = await getJob("job-123");

      expect(result.status).toBe(JobStatus.SUCCESS);
      expect(result.validationErrors).toEqual([]);
      expect(result.identificationErrors).toEqual([]);
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/GetJob",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("returns validation and identification errors when failed", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          GetJobResponseSchema,
          create(GetJobResponseSchema, {
            status: JobStatus.FAILED,
            validationErrors: [{ rowIndex: 0, field: "timestamp", message: "required" }],
            identificationErrors: [
              { rowIndex: 1, instrumentDescription: "FOO", message: "broker-description-only" },
            ],
          })
        )
      );

      const result = await getJob("job-456");

      expect(result.status).toBe(JobStatus.FAILED);
      expect(result.validationErrors).toHaveLength(1);
      expect(result.validationErrors[0]).toMatchObject({ rowIndex: 0, field: "timestamp", message: "required" });
      expect(result.identificationErrors).toHaveLength(1);
      expect(result.identificationErrors[0].instrumentDescription).toBe("FOO");
    });
  });
  describe("listResidualBalances", () => {
    it("maps balances and converts timestamps to Dates", async () => {
      const oldest = new Date("2026-03-01T12:00:00Z");
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          ListResidualBalancesResponseSchema,
          create(ListResidualBalancesResponseSchema, {
            balances: [
              {
                accountType: AccountType.IMBALANCE,
                broker: Broker.FIDELITY,
                account: "X123",
                instrumentId: "inst-1",
                commodity: "USD",
                assetClass: AssetClass.CASH,
                txType: TxType.INCOME,
                balance: -1234.56,
                postingCount: 7,
                oldestTimestamp: timestampFromDate(oldest),
              },
            ],
          })
        )
      );

      const balances = await listResidualBalances();

      expect(balances).toHaveLength(1);
      expect(balances[0]).toMatchObject({
        accountType: AccountType.IMBALANCE,
        broker: Broker.FIDELITY,
        account: "X123",
        commodity: "USD",
        assetClass: AssetClass.CASH,
        txType: TxType.INCOME,
        balance: -1234.56,
        postingCount: 7,
      });
      expect(balances[0].oldestTimestamp?.getTime()).toBe(oldest.getTime());
      // A row with no posting on the outstanding side stays undated rather than
      // acquiring the epoch.
      expect(balances[0].newestTimestamp).toBeUndefined();
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/ListResidualBalances",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });

    it("returns an empty list when nothing is outstanding", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(ListResidualBalancesResponseSchema, create(ListResidualBalancesResponseSchema, {}))
      );
      expect(await listResidualBalances()).toEqual([]);
    });
  });

  describe("countResidualBalances", () => {
    it("returns both headline counts", async () => {
      mockUnaryFetch.mockResolvedValue(
        toBinary(
          CountResidualBalancesResponseSchema,
          create(CountResidualBalancesResponseSchema, { imbalanceCount: 3, staleTransferCount: 1 })
        )
      );

      expect(await countResidualBalances()).toEqual({ imbalanceCount: 3, staleTransferCount: 1 });
      expect(mockUnaryFetch).toHaveBeenCalledWith(
        expect.any(String),
        "portfoliodb.api.v1.ApiService/CountResidualBalances",
        expect.any(Uint8Array),
        { credentials: "include" }
      );
    });
  });
});
