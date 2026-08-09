import { create } from "@bufbuild/protobuf";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import { CorporateEventPartSchema } from "@/gen/archive/v1/corporate_events_pb";
import { FetchBlockPartSchema } from "@/gen/archive/v1/fetch_blocks_pb";
import { InflationPartSchema } from "@/gen/archive/v1/inflation_pb";
import { InstrumentPartSchema } from "@/gen/archive/v1/instruments_pb";
import { PricePartSchema } from "@/gen/archive/v1/prices_pb";
import { UnhandledEventPartSchema } from "@/gen/archive/v1/unhandled_events_pb";
import type { ExportSystemArchiveResponse } from "@/gen/api/v1/api_pb";
import type { SystemArchive } from "@/gen/archive/v1/archive_pb";

/**
 * Rebuild a system archive from its export stream.
 *
 * The stream is a document taken apart: the envelope, then for each selected
 * part a marker followed by that part's items. The marker is what makes a part
 * that is present and empty distinguishable from one that was never selected --
 * it creates the empty container, and an unselected part is never assigned at
 * all, so it stays absent from the serialised document.
 */
export function assembleSystemArchive(items: ExportSystemArchiveResponse[]): SystemArchive {
  const doc: Partial<SystemArchive> = {};
  for (const item of items) {
    switch (item.item.case) {
      case "envelope":
        doc.envelope = item.item.value;
        break;
      case "partBegin":
        switch (item.item.value.part) {
          case ArchivePart.INSTRUMENTS:
            doc.instruments ??= create(InstrumentPartSchema, {});
            break;
          case ArchivePart.PRICES:
            doc.prices ??= create(PricePartSchema, {});
            break;
          case ArchivePart.CORPORATE_EVENTS:
            doc.corporateEvents ??= create(CorporateEventPartSchema, {});
            break;
          case ArchivePart.INFLATION_INDICES:
            doc.inflationIndices ??= create(InflationPartSchema, {});
            break;
          case ArchivePart.FETCH_BLOCKS:
            doc.fetchBlocks ??= create(FetchBlockPartSchema, {});
            break;
          case ArchivePart.UNHANDLED_EVENTS:
            doc.unhandledEvents ??= create(UnhandledEventPartSchema, {});
            break;
        }
        break;
      case "instrument":
        doc.instruments?.instruments.push(item.item.value);
        break;
      case "priceGroup":
        doc.prices?.groups.push(item.item.value);
        break;
      case "corporateEventGroup":
        doc.corporateEvents?.groups.push(item.item.value);
        break;
      case "inflationGroup":
        doc.inflationIndices?.groups.push(item.item.value);
        break;
      case "fetchBlockGroup":
        doc.fetchBlocks?.groups.push(item.item.value);
        break;
      case "unhandledEventGroup":
        doc.unhandledEvents?.groups.push(item.item.value);
        break;
    }
  }
  if (!doc.envelope) {
    throw new Error("export stream sent no envelope");
  }
  return doc as SystemArchive;
}

/** How many rows each part of a document carries, for a preview. */
export function partCounts(archive: SystemArchive): { label: string; count: number }[] {
  const out: { label: string; count: number }[] = [];
  if (archive.instruments) {
    out.push({ label: "instruments", count: archive.instruments.instruments.length });
  }
  if (archive.prices) {
    const rows = archive.prices.groups.reduce((n, g) => n + g.rows.length, 0);
    out.push({ label: "prices", count: rows });
  }
  if (archive.corporateEvents) {
    const events = archive.corporateEvents.groups.reduce((n, g) => n + g.events.length, 0);
    out.push({ label: "corporate events", count: events });
  }
  if (archive.inflationIndices) {
    const rows = archive.inflationIndices.groups.reduce((n, g) => n + g.rows.length, 0);
    out.push({ label: "inflation index values", count: rows });
  }
  if (archive.fetchBlocks) {
    const blocks = archive.fetchBlocks.groups.reduce((n, g) => n + g.blocks.length, 0);
    out.push({ label: "fetch blocks", count: blocks });
  }
  if (archive.unhandledEvents) {
    const events = archive.unhandledEvents.groups.reduce((n, g) => n + g.events.length, 0);
    out.push({ label: "unhandled corporate events", count: events });
  }
  return out;
}
