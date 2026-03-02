/**
 * Portfolio API client using generated protobuf bindings.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import {
  CreatePortfolioRequestSchema,
  CreatePortfolioResponseSchema,
  DeletePortfolioRequestSchema,
  ListPortfoliosRequestSchema,
  ListPortfoliosResponseSchema,
  UpdatePortfolioRequestSchema,
  UpdatePortfolioResponseSchema,
} from "@/gen/api/v1/api_pb";
import type { Portfolio as GenPortfolio } from "@/gen/api/v1/api_pb";
import { unaryFetch } from "./grpc-web";

const PAGE_SIZE = 30;
const ApiServicePrefix = "portfoliodb.api.v1.ApiService/";

function getBaseUrl(): string {
  if (typeof window === "undefined") return "http://localhost:8080";
  return (process.env.NEXT_PUBLIC_GRPC_WEB_BASE ?? window.location.origin).replace(/\/$/, "");
}

/** UI-friendly portfolio with createdAt as Date. */
export interface Portfolio {
  id: string;
  name: string;
  createdAt?: Date;
}

export interface ListPortfoliosResult {
  portfolios: Portfolio[];
  nextPageToken: string | null;
}

function toPortfolio(p: GenPortfolio): Portfolio {
  return {
    id: p.id,
    name: p.name,
    createdAt: p.createdAt ? timestampDate(p.createdAt) : undefined,
  };
}

export async function listPortfolios(
  pageToken?: string | null
): Promise<ListPortfoliosResult> {
  const base = getBaseUrl();
  const req = create(ListPortfoliosRequestSchema, {
    pageSize: PAGE_SIZE,
    pageToken: pageToken ?? "",
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListPortfolios", toBinary(ListPortfoliosRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListPortfoliosResponseSchema, resBytes);
  return {
    portfolios: res.portfolios.map(toPortfolio),
    nextPageToken: res.nextPageToken || null,
  };
}

export async function createPortfolio(name: string): Promise<Portfolio> {
  const base = getBaseUrl();
  const req = create(CreatePortfolioRequestSchema, { name });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "CreatePortfolio", toBinary(CreatePortfolioRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(CreatePortfolioResponseSchema, resBytes);
  return toPortfolio(res.portfolio!);
}

export async function updatePortfolio(id: string, name: string): Promise<Portfolio> {
  const base = getBaseUrl();
  const req = create(UpdatePortfolioRequestSchema, { portfolioId: id, name });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdatePortfolio", toBinary(UpdatePortfolioRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(UpdatePortfolioResponseSchema, resBytes);
  return toPortfolio(res.portfolio!);
}

export async function deletePortfolio(id: string): Promise<void> {
  const base = getBaseUrl();
  const req = create(DeletePortfolioRequestSchema, { portfolioId: id });
  await unaryFetch(base, ApiServicePrefix + "DeletePortfolio", toBinary(DeletePortfolioRequestSchema, req), {
    credentials: "include",
  });
}
