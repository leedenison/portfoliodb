/**
 * Ingestion API client for bulk and single transaction uploads.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import {
  UpsertTxsResponseSchema,
  UpsertTxsRequestSchema,
} from "@/gen/ingestion/v1/ingestion_pb";
import type { TxWindowSchema } from "@/gen/archive/v1/txs_pb";
import type { UpsertTxsResponse } from "@/gen/ingestion/v1/ingestion_pb";
import { unaryFetch } from "./grpc-web";

const IngestionServicePrefix = "portfoliodb.ingestion.v1.IngestionService/";

function getBaseUrl(): string {
  if (typeof window === "undefined") return "http://localhost:8080";
  return (process.env.NEXT_PUBLIC_GRPC_WEB_BASE ?? window.location.origin).replace(/\/$/, "");
}

/** Parameters for bulk transaction upload. */
export interface UpsertTxsParams {
  /**
   * The replacement scope and the postings inside it, in the archive schema.
   * The window states its own broker, source and half-open period, so an upload
   * describes itself. See docs/spec/archive-format.md.
   */
  window: MessageInitShape<typeof TxWindowSchema>;
  filename?: string;
}

export async function upsertTxs(params: UpsertTxsParams): Promise<UpsertTxsResponse> {
  const base = getBaseUrl();
  const req = create(UpsertTxsRequestSchema, {
    window: params.window,
    filename: params.filename ?? "",
  });
  const resBytes = await unaryFetch(
    base,
    IngestionServicePrefix + "UpsertTxs",
    toBinary(UpsertTxsRequestSchema, req),
    { credentials: "include" }
  );
  return fromBinary(UpsertTxsResponseSchema, resBytes);
}
