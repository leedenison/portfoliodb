import { create, toBinary } from "@bufbuild/protobuf";
import {
  CreatePortfolioResponseSchema,
  GetJobResponseSchema,
  ListPortfoliosResponseSchema,
  UpdatePortfolioResponseSchema,
} from "@/gen/api/v1/api_pb";
import { JobStatus } from "@/gen/api/v1/api_pb";
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  listPortfolios,
  createPortfolio,
  updatePortfolio,
  deletePortfolio,
  getJob,
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
});
