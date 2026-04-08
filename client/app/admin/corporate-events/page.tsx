"use client";

import { useState } from "react";
import { ImportCorporateEventsModal } from "./import-modal";

export default function AdminCorporateEventsPage() {
  const [importOpen, setImportOpen] = useState(false);

  return (
    <div className="space-y-5">
      <h1 className="font-display text-xl font-bold text-text-primary">
        Corporate Events
      </h1>

      <p className="text-sm text-text-muted">
        Extract stock split events from a broker transaction file and import
        them into the corporate events table. Re-importing the same file is
        safe; the server upserts on (instrument, ex date).
      </p>

      <div>
        <button
          type="button"
          onClick={() => setImportOpen(true)}
          className="rounded-md border border-border bg-surface px-3 py-1.5 text-xs font-medium text-text-primary transition-colors hover:bg-primary-light/15"
        >
          Import splits from broker file
        </button>
      </div>

      <ImportCorporateEventsModal
        open={importOpen}
        onClose={() => setImportOpen(false)}
      />
    </div>
  );
}
