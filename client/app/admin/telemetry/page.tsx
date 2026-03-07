"use client";

import { useCallback, useEffect, useState } from "react";
import { listTelemetryCounters, type TelemetryCounterRow } from "@/lib/portfolio-api";

export default function AdminTelemetryPage() {
  const [counters, setCounters] = useState<TelemetryCounterRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await listTelemetryCounters();
      setCounters(list);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load counters");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  if (loading && counters.length === 0) {
    return (
      <div>
        <h1 className="text-xl font-semibold text-text-primary">Telemetry</h1>
        <p className="mt-2 text-text-muted">Loading counters…</p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between gap-4">
        <h1 className="text-xl font-semibold text-text-primary">Telemetry</h1>
        <button
          type="button"
          onClick={load}
          disabled={loading}
          className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-dark disabled:opacity-50"
        >
          {loading ? "Refreshing…" : "Refresh"}
        </button>
      </div>
      {error && (
        <p className="mt-2 text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
      <p className="mt-1 text-sm text-text-muted">
        Counters are discovered from Redis (portfoliodb:counters:*).
      </p>
      {counters.length === 0 && !error ? (
        <p className="mt-4 text-text-muted">No counters yet.</p>
      ) : (
        <ul className="mt-4 space-y-2">
          {counters.map((c) => (
            <li
              key={c.name}
              className="flex items-baseline justify-between gap-4 rounded border border-border bg-card px-3 py-2"
            >
              <span className="font-mono text-sm text-text-primary">{c.name}</span>
              <span className="tabular-nums text-text-secondary">{c.value.toLocaleString()}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
