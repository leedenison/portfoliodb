/**
 * Portfolio API client using generated protobuf bindings.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  ExportInstrumentsRequestSchema,
  ImportInstrumentsRequestSchema,
  ImportInstrumentsResponseSchema,
  InstrumentSchema,
  CreatePortfolioRequestSchema,
  CreatePortfolioResponseSchema,
  DeletePortfolioRequestSchema,
  GetHoldingsRequestSchema,
  GetHoldingsResponseSchema,
  GetJobRequestSchema,
  GetJobResponseSchema,
  GetPortfolioRequestSchema,
  GetPortfolioResponseSchema,
  GetPortfolioFiltersRequestSchema,
  GetPortfolioFiltersResponseSchema,
  ListDescriptionPluginsRequestSchema,
  ListDescriptionPluginsResponseSchema,
  ListIdentifierPluginsRequestSchema,
  ListIdentifierPluginsResponseSchema,
  ListPriceFetchBlocksRequestSchema,
  ListPriceFetchBlocksResponseSchema,
  DeletePriceFetchBlockRequestSchema,
  ListPricesRequestSchema,
  ListPricesResponseSchema,
  ListPricePluginsRequestSchema,
  ListPricePluginsResponseSchema,
  ListInstrumentsRequestSchema,
  ListInstrumentsResponseSchema,
  ListJobsRequestSchema,
  ListJobsResponseSchema,
  ListPortfoliosRequestSchema,
  ListPortfoliosResponseSchema,
  ListTelemetryCountersRequestSchema,
  ListTelemetryCountersResponseSchema,
  ListTxsRequestSchema,
  ListTxsResponseSchema,
  SetPortfolioFiltersRequestSchema,
  SetPortfolioFiltersResponseSchema,
  UpdateDescriptionPluginRequestSchema,
  UpdateDescriptionPluginResponseSchema,
  UpdateIdentifierPluginRequestSchema,
  UpdateIdentifierPluginResponseSchema,
  UpdatePricePluginRequestSchema,
  UpdatePricePluginResponseSchema,
  UpdatePortfolioRequestSchema,
  UpdatePortfolioResponseSchema,
  JobStatus,
} from "@/gen/api/v1/api_pb";
import type {
  DescriptionPluginConfig,
  EODPriceProto,
  Holding,
  IdentificationError,
  IdentifierPluginConfig,
  Instrument,
  PriceFetchBlock,
  PricePluginConfig,
  Portfolio as GenPortfolio,
  PortfolioFilterProto,
  PortfolioTx,
  ValidationError,
} from "@/gen/api/v1/api_pb";
import { streamingFetch, unaryFetch } from "./grpc-web";

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

/** Result of GetHoldings with asOf as Date for UI. */
export interface GetHoldingsResult {
  holdings: Holding[];
  asOf?: Date;
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

export async function getPortfolio(id: string): Promise<Portfolio> {
  const base = getBaseUrl();
  const req = create(GetPortfolioRequestSchema, { portfolioId: id });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "GetPortfolio", toBinary(GetPortfolioRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(GetPortfolioResponseSchema, resBytes);
  if (!res.portfolio) throw new Error("GetPortfolio: no portfolio in response");
  return toPortfolio(res.portfolio);
}

export interface GetHoldingsParams {
  portfolioId?: string;
  asOf?: Date | null;
}

export async function getHoldings(params?: GetHoldingsParams): Promise<GetHoldingsResult> {
  const base = getBaseUrl();
  const req = create(GetHoldingsRequestSchema, {
    portfolioId: params?.portfolioId ?? "",
    asOf: params?.asOf != null ? timestampFromDate(params.asOf) : undefined,
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "GetHoldings", toBinary(GetHoldingsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(GetHoldingsResponseSchema, resBytes);
  return {
    holdings: res.holdings,
    asOf: res.asOf ? timestampDate(res.asOf) : undefined,
  };
}

export interface ListTxsParams {
  portfolioId?: string;
  periodFrom?: Date | null;
  periodTo?: Date | null;
  pageToken?: string | null;
}

export interface ListTxsResult {
  txs: PortfolioTx[];
  nextPageToken: string | null;
}

export async function listTxs(params?: ListTxsParams): Promise<ListTxsResult> {
  const base = getBaseUrl();
  const req = create(ListTxsRequestSchema, {
    portfolioId: params?.portfolioId ?? "",
    periodFrom: params?.periodFrom != null ? timestampFromDate(params.periodFrom) : undefined,
    periodTo: params?.periodTo != null ? timestampFromDate(params.periodTo) : undefined,
    pageSize: PAGE_SIZE,
    pageToken: params?.pageToken ?? "",
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListTxs", toBinary(ListTxsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListTxsResponseSchema, resBytes);
  return {
    txs: res.txs,
    nextPageToken: res.nextPageToken || null,
  };
}

export interface PortfolioFilter {
  filterType: string;
  filterValue: string;
}

export async function getPortfolioFilters(portfolioId: string): Promise<PortfolioFilter[]> {
  const base = getBaseUrl();
  const req = create(GetPortfolioFiltersRequestSchema, { portfolioId });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "GetPortfolioFilters", toBinary(GetPortfolioFiltersRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(GetPortfolioFiltersResponseSchema, resBytes);
  return (res.filters ?? []).map((f: PortfolioFilterProto) => ({ filterType: f.filterType, filterValue: f.filterValue }));
}

export async function setPortfolioFilters(portfolioId: string, filters: PortfolioFilter[]): Promise<void> {
  const base = getBaseUrl();
  const req = create(SetPortfolioFiltersRequestSchema, {
    portfolioId,
    filters: filters.map((f) => ({ filterType: f.filterType, filterValue: f.filterValue })),
  });
  await unaryFetch(base, ApiServicePrefix + "SetPortfolioFilters", toBinary(SetPortfolioFiltersRequestSchema, req), {
    credentials: "include",
  });
}

/** Result of GetJob for ingestion job status. */
export interface GetJobResult {
  status: JobStatus;
  validationErrors: ValidationError[];
  identificationErrors: IdentificationError[];
  totalCount: number;
  processedCount: number;
}

export async function getJob(jobId: string): Promise<GetJobResult> {
  const base = getBaseUrl();
  const req = create(GetJobRequestSchema, { jobId });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "GetJob", toBinary(GetJobRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(GetJobResponseSchema, resBytes);
  return {
    status: res.status,
    validationErrors: res.validationErrors,
    identificationErrors: res.identificationErrors,
    totalCount: res.totalCount,
    processedCount: res.processedCount,
  };
}

/** List identifier plugin configs (admin only). */
export async function listIdentifierPlugins(): Promise<IdentifierPluginConfig[]> {
  const base = getBaseUrl();
  const req = create(ListIdentifierPluginsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListIdentifierPlugins", toBinary(ListIdentifierPluginsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListIdentifierPluginsResponseSchema, resBytes);
  return res.plugins;
}

/** Update identifier plugin (admin only). Pass only fields to update. */
export async function updateIdentifierPlugin(
  pluginId: string,
  opts: { enabled?: boolean; precedence?: number; configJson?: string }
): Promise<IdentifierPluginConfig> {
  const base = getBaseUrl();
  const reqMsg = create(UpdateIdentifierPluginRequestSchema, {
    pluginId,
    ...(opts.enabled !== undefined && { enabled: opts.enabled }),
    ...(opts.precedence !== undefined && { precedence: opts.precedence }),
    ...(opts.configJson !== undefined && { configJson: opts.configJson }),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdateIdentifierPlugin", toBinary(UpdateIdentifierPluginRequestSchema, reqMsg), {
    credentials: "include",
  });
  const res = fromBinary(UpdateIdentifierPluginResponseSchema, resBytes);
  return res.plugin!;
}

/** List description plugin configs (admin only). */
export async function listDescriptionPlugins(): Promise<DescriptionPluginConfig[]> {
  const base = getBaseUrl();
  const req = create(ListDescriptionPluginsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListDescriptionPlugins", toBinary(ListDescriptionPluginsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListDescriptionPluginsResponseSchema, resBytes);
  return res.plugins;
}

/** Update description plugin (admin only). Pass only fields to update. */
export async function updateDescriptionPlugin(
  pluginId: string,
  opts: { enabled?: boolean; precedence?: number; configJson?: string }
): Promise<DescriptionPluginConfig> {
  const base = getBaseUrl();
  const reqMsg = create(UpdateDescriptionPluginRequestSchema, {
    pluginId,
    ...(opts.enabled !== undefined && { enabled: opts.enabled }),
    ...(opts.precedence !== undefined && { precedence: opts.precedence }),
    ...(opts.configJson !== undefined && { configJson: opts.configJson }),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdateDescriptionPlugin", toBinary(UpdateDescriptionPluginRequestSchema, reqMsg), {
    credentials: "include",
  });
  const res = fromBinary(UpdateDescriptionPluginResponseSchema, resBytes);
  return res.plugin!;
}

/** List price plugin configs (admin only). */
export async function listPricePlugins(): Promise<PricePluginConfig[]> {
  const base = getBaseUrl();
  const req = create(ListPricePluginsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListPricePlugins", toBinary(ListPricePluginsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListPricePluginsResponseSchema, resBytes);
  return res.plugins;
}

/** Update price plugin (admin only). Pass only fields to update. */
export async function updatePricePlugin(
  pluginId: string,
  opts: { enabled?: boolean; precedence?: number; configJson?: string; maxHistoryDays?: number }
): Promise<PricePluginConfig> {
  const base = getBaseUrl();
  const reqMsg = create(UpdatePricePluginRequestSchema, {
    pluginId,
    ...(opts.enabled !== undefined && { enabled: opts.enabled }),
    ...(opts.precedence !== undefined && { precedence: opts.precedence }),
    ...(opts.configJson !== undefined && { configJson: opts.configJson }),
    ...(opts.maxHistoryDays !== undefined && { maxHistoryDays: opts.maxHistoryDays }),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdatePricePlugin", toBinary(UpdatePricePluginRequestSchema, reqMsg), {
    credentials: "include",
  });
  const res = fromBinary(UpdatePricePluginResponseSchema, resBytes);
  return res.plugin!;
}

/** List price fetch blocks (admin only). */
export async function listPriceFetchBlocks(): Promise<PriceFetchBlock[]> {
  const base = getBaseUrl();
  const req = create(ListPriceFetchBlocksRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListPriceFetchBlocks", toBinary(ListPriceFetchBlocksRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListPriceFetchBlocksResponseSchema, resBytes);
  return res.blocks;
}

/** Delete a price fetch block (admin only). */
export async function deletePriceFetchBlock(instrumentId: string, pluginId: string): Promise<void> {
  const base = getBaseUrl();
  const req = create(DeletePriceFetchBlockRequestSchema, { instrumentId, pluginId });
  await unaryFetch(base, ApiServicePrefix + "DeletePriceFetchBlock", toBinary(DeletePriceFetchBlockRequestSchema, req), {
    credentials: "include",
  });
}

/** Result of ListPrices for the admin prices page. */
export interface ListPricesResult {
  prices: EODPriceProto[];
  nextPageToken: string | null;
  totalCount: number;
}

export async function listPrices(params?: {
  search?: string;
  dateFrom?: string;
  dateTo?: string;
  dataProvider?: string;
  pageToken?: string | null;
}): Promise<ListPricesResult> {
  const base = getBaseUrl();
  const req = create(ListPricesRequestSchema, {
    search: params?.search ?? "",
    dateFrom: params?.dateFrom ?? "",
    dateTo: params?.dateTo ?? "",
    dataProvider: params?.dataProvider ?? "",
    pageSize: PAGE_SIZE,
    pageToken: params?.pageToken ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    ApiServicePrefix + "ListPrices",
    toBinary(ListPricesRequestSchema, req),
    { credentials: "include" }
  );
  const res = fromBinary(ListPricesResponseSchema, resBytes);
  return {
    prices: res.prices,
    nextPageToken: res.nextPageToken || null,
    totalCount: res.totalCount,
  };
}

/** Result of ListInstruments for the instruments page. */
export interface ListInstrumentsResult {
  instruments: Instrument[];
  nextPageToken: string | null;
  totalCount: number;
}

export async function listInstruments(params?: {
  search?: string;
  assetClasses?: string[];
  pageToken?: string | null;
}): Promise<ListInstrumentsResult> {
  const base = getBaseUrl();
  const req = create(ListInstrumentsRequestSchema, {
    search: params?.search ?? "",
    assetClasses: params?.assetClasses ?? [],
    pageSize: PAGE_SIZE,
    pageToken: params?.pageToken ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    ApiServicePrefix + "ListInstruments",
    toBinary(ListInstrumentsRequestSchema, req),
    { credentials: "include" }
  );
  const res = fromBinary(ListInstrumentsResponseSchema, resBytes);
  return {
    instruments: res.instruments,
    nextPageToken: res.nextPageToken || null,
    totalCount: res.totalCount,
  };
}

export interface TelemetryCounterRow {
  name: string;
  value: number;
}

/** List telemetry counters (admin only). Discovery from Redis. */
export async function listTelemetryCounters(): Promise<TelemetryCounterRow[]> {
  const base = getBaseUrl();
  const req = create(ListTelemetryCountersRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListTelemetryCounters", toBinary(ListTelemetryCountersRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListTelemetryCountersResponseSchema, resBytes);
  return (res.counters ?? []).map((c) => ({ name: c.name ?? "", value: Number(c.value ?? 0) }));
}

/** Job summary for the uploads list page. */
export interface JobSummary {
  id: string;
  filename: string;
  broker: string;
  status: JobStatus;
  createdAt?: Date;
  validationErrorCount: number;
  identificationErrorCount: number;
}

export interface ListJobsResult {
  jobs: JobSummary[];
  nextPageToken: string | null;
  totalCount: number;
}

export async function listJobs(pageToken?: string | null): Promise<ListJobsResult> {
  const base = getBaseUrl();
  const req = create(ListJobsRequestSchema, {
    pageSize: PAGE_SIZE,
    pageToken: pageToken ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    ApiServicePrefix + "ListJobs",
    toBinary(ListJobsRequestSchema, req),
    { credentials: "include" }
  );
  const res = fromBinary(ListJobsResponseSchema, resBytes);
  return {
    jobs: res.jobs.map((j) => ({
      id: j.id,
      filename: j.filename,
      broker: j.broker,
      status: j.status,
      createdAt: j.createdAt ? timestampDate(j.createdAt) : undefined,
      validationErrorCount: j.validationErrorCount,
      identificationErrorCount: j.identificationErrorCount,
    })),
    nextPageToken: res.nextPageToken || null,
    totalCount: res.totalCount,
  };
}

/** Stream all exported instruments (admin only). */
export async function* exportInstruments(params?: { exchange?: string }): AsyncGenerator<Instrument> {
  const base = getBaseUrl();
  const req = create(ExportInstrumentsRequestSchema, { exchange: params?.exchange ?? "" });
  for await (const bytes of streamingFetch(base, ApiServicePrefix + "ExportInstruments", toBinary(ExportInstrumentsRequestSchema, req), { credentials: "include" })) {
    yield fromBinary(InstrumentSchema, bytes);
  }
}

export interface ImportInstrumentsResult {
  ensuredCount: number;
  errors: Array<{ index: number; message: string }>;
}

/** Import (upsert) instruments (admin only). */
export async function importInstruments(instruments: Instrument[]): Promise<ImportInstrumentsResult> {
  const base = getBaseUrl();
  const req = create(ImportInstrumentsRequestSchema, { instruments });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ImportInstruments", toBinary(ImportInstrumentsRequestSchema, req), { credentials: "include" });
  const res = fromBinary(ImportInstrumentsResponseSchema, resBytes);
  return {
    ensuredCount: res.ensuredCount,
    errors: res.errors.map((e) => ({ index: e.index, message: e.message })),
  };
}
