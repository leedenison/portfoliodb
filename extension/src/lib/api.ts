/**
 * PortfolioDB API clients for the service worker.
 *
 * Reuses the client's gRPC-Web transport and generated message types rather than
 * reimplementing the wire format. Two things differ from the browser app:
 *
 * - Credentials are omitted and the session travels as `Authorization: Bearer`.
 *   A service worker request to the PortfolioDB origin is cross-site, so the
 *   SameSite session cookie is not attached; the auth interceptor accepts a
 *   bearer token as an alternative.
 * - The base URL is the configured origin, not window.location, since a service
 *   worker has no page origin.
 */

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import type { DescMessage, MessageInitShape, MessageShape } from "@bufbuild/protobuf";
import type { GetSessionResponse } from "@/gen/auth/v1/auth_pb";
import { GetSessionRequestSchema, GetSessionResponseSchema } from "@/gen/auth/v1/auth_pb";
import type { GetJobResponse, ListTxsResponse } from "@/gen/api/v1/api_pb";
import {
  GetJobRequestSchema,
  GetJobResponseSchema,
  ListTxsRequestSchema,
  ListTxsResponseSchema,
} from "@/gen/api/v1/api_pb";
import type { UpsertTxsResponse } from "@/gen/ingestion/v1/ingestion_pb";
import { UpsertTxsResponseSchema, UpsertTxsRequestSchema } from "@/gen/ingestion/v1/ingestion_pb";
import { GetSessionServiceMethod, unaryFetch } from "@/lib/grpc-web";

const API_SERVICE = "portfoliodb.api.v1.ApiService/";
const INGESTION_SERVICE = "portfoliodb.ingestion.v1.IngestionService/";

/** Sends one authenticated unary call and decodes the response. */
async function call<Req extends DescMessage, Res extends DescMessage>(
  origin: string,
  sessionId: string,
  method: string,
  reqSchema: Req,
  req: MessageInitShape<Req>,
  resSchema: Res
): Promise<MessageShape<Res>> {
  const bytes = await unaryFetch(origin, method, toBinary(reqSchema, create(reqSchema, req)), {
    credentials: "omit",
    headers: { Authorization: `Bearer ${sessionId}` },
  });
  return fromBinary(resSchema, bytes);
}

/**
 * Returns the signed-in user for the stored session. Used to confirm the bearer
 * token works from the service worker, which is a different code path from the
 * cookie-authenticated call the bootstrap makes.
 */
export function getSession(origin: string, sessionId: string): Promise<GetSessionResponse> {
  return call(
    origin,
    sessionId,
    GetSessionServiceMethod,
    GetSessionRequestSchema,
    {},
    GetSessionResponseSchema
  );
}

export function listTxs(
  origin: string,
  sessionId: string,
  req: MessageInitShape<typeof ListTxsRequestSchema>
): Promise<ListTxsResponse> {
  return call(origin, sessionId, `${API_SERVICE}ListTxs`, ListTxsRequestSchema, req, ListTxsResponseSchema);
}

export function upsertTxs(
  origin: string,
  sessionId: string,
  req: MessageInitShape<typeof UpsertTxsRequestSchema>
): Promise<UpsertTxsResponse> {
  return call(
    origin,
    sessionId,
    `${INGESTION_SERVICE}UpsertTxs`,
    UpsertTxsRequestSchema,
    req,
    UpsertTxsResponseSchema
  );
}

export function getJob(origin: string, sessionId: string, jobId: string): Promise<GetJobResponse> {
  return call(origin, sessionId, `${API_SERVICE}GetJob`, GetJobRequestSchema, { jobId }, GetJobResponseSchema);
}
