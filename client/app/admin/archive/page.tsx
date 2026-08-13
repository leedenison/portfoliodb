"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { qk } from "@/lib/query-keys";
import { exportSystemArchive, importSystemArchive } from "@/lib/portfolio-api";
import { marshalSystem, unmarshalSystem } from "@/lib/archive/codec";
import { assembleSystemArchive, systemPartCounts } from "@/lib/archive/assemble";
import { SYSTEM_ARCHIVE_PART_OPTIONS } from "@/lib/archive/parts";
import { ArchivePart } from "@/gen/archive/v1/common_pb";
import { ExportArchivePanel } from "@/app/components/archive/export-panel";
import { ImportArchivePanel } from "@/app/components/archive/import-panel";
import { ArchiveJobSection } from "@/app/components/archive/job-parts";
import { useArchiveJob } from "@/hooks/use-archive-job";

const SYSTEM_ARCHIVE_JOB_TYPE = "system_archive";

export default function ArchivePage() {
  const queryClient = useQueryClient();

  // What an import changes is spread across the admin section, so once it is
  // done everything it could have touched is dropped rather than guessing which
  // part landed.
  const onFinished = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: qk.instruments() });
    queryClient.invalidateQueries({ queryKey: qk.prices() });
    queryClient.invalidateQueries({ queryKey: qk.corporateEventSplits() });
  }, [queryClient]);

  const { job, running, start } = useArchiveJob(SYSTEM_ARCHIVE_JOB_TYPE, onFinished);

  async function build(parts: ArchivePart[]): Promise<string> {
    const items = [];
    for await (const item of exportSystemArchive(parts)) {
      items.push(item);
    }
    return marshalSystem(assembleSystemArchive(items));
  }

  return (
    <div className="space-y-5">
      <div>
        <h1 className="font-display text-xl font-bold text-text-primary">Archive</h1>
        <p className="mt-1 text-sm text-text-muted">
          Export and import the system archive: the shared reference data this instance is built
          from. A user&apos;s own data is not reachable from here.
        </p>
      </div>

      <ExportArchivePanel
        options={SYSTEM_ARCHIVE_PART_OPTIONS}
        build={build}
        filename="system-archive.json"
      />

      <ImportArchivePanel
        running={running}
        onStarted={start}
        parse={unmarshalSystem}
        counts={systemPartCounts}
        submit={importSystemArchive}
        note="The parts are applied in order on the server, so an import finishes whether or not this page stays open. To import one kind of data, upload an archive carrying only that part."
      />

      {job && <ArchiveJobSection job={job} running={running} />}
    </div>
  );
}
