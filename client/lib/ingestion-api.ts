/**
 * Ingestion API client for bulk and single transaction uploads.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  UpsertTxsResponseSchema,
  UpsertTxsRequestSchema,
} from "@/gen/ingestion/v1/ingestion_pb";
import type { Tx } from "@/gen/api/v1/api_pb";
import type { Broker } from "@/gen/type/v1/type_pb";
import type { UpsertTxsResponse } from "@/gen/ingestion/v1/ingestion_pb";
import { unaryFetch } from "./grpc-web";

const IngestionServicePrefix = "portfoliodb.ingestion.v1.IngestionService/";

function getBaseUrl(): string {
  if (typeof window === "undefined") return "http://localhost:8080";
  return (process.env.NEXT_PUBLIC_GRPC_WEB_BASE ?? window.location.origin).replace(/\/$/, "");
}

/** Parameters for bulk transaction upload. */
export interface UpsertTxsParams {
  broker: Broker;
  source: string;
  periodFrom?: Timestamp;
  /** Exclusive: the first instant NOT replaced. */
  periodBefore?: Timestamp;
  txs: Tx[];
  filename?: string;
  /**
   * The share count the uploaded quantities and unit prices are denominated
   * in, as "YYYY-MM-DD". Omit for an as-traded source, which is every CSV
   * export the client handles. See docs/spec/bitemporality.md.
   */
  shareCountBasis?: string;
}

export async function upsertTxs(params: UpsertTxsParams): Promise<UpsertTxsResponse> {
  const base = getBaseUrl();
  const req = create(UpsertTxsRequestSchema, {
    broker: params.broker,
    source: params.source,
    periodFrom: params.periodFrom,
    periodBefore: params.periodBefore,
    txs: params.txs,
    filename: params.filename ?? "",
    shareCountBasis: params.shareCountBasis ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    IngestionServicePrefix + "UpsertTxs",
    toBinary(UpsertTxsRequestSchema, req),
    { credentials: "include" }
  );
  return fromBinary(UpsertTxsResponseSchema, resBytes);
}
