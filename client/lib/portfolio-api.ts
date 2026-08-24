/**
 * Portfolio API client using generated protobuf bindings.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampDate, timestampFromDate } from "@bufbuild/protobuf/wkt";
import { DateSchema } from "@/gen/google/type/date_pb";
import type { Date as ProtoDate } from "@/gen/google/type/date_pb";
import { GetPortfolioValuationRequestSchema, GetPortfolioValuationResponseSchema, CreatePortfolioRequestSchema, CreatePortfolioResponseSchema, DeletePortfolioRequestSchema, GetHoldingsRequestSchema, GetHoldingsResponseSchema, GetJobRequestSchema, GetJobResponseSchema, GetPortfolioRequestSchema, GetPortfolioResponseSchema, GetPortfolioFiltersRequestSchema, GetPortfolioFiltersResponseSchema, ListBrokersAndAccountsRequestSchema, ListBrokersAndAccountsResponseSchema, ListCandidatePluginsRequestSchema, ListCandidatePluginsResponseSchema, ListIdentifierPluginsRequestSchema, ListIdentifierPluginsResponseSchema, ListPriceFetchBlocksRequestSchema, ListPriceFetchBlocksResponseSchema, DeletePriceFetchBlockRequestSchema, ListPricesRequestSchema, ListPricesResponseSchema, ExportSystemArchiveRequestSchema, ExportSystemArchiveResponseSchema, ImportSystemArchiveRequestSchema, ImportSystemArchiveResponseSchema, ExportUserArchiveRequestSchema, ExportUserArchiveResponseSchema, ImportUserArchiveRequestSchema, ImportUserArchiveResponseSchema, ListPricePluginsRequestSchema, ListPricePluginsResponseSchema, ListInstrumentsRequestSchema, ListInstrumentsResponseSchema, ListJobsRequestSchema, ListJobsResponseSchema, ListPortfoliosRequestSchema, ListPortfoliosResponseSchema, ListTxsRequestSchema, ListTxsResponseSchema, SetPortfolioFiltersRequestSchema, UpdateCandidatePluginRequestSchema, UpdateCandidatePluginResponseSchema, UpdateIdentifierPluginRequestSchema, UpdateIdentifierPluginResponseSchema, UpdatePricePluginRequestSchema, UpdatePricePluginResponseSchema, UpdatePortfolioRequestSchema, UpdatePortfolioResponseSchema, ReorderPluginsRequestSchema, CreateHoldingDeclarationRequestSchema, CreateHoldingDeclarationResponseSchema, UpdateHoldingDeclarationRequestSchema, UpdateHoldingDeclarationResponseSchema, DeleteHoldingDeclarationRequestSchema, ListHoldingDeclarationsRequestSchema, ListHoldingDeclarationsResponseSchema, ListWorkersRequestSchema, ListWorkersResponseSchema, GetDisplayCurrencyRequestSchema, GetDisplayCurrencyResponseSchema, SetDisplayCurrencyRequestSchema, SetDisplayCurrencyResponseSchema, ListInflationIndicesRequestSchema, ListInflationIndicesResponseSchema, ListInflationPluginsRequestSchema, ListInflationPluginsResponseSchema, UpdateInflationPluginRequestSchema, UpdateInflationPluginResponseSchema, TriggerInflationFetchRequestSchema, TriggerPriceFetchRequestSchema, CountUnhandledCorporateEventsRequestSchema, CountUnhandledCorporateEventsResponseSchema, ListUnhandledCorporateEventsRequestSchema, ListUnhandledCorporateEventsResponseSchema, ResolveUnhandledCorporateEventRequestSchema, ListResidualBalancesRequestSchema, ListResidualBalancesResponseSchema, CountResidualBalancesRequestSchema, CountResidualBalancesResponseSchema, JobStatus, WorkerState } from "@/gen/api/v1/api_pb";
import { AccountType, AssetClass } from "@/gen/type/v1/type_pb";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import type { SystemArchive, UserArchive } from "@/gen/archive/v1/archive_pb";
import type { CandidatePluginConfig, EODPriceProto, ExportSystemArchiveResponse, ExportUserArchiveResponse, InflationIndexProto, InflationPluginConfig, Holding, HoldingDeclaration, IdentificationError, IdentifierPluginConfig, Instrument, PriceFetchBlock, PricePluginConfig, Portfolio as GenPortfolio, PortfolioFilterProto, PortfolioTx, ResidualBalance as ResidualBalanceProto, ValidationError, Worker as WorkerProto } from "@/gen/api/v1/api_pb";
import type { Broker } from "@/gen/type/v1/type_pb";
import { streamingFetch, unaryFetch } from "./grpc-web";

const PAGE_SIZE = 30;
const ApiServicePrefix = "portfoliodb.api.v1.ApiService/";

/** Convert a "YYYY-MM-DD" string to a google.type.Date proto. Returns undefined if empty. */
function strToProtoDate(s: string | undefined): ProtoDate | undefined {
  if (!s) return undefined;
  const [y, m, d] = s.split("-").map(Number);
  return create(DateSchema, { year: y, month: m, day: d });
}

/** Convert a google.type.Date proto to a "YYYY-MM-DD" string. */
export function protoDateToStr(d: ProtoDate | undefined): string {
  if (!d || !d.year) return "";
  return `${String(d.year).padStart(4, "0")}-${String(d.month).padStart(2, "0")}-${String(d.day).padStart(2, "0")}`;
}

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
  /** Exclusive: the first instant NOT wanted. */
  periodBefore?: Date | null;
  pageToken?: string | null;
  /** Restrict to one broker. Omit for all brokers. */
  broker?: Broker;
  /** Order newest first. Defaults to oldest first. */
  descending?: boolean;
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
    periodBefore:
      params?.periodBefore != null ? timestampFromDate(params.periodBefore) : undefined,
    pageSize: PAGE_SIZE,
    pageToken: params?.pageToken ?? "",
    broker: params?.broker,
    descending: params?.descending ?? false,
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

export interface BrokerAccounts {
  broker: string;
  accounts: string[];
}

export async function listBrokersAndAccounts(): Promise<BrokerAccounts[]> {
  const base = getBaseUrl();
  const req = create(ListBrokersAndAccountsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListBrokersAndAccounts", toBinary(ListBrokersAndAccountsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListBrokersAndAccountsResponseSchema, resBytes);
  return (res.brokers ?? []).map((b) => ({ broker: b.broker, accounts: [...b.accounts] }));
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

/** One archive part as applied by one job. */
export interface JobPartResult {
  part: ArchivePart;
  status: JobStatus;
  totalCount: number;
  processedCount: number;
  validationErrors: ValidationError[];
  message: string;
}

/** Result of GetJob for ingestion job status. */
export interface GetJobResult {
  status: JobStatus;
  validationErrors: ValidationError[];
  identificationErrors: IdentificationError[];
  totalCount: number;
  processedCount: number;
  /** One row per part the archive carried; empty for any other kind of job. */
  parts: JobPartResult[];
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
    parts: res.parts.map((p) => ({
      part: p.part,
      status: p.status,
      totalCount: p.totalCount,
      processedCount: p.processedCount,
      validationErrors: p.validationErrors,
      message: p.message,
    })),
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

/** List candidate plugin configs (admin only). */
export async function listCandidatePlugins(): Promise<CandidatePluginConfig[]> {
  const base = getBaseUrl();
  const req = create(ListCandidatePluginsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListCandidatePlugins", toBinary(ListCandidatePluginsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListCandidatePluginsResponseSchema, resBytes);
  return res.plugins;
}

/** Update candidate plugin (admin only). Pass only fields to update. */
export async function updateCandidatePlugin(
  pluginId: string,
  opts: { enabled?: boolean; precedence?: number; configJson?: string }
): Promise<CandidatePluginConfig> {
  const base = getBaseUrl();
  const reqMsg = create(UpdateCandidatePluginRequestSchema, {
    pluginId,
    ...(opts.enabled !== undefined && { enabled: opts.enabled }),
    ...(opts.precedence !== undefined && { precedence: opts.precedence }),
    ...(opts.configJson !== undefined && { configJson: opts.configJson }),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdateCandidatePlugin", toBinary(UpdateCandidatePluginRequestSchema, reqMsg), {
    credentials: "include",
  });
  const res = fromBinary(UpdateCandidatePluginResponseSchema, resBytes);
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

/** Reorder plugins within a category (admin only). pluginIds ordered highest precedence first. */
export async function reorderPlugins(category: string, pluginIds: string[]): Promise<void> {
  const base = getBaseUrl();
  const reqMsg = create(ReorderPluginsRequestSchema, { category, pluginIds });
  await unaryFetch(base, ApiServicePrefix + "ReorderPlugins", toBinary(ReorderPluginsRequestSchema, reqMsg), {
    credentials: "include",
  });
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
/** Lifts a block on one currency line, which says nothing about the security's others. */
export async function deletePriceFetchBlock(listingId: string, pluginId: string): Promise<void> {
  const base = getBaseUrl();
  const req = create(DeletePriceFetchBlockRequestSchema, { listingId, pluginId });
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
  /** Exclusive: the first day NOT wanted. See lib/dates.ts. */
  dateBefore?: string;
  dataProvider?: string;
  pageToken?: string | null;
}): Promise<ListPricesResult> {
  const base = getBaseUrl();
  const req = create(ListPricesRequestSchema, {
    search: params?.search ?? "",
    dateFrom: strToProtoDate(params?.dateFrom),
    dateBefore: strToProtoDate(params?.dateBefore),
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
  assetClasses?: AssetClass[];
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

/** Job summary for the uploads list page. */
export interface JobSummary {
  id: string;
  jobType: string;
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

export async function listJobs(pageToken?: string | null, jobType?: string): Promise<ListJobsResult> {
  const base = getBaseUrl();
  const req = create(ListJobsRequestSchema, {
    pageSize: PAGE_SIZE,
    pageToken: pageToken ?? "",
    jobType: jobType ?? "",
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
      jobType: j.jobType,
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


/**
 * Stream one whole system archive (admin only): the envelope, then for each
 * selected part a part_begin followed by that part's items. A part not asked
 * for is absent from the stream entirely; a part asked for is announced even
 * when it holds nothing.
 */
export async function* exportSystemArchive(parts: ArchivePart[]): AsyncGenerator<ExportSystemArchiveResponse> {
  const base = getBaseUrl();
  const req = create(ExportSystemArchiveRequestSchema, { parts });
  for await (const bytes of streamingFetch(base, ApiServicePrefix + "ExportSystemArchive", toBinary(ExportSystemArchiveRequestSchema, req), { credentials: "include" })) {
    yield fromBinary(ExportSystemArchiveResponseSchema, bytes);
  }
}

/**
 * Queue one whole system archive for import (admin only), returning the job to
 * poll. The parts are applied server-side, so the caller does not have to stay
 * on the page for the import to finish.
 *
 * The archive is the file's own document, forwarded rather than rebuilt, so the
 * server sees the format version and kind the file declared.
 */
export async function importSystemArchive(archive: SystemArchive, filename: string): Promise<string> {
  const base = getBaseUrl();
  const req = create(ImportSystemArchiveRequestSchema, { archive, filename });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ImportSystemArchive", toBinary(ImportSystemArchiveRequestSchema, req), {
    credentials: "include",
  });
  return fromBinary(ImportSystemArchiveResponseSchema, resBytes).jobId;
}

/**
 * Stream the signed-in user's own archive, in the same shape as the system
 * export. A part whose unit is a setting rather than a row arrives as one
 * whole-part message after its part_begin.
 *
 * The optional half-open period bounds the transaction part alone. The export
 * adheres strictly to it, so a group straddling a bound contributes only its
 * in-period legs and the exported group does not balance; the importer routes
 * the residual. Omitting it exports all of history, each window bounded by the
 * postings it carries.
 */
export async function* exportUserArchive(
  parts: ArchivePart[],
  period?: { from?: Date; before?: Date },
): AsyncGenerator<ExportUserArchiveResponse> {
  const base = getBaseUrl();
  const req = create(ExportUserArchiveRequestSchema, {
    parts,
    periodFrom: period?.from != null ? timestampFromDate(period.from) : undefined,
    periodBefore: period?.before != null ? timestampFromDate(period.before) : undefined,
  });
  for await (const bytes of streamingFetch(base, ApiServicePrefix + "ExportUserArchive", toBinary(ExportUserArchiveRequestSchema, req), { credentials: "include" })) {
    yield fromBinary(ExportUserArchiveResponseSchema, bytes);
  }
}

/**
 * Queue the signed-in user's own archive for import, returning the job to poll.
 * It restores into the caller's account: a user archive does not name its user.
 */
export async function importUserArchive(archive: UserArchive, filename: string): Promise<string> {
  const base = getBaseUrl();
  const req = create(ImportUserArchiveRequestSchema, { archive, filename });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ImportUserArchive", toBinary(ImportUserArchiveRequestSchema, req), {
    credentials: "include",
  });
  return fromBinary(ImportUserArchiveResponseSchema, resBytes).jobId;
}

/** A single day's portfolio value point. */
export interface ValuationPointUI {
  date: string;
  totalValue: number;
  unpricedInstruments: string[];
}

export interface GetPortfolioValuationResult {
  points: ValuationPointUI[];
}

export async function getPortfolioValuation(params: {
  portfolioId?: string;
  dateFrom: string;
  /** Exclusive: the first day NOT valued. See lib/dates.ts. */
  dateBefore: string;
  displayCurrency?: string;
}): Promise<GetPortfolioValuationResult> {
  const base = getBaseUrl();
  const req = create(GetPortfolioValuationRequestSchema, {
    portfolioId: params.portfolioId ?? "",
    dateFrom: strToProtoDate(params.dateFrom),
    dateBefore: strToProtoDate(params.dateBefore),
    displayCurrency: params.displayCurrency ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    ApiServicePrefix + "GetPortfolioValuation",
    toBinary(GetPortfolioValuationRequestSchema, req),
    { credentials: "include" }
  );
  const res = fromBinary(GetPortfolioValuationResponseSchema, resBytes);
  return {
    points: res.points.map((p) => ({
      date: p.date,
      totalValue: p.totalValue,
      unpricedInstruments: [...p.unpricedInstruments],
    })),
  };
}

// Holding declarations

export async function listHoldingDeclarations(): Promise<HoldingDeclaration[]> {
  const base = getBaseUrl();
  const req = create(ListHoldingDeclarationsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListHoldingDeclarations", toBinary(ListHoldingDeclarationsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListHoldingDeclarationsResponseSchema, resBytes);
  return res.declarations;
}

export async function createHoldingDeclaration(params: {
  broker: string;
  account: string;
  instrumentId: string;
  declaredQty: string;
  asOfDate: string;
  shareCountBasis: string;
  /**
   * Which currency line is being declared. Omitted where the caller has not
   * picked one, which the server settles to the security's sole line and to no
   * line where it has several.
   */
  listingId?: string;
}): Promise<HoldingDeclaration> {
  const base = getBaseUrl();
  const req = create(CreateHoldingDeclarationRequestSchema, {
    broker: params.broker,
    account: params.account,
    instrumentId: params.instrumentId,
    declaredQty: params.declaredQty,
    asOfDate: strToProtoDate(params.asOfDate),
    shareCountBasis: strToProtoDate(params.shareCountBasis),
    listingId: params.listingId ?? "",
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "CreateHoldingDeclaration", toBinary(CreateHoldingDeclarationRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(CreateHoldingDeclarationResponseSchema, resBytes);
  return res.declaration!;
}

export async function updateHoldingDeclaration(params: {
  id: string;
  declaredQty: string;
  asOfDate: string;
  shareCountBasis: string;
}): Promise<HoldingDeclaration> {
  const base = getBaseUrl();
  const req = create(UpdateHoldingDeclarationRequestSchema, {
    id: params.id,
    declaredQty: params.declaredQty,
    asOfDate: strToProtoDate(params.asOfDate),
    shareCountBasis: strToProtoDate(params.shareCountBasis),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdateHoldingDeclaration", toBinary(UpdateHoldingDeclarationRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(UpdateHoldingDeclarationResponseSchema, resBytes);
  return res.declaration!;
}

export async function deleteHoldingDeclaration(id: string): Promise<void> {
  const base = getBaseUrl();
  const req = create(DeleteHoldingDeclarationRequestSchema, { id });
  await unaryFetch(base, ApiServicePrefix + "DeleteHoldingDeclaration", toBinary(DeleteHoldingDeclarationRequestSchema, req), {
    credentials: "include",
  });
}

// Display currency

export async function getDisplayCurrency(): Promise<string> {
  const base = getBaseUrl();
  const req = create(GetDisplayCurrencyRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "GetDisplayCurrency", toBinary(GetDisplayCurrencyRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(GetDisplayCurrencyResponseSchema, resBytes);
  return res.displayCurrency;
}

export async function setDisplayCurrency(displayCurrency: string): Promise<string> {
  const base = getBaseUrl();
  const req = create(SetDisplayCurrencyRequestSchema, { displayCurrency });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "SetDisplayCurrency", toBinary(SetDisplayCurrencyRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(SetDisplayCurrencyResponseSchema, resBytes);
  return res.displayCurrency;
}

export { WorkerState };

export interface WorkerRow {
  name: string;
  state: WorkerState;
  summary: string;
  queueDepth: number;
  /** Cycles completed since the service started. */
  cycles: number;
  updatedAt?: Date;
}

/** List background workers and their current state (admin only). */
export async function listWorkers(): Promise<WorkerRow[]> {
  const base = getBaseUrl();
  const req = create(ListWorkersRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListWorkers", toBinary(ListWorkersRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListWorkersResponseSchema, resBytes);
  return (res.workers ?? []).map((w: WorkerProto) => ({
    name: w.name,
    state: w.state,
    summary: w.summary,
    queueDepth: w.queueDepth,
    cycles: Number(w.cycles),
    updatedAt: w.updatedAt ? timestampDate(w.updatedAt) : undefined,
  }));
}

// ---------------------------------------------------------------------------
// Inflation indices
// ---------------------------------------------------------------------------

export interface ListInflationIndicesResult {
  indices: InflationIndexProto[];
  nextPageToken: string | null;
  totalCount: number;
}

export async function listInflationIndices(params?: {
  currency?: string;
  dateFrom?: string;
  /** Exclusive: the first month NOT wanted. See lib/dates.ts. */
  dateBefore?: string;
  pageToken?: string | null;
}): Promise<ListInflationIndicesResult> {
  const base = getBaseUrl();
  const req = create(ListInflationIndicesRequestSchema, {
    currency: params?.currency ?? "",
    dateFrom: strToProtoDate(params?.dateFrom),
    dateBefore: strToProtoDate(params?.dateBefore),
    pageSize: PAGE_SIZE,
    pageToken: params?.pageToken ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    ApiServicePrefix + "ListInflationIndices",
    toBinary(ListInflationIndicesRequestSchema, req),
    { credentials: "include" }
  );
  const res = fromBinary(ListInflationIndicesResponseSchema, resBytes);
  return {
    indices: res.indices,
    nextPageToken: res.nextPageToken || null,
    totalCount: res.totalCount,
  };
}

// ---------------------------------------------------------------------------
// Inflation plugins
// ---------------------------------------------------------------------------

export async function listInflationPlugins(): Promise<InflationPluginConfig[]> {
  const base = getBaseUrl();
  const req = create(ListInflationPluginsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListInflationPlugins", toBinary(ListInflationPluginsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListInflationPluginsResponseSchema, resBytes);
  return res.plugins;
}

export async function updateInflationPlugin(
  pluginId: string,
  opts: { enabled?: boolean; precedence?: number; configJson?: string }
): Promise<InflationPluginConfig> {
  const base = getBaseUrl();
  const reqMsg = create(UpdateInflationPluginRequestSchema, {
    pluginId,
    ...(opts.enabled !== undefined && { enabled: opts.enabled }),
    ...(opts.precedence !== undefined && { precedence: opts.precedence }),
    ...(opts.configJson !== undefined && { configJson: opts.configJson }),
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "UpdateInflationPlugin", toBinary(UpdateInflationPluginRequestSchema, reqMsg), {
    credentials: "include",
  });
  const res = fromBinary(UpdateInflationPluginResponseSchema, resBytes);
  return res.plugin!;
}

export async function triggerPriceFetch(): Promise<void> {
  const base = getBaseUrl();
  const req = create(TriggerPriceFetchRequestSchema, {});
  await unaryFetch(base, ApiServicePrefix + "TriggerPriceFetch", toBinary(TriggerPriceFetchRequestSchema, req), {
    credentials: "include",
  });
}

export async function triggerInflationFetch(): Promise<void> {
  const base = getBaseUrl();
  const req = create(TriggerInflationFetchRequestSchema, {});
  await unaryFetch(base, ApiServicePrefix + "TriggerInflationFetch", toBinary(TriggerInflationFetchRequestSchema, req), {
    credentials: "include",
  });
}

// --- Corporate Events ---

export interface UnhandledCorporateEvent {
  id: string;
  instrumentId: string;
  instrumentName: string;
  eventType: string;
  exDate: string;
  detail: string;
  data: string;
  resolved: boolean;
  createdAt: Date | null;
}

export interface ListUnhandledCorporateEventsResult {
  events: UnhandledCorporateEvent[];
  totalCount: number;
  nextPageToken: string | null;
}

export async function listUnhandledCorporateEvents(params?: {
  includeResolved?: boolean;
  pageSize?: number;
  pageToken?: string | null;
}): Promise<ListUnhandledCorporateEventsResult> {
  const base = getBaseUrl();
  const req = create(ListUnhandledCorporateEventsRequestSchema, {
    includeResolved: params?.includeResolved ?? false,
    pageSize: params?.pageSize ?? 50,
    pageToken: params?.pageToken ?? "",
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListUnhandledCorporateEvents", toBinary(ListUnhandledCorporateEventsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListUnhandledCorporateEventsResponseSchema, resBytes);
  return {
    events: res.events.map((e) => ({
      id: e.id,
      instrumentId: e.instrumentId,
      instrumentName: e.instrumentName,
      eventType: e.eventType,
      exDate: e.exDate,
      detail: e.detail,
      data: e.data,
      resolved: e.resolved,
      createdAt: e.createdAt ? timestampDate(e.createdAt) : null,
    })),
    totalCount: res.totalCount,
    nextPageToken: res.nextPageToken || null,
  };
}

export async function countUnhandledCorporateEvents(): Promise<number> {
  const base = getBaseUrl();
  const req = create(CountUnhandledCorporateEventsRequestSchema, {});
  const resBytes = await unaryFetch(base, ApiServicePrefix + "CountUnhandledCorporateEvents", toBinary(CountUnhandledCorporateEventsRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(CountUnhandledCorporateEventsResponseSchema, resBytes);
  return res.count;
}

export async function resolveUnhandledCorporateEvent(id: string): Promise<void> {
  const base = getBaseUrl();
  const req = create(ResolveUnhandledCorporateEventRequestSchema, { id });
  await unaryFetch(base, ApiServicePrefix + "ResolveUnhandledCorporateEvent", toBinary(ResolveUnhandledCorporateEventRequestSchema, req), {
    credentials: "include",
  });
}

// Residual balances

/**
 * The net value left in one non-asset account: what events of one tx type left
 * over in one broker account in one commodity. Balances are per commodity and are
 * never converted, so `assetClass` decides whether a row is money or a quantity.
 */
export interface ResidualBalance {
  accountType: AccountType;
  broker: number;
  account: string;
  instrumentId: string;
  commodity: string;
  assetClass: AssetClass;
  resolvedTxType: number;
  /** Decimal string: a signed sum over exact weights, so it stays exact. */
  balance: string;
  postingCount: number;
  /**
   * Oldest and newest postings contributing to the balance. For a transfer these
   * are not the age of a missing side: nothing pairs the two sides of a journal
   * until transfers are matched, so a settled transfer is reported like an open
   * one.
   */
  oldestTimestamp?: Date;
  newestTimestamp?: Date;
}

/**
 * List residual balances across all users (admin only). Omitting the period
 * bounds reports all of history.
 */
export async function listResidualBalances(params?: {
  periodFrom?: Date;
  periodBefore?: Date;
  accountType?: AccountType;
}): Promise<ResidualBalance[]> {
  const base = getBaseUrl();
  const req = create(ListResidualBalancesRequestSchema, {
    periodFrom: params?.periodFrom ? timestampFromDate(params.periodFrom) : undefined,
    periodBefore: params?.periodBefore ? timestampFromDate(params.periodBefore) : undefined,
    accountType: params?.accountType ?? AccountType.UNSPECIFIED,
  });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "ListResidualBalances", toBinary(ListResidualBalancesRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(ListResidualBalancesResponseSchema, resBytes);
  return (res.balances ?? []).map((b: ResidualBalanceProto) => ({
    accountType: b.accountType,
    broker: b.broker,
    account: b.account,
    instrumentId: b.instrumentId,
    commodity: b.commodity,
    assetClass: b.assetClass,
    resolvedTxType: b.resolvedTxType,
    balance: b.balance,
    postingCount: b.postingCount,
    oldestTimestamp: b.oldestTimestamp ? timestampDate(b.oldestTimestamp) : undefined,
    newestTimestamp: b.newestTimestamp ? timestampDate(b.newestTimestamp) : undefined,
  }));
}

export interface ResidualBalanceCounts {
  imbalanceCount: number;
  staleTransferCount: number;
}

/** Headline counts for the admin dashboard (admin only). */
export async function countResidualBalances(staleAfterDays?: number): Promise<ResidualBalanceCounts> {
  const base = getBaseUrl();
  const req = create(CountResidualBalancesRequestSchema, { staleAfterDays: staleAfterDays ?? 0 });
  const resBytes = await unaryFetch(base, ApiServicePrefix + "CountResidualBalances", toBinary(CountResidualBalancesRequestSchema, req), {
    credentials: "include",
  });
  const res = fromBinary(CountResidualBalancesResponseSchema, resBytes);
  return { imbalanceCount: res.imbalanceCount, staleTransferCount: res.staleTransferCount };
}
