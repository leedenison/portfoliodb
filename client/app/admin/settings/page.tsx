"use client";

import { useState } from "react";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import { listSettings, setSetting } from "@/lib/portfolio-api";
import type { Setting } from "@/gen/api/v1/api_pb";

// What each setting means. The server refuses a key it does not know, so this
// covers the same vocabulary; a key with no note still lists and still edits,
// which is what stops a newly seeded setting being invisible until somebody
// remembers to describe it.
const notes: Record<string, string> = {
  promotion_threshold:
    "How many users must supply the same broker mapping, with none of them supplying a conflicting one, " +
    "before it becomes this instance's. One promotes whatever a single user's file said, which is what a " +
    "single-user instance needs: the threshold says the file was not doctored or stale, never that the broker is right.",
};

export default function AdminSettingsPage() {
  const [edited, setEdited] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState<string | null>(null);
  const [saveError, setSaveError] = useState<string | null>(null);

  const {
    data: settings = [],
    isPending: loading,
    refetch,
    error: loadError,
  } = useAuthedQuery<Setting[]>({
    queryKey: qk.settings(),
    queryFn: listSettings,
  });

  const error = saveError ?? (loadError ? errorMessage(loadError, "Failed to load settings") : null);

  async function save(key: string, value: string) {
    setSaving(key);
    setSaveError(null);
    try {
      await setSetting(key, value);
      setEdited((e) => {
        const next = { ...e };
        delete next[key];
        return next;
      });
      await refetch();
    } catch (e) {
      setSaveError(errorMessage(e, `Failed to save ${key}`));
    } finally {
      setSaving(null);
    }
  }

  return (
    <div data-testid="page-settings">
      <h1 className="font-display text-xl font-bold text-text-primary">Settings</h1>
      <p className="mt-1 text-sm text-text-muted">
        What this instance is configured to do. Personal preferences are on your own account.
      </p>
      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}
      {loading && settings.length === 0 ? (
        <p className="mt-4 text-text-muted">Loading settings...</p>
      ) : settings.length === 0 && !error ? (
        <p className="mt-4 text-text-muted">No settings.</p>
      ) : (
        <table data-testid="settings-table" className="mt-4 w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border text-text-muted">
              <th className="py-2 pr-4 font-medium">Setting</th>
              <th className="py-2 pr-4 font-medium">Value</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {settings.map((s) => {
              const value = edited[s.key] ?? s.value;
              const dirty = edited[s.key] !== undefined && edited[s.key] !== s.value;
              return (
                <tr key={s.key} data-testid="setting-row" className="border-b border-border align-top">
                  <td className="py-3 pr-4">
                    <div className="font-mono text-text-primary">{s.key}</div>
                    {notes[s.key] && <p className="mt-1 max-w-prose text-xs text-text-muted">{notes[s.key]}</p>}
                  </td>
                  <td className="py-3 pr-4">
                    <input
                      type="text"
                      value={value}
                      data-setting-key={s.key}
                      onChange={(e) => setEdited((prev) => ({ ...prev, [s.key]: e.target.value }))}
                      className="w-32 rounded-sm border border-border bg-background px-2 py-1 font-mono text-text-primary"
                    />
                  </td>
                  <td className="py-3 text-right">
                    <button
                      type="button"
                      onClick={() => void save(s.key, value)}
                      disabled={!dirty || saving !== null}
                      className="rounded-sm border border-border px-3 py-1 text-xs hover:bg-background disabled:opacity-50"
                    >
                      {saving === s.key ? "Saving..." : "Save"}
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </div>
  );
}
