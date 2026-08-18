/**
 * Ingestion API client for bulk and single transaction uploads.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
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
  /**
   * When the uploaded file was written, and so the point in market time its
   * identifiers are stated as of: a broker names an option under the symbol
   * current at its export, not under the one the contract wore on the trade
   * date. Omitted, the server takes the upload for the export and stamps its own
   * clock. See docs/spec/bitemporality.md.
   */
  exportedAt?: Date;
}

export async function upsertTxs(params: UpsertTxsParams): Promise<UpsertTxsResponse> {
  const base = getBaseUrl();
  const req = create(UpsertTxsRequestSchema, {
    window: params.window,
    filename: params.filename ?? "",
    exportedAt: params.exportedAt ? timestampFromDate(params.exportedAt) : undefined,
  });
  const resBytes = await unaryFetch(
    base,
    IngestionServicePrefix + "UpsertTxs",
    toBinary(UpsertTxsRequestSchema, req),
    { credentials: "include" }
  );
  return fromBinary(UpsertTxsResponseSchema, resBytes);
}
