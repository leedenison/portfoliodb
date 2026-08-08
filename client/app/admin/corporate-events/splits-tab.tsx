"use client";

import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import { exportSystemArchive } from "@/lib/portfolio-api";
import { ArchivePart } from "@/gen/archive/v1/common_pb";

interface SplitDisplay {
  identifierValue: string;
  identifierDomain: string;
  exDate: string;
  splitFrom: string;
  splitTo: string;
}

export function SplitsTab() {
  // The export is a streaming generator, so the queryFn drains it and flattens
  // the groups back out. This tab lists splits; the dividends the same file
  // carries have a tab of their own that is not built yet.
  //
  // Only the corporate event part is asked for: this is a read of what the
  // instance holds, not a rebuild. Producing a file is done from /admin/archive.
  const {
    data: splits = [],
    isPending: loading,
    error: loadError,
  } = useAuthedQuery<SplitDisplay[]>({
    queryKey: qk.corporateEventSplits(),
    queryFn: async () => {
      const rows: SplitDisplay[] = [];
      for await (const item of exportSystemArchive([ArchivePart.CORPORATE_EVENTS])) {
        if (item.item.case !== "corporateEventGroup") continue;
        const group = item.item.value;
        for (const event of group.events) {
          if (event.event.case !== "split") continue;
          rows.push({
            identifierValue: group.instrument?.value ?? "",
            identifierDomain: group.instrument?.domain ?? "",
            exDate: event.event.value.exDate,
            splitFrom: event.event.value.splitFrom,
            splitTo: event.event.value.splitTo,
          });
        }
      }
      rows.sort((a, b) => b.exDate.localeCompare(a.exDate));
      return rows;
    },
  });

  const error = loadError ? errorMessage(loadError, "Failed to load splits") : null;

  return (
    <div className="mt-4 space-y-4">
      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      {loading ? (
        <p className="mt-4 text-text-muted">Loading splits...</p>
      ) : splits.length === 0 ? (
        <p className="mt-4 text-text-muted">No stock splits.</p>
      ) : (
        <table className="mt-4 w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border text-text-muted">
              <th className="py-2 pr-4 font-medium">Instrument</th>
              <th className="py-2 pr-4 font-medium">Ex Date</th>
              <th className="py-2 pr-4 font-medium">From</th>
              <th className="py-2 pr-4 font-medium">To</th>
            </tr>
          </thead>
          <tbody>
            {splits.map((s) => (
              <tr key={`${s.identifierValue}-${s.exDate}`} className="border-b border-border">
                <td className="py-2 pr-4 font-mono text-text-primary">
                  {s.identifierDomain ? `${s.identifierDomain}:` : ""}
                  {s.identifierValue}
                </td>
                <td className="py-2 pr-4 text-text-muted">{s.exDate}</td>
                <td className="py-2 pr-4 text-text-muted">{s.splitFrom}</td>
                <td className="py-2 pr-4 text-text-muted">{s.splitTo}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
