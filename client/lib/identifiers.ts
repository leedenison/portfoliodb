/** Identifier helpers shared by the import parsers. */

import { IdentifierTypeSchema } from "@/gen/type/v1/type_pb";

/** Valid identifier type names from the proto IdentifierType enum (excluding UNSPECIFIED). */
export const VALID_IDENTIFIER_TYPES = new Set(
  IdentifierTypeSchema.values
    .filter((v) => v.number !== 0)
    .map((v) => v.name),
);
