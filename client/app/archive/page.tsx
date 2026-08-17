"use client";

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AppShell } from "@/app/components/app-shell";
import { ExportArchivePanel } from "@/app/components/archive/export-panel";
import { ImportArchivePanel } from "@/app/components/archive/import-panel";
import { ArchiveJobSection } from "@/app/components/archive/job-parts";
import { useAuth } from "@/contexts/auth-context";
import { useArchiveJob } from "@/hooks/use-archive-job";
import { qk } from "@/lib/query-keys";
import { exportUserArchive, importUserArchive } from "@/lib/portfolio-api";
import { marshalUser, unmarshalUser } from "@/lib/archive/codec";
import { assembleUserArchive, userPartCounts } from "@/lib/archive/assemble";
import { USER_ARCHIVE_PART_OPTIONS } from "@/lib/archive/parts";
import { ArchivePart } from "@/gen/archive/v1/common_pb";

const USER_ARCHIVE_JOB_TYPE = "user_archive";

const IMPORT_NOTE = (
  <>
    The parts are applied in order on the server, so an import finishes whether or not this page
    stays open. Importing holding declarations restates the ones the file names and leaves the
    rest alone -- a declaration missing from a file is not a declaration you deleted.
  </>
);

/**
 * Export and import the signed-in user's own archive.
 *
 * A separate page from /admin/archive, and a separate file: this one carries the
 * user's own data and no shared reference data at all. Restoring it into an
 * instance whose instruments are not loaded is supported and correct -- the
 * postings resolve through the normal identifier path, merely slower -- so
 * nothing here presents that as an error.
 */
export default function UserArchivePage() {
  const { state } = useAuth();
  const queryClient = useQueryClient();

  // What an import changes is spread across the user's own pages, so once it is
  // done everything it could have touched is dropped rather than guessing which
  // part landed. A restored declaration writes the pad posting that opens its
  // holding, so holdings go too.
  const onFinished = useCallback(() => {
    queryClient.invalidateQueries({ queryKey: qk.displayCurrency() });
    queryClient.invalidateQueries({ queryKey: qk.holdingDeclarations() });
    queryClient.invalidateQueries({ queryKey: qk.holdings() });
    queryClient.invalidateQueries({ queryKey: qk.txs() });
  }, [queryClient]);

  const { job, running, start } = useArchiveJob(USER_ARCHIVE_JOB_TYPE, onFinished);

  async function build(parts: ArchivePart[]): Promise<string> {
    const items = [];
    for await (const item of exportUserArchive(parts)) {
      items.push(item);
    }
    return marshalUser(assembleUserArchive(items));
  }

  return (
    <AppShell>
      <div data-testid="page-archive" className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && <p className="text-text-muted">Loading...</p>}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Your archive
            </h1>
            <p className="mt-3 text-text-muted">Sign in to export or import your data.</p>
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-2xl animate-fade-in space-y-5">
            <div>
              <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
                Your archive
              </h2>
              <p className="mt-1 text-sm text-text-muted">
                Export and import your own data. The file carries none of the shared reference data
                the instance is built from, which is a separate archive an administrator keeps.
              </p>
            </div>

            <ExportArchivePanel
              options={USER_ARCHIVE_PART_OPTIONS}
              build={build}
              filename="user-archive.json"
            />

            <ImportArchivePanel
              running={running}
              onStarted={start}
              parse={unmarshalUser}
              counts={userPartCounts}
              submit={importUserArchive}
              note={IMPORT_NOTE}
            />

            {job && <ArchiveJobSection job={job} running={running} />}
          </div>
        )}
      </div>
    </AppShell>
  );
}
