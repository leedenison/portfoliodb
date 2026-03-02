/**
 * Ingestion API client for bulk and single transaction uploads.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  IngestionResponseSchema,
  UpsertTxsRequestSchema,
} from "@/gen/ingestion/v1/ingestion_pb";
import type { Broker, Tx } from "@/gen/api/v1/api_pb";
import type { IngestionResponse } from "@/gen/ingestion/v1/ingestion_pb";
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
  periodTo?: Timestamp;
  txs: Tx[];
}

export async function upsertTxs(params: UpsertTxsParams): Promise<IngestionResponse> {
  const base = getBaseUrl();
  const req = create(UpsertTxsRequestSchema, {
    broker: params.broker,
    source: params.source,
    periodFrom: params.periodFrom,
    periodTo: params.periodTo,
    txs: params.txs,
  });
  const resBytes = await unaryFetch(
    base,
    IngestionServicePrefix + "UpsertTxs",
    toBinary(UpsertTxsRequestSchema, req),
    { credentials: "include" }
  );
  return fromBinary(IngestionResponseSchema, resBytes);
}
