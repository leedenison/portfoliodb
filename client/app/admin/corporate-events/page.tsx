"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  listUnhandledCorporateEvents,
  resolveUnhandledCorporateEvent,
  type UnhandledCorporateEvent,
} from "@/lib/portfolio-api";

function eventTypeBadge(eventType: string): string {
  switch (eventType.toLowerCase()) {
    case "split":
      return "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400";
    case "dividend":
      return "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400";
    default:
      return "bg-gray-100 text-gray-800 dark:bg-gray-900/30 dark:text-gray-400";
  }
}

export default function AdminCorporateEventsPage() {
  const [events, setEvents] = useState<UnhandledCorporateEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [resolving, setResolving] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await listUnhandledCorporateEvents();
      setEvents(result.events);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load corporate events");
    } finally {
      setLoading(false);
    }
  }, []);

  async function handleResolve(id: string) {
    setResolving(id);
    setError(null);
    try {
      await resolveUnhandledCorporateEvent(id);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : `Failed to resolve event ${id}`);
    } finally {
      setResolving(null);
    }
  }

  useEffect(() => {
    load();
  }, [load]);

  if (loading && events.length === 0) {
    return (
      <div>
        <h1 className="font-display text-xl font-bold text-text-primary">Corporate Events</h1>
        <p className="mt-2 text-text-muted">Loading corporate events...</p>
      </div>
    );
  }

  return (
    <div data-testid="page-corporate-events">
      <div className="flex items-center justify-between gap-4">
        <h1 className="font-display text-xl font-bold text-text-primary">Corporate Events</h1>
        <button
          type="button"
          onClick={load}
          disabled={loading}
          className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-dark disabled:opacity-50"
        >
          {loading ? "Refreshing..." : "Refresh"}
        </button>
      </div>
      {error && (
        <div className="mt-2">
          <ErrorAlert>{error}</ErrorAlert>
        </div>
      )}

      <div className="mt-4 flex gap-0 border-b border-border">
        <button
          type="button"
          className="border-b-2 border-accent px-4 py-2 text-sm font-medium text-accent"
        >
          Unhandled
        </button>
        <button
          type="button"
          className="px-4 py-2 text-sm font-medium text-text-muted hover:text-text-primary"
          disabled
        >
          Splits (coming soon)
        </button>
        <button
          type="button"
          className="px-4 py-2 text-sm font-medium text-text-muted hover:text-text-primary"
          disabled
        >
          Dividends (coming soon)
        </button>
      </div>

      {events.length === 0 && !error ? (
        <p className="mt-4 text-text-muted">No unhandled corporate events.</p>
      ) : (
        <table data-testid="corporate-events-table" className="mt-4 w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border text-text-muted">
              <th className="py-2 pr-4 font-medium">Instrument</th>
              <th className="py-2 pr-4 font-medium">Type</th>
              <th className="py-2 pr-4 font-medium">Ex Date</th>
              <th className="py-2 pr-4 font-medium">Detail</th>
              <th className="py-2 pr-4 font-medium">Created</th>
              <th className="py-2 font-medium" />
            </tr>
          </thead>
          <tbody>
            {events.map((ev) => (
              <tr key={ev.id} data-testid="corporate-event-row" className="border-b border-border">
                <td className="py-2 pr-4 font-mono text-text-primary">
                  {ev.instrumentName || ev.instrumentId}
                </td>
                <td className="py-2 pr-4">
                  <span
                    className={`inline-block rounded-full px-2 py-0.5 text-xs font-medium ${eventTypeBadge(ev.eventType)}`}
                  >
                    {ev.eventType}
                  </span>
                </td>
                <td className="py-2 pr-4 text-text-muted">{ev.exDate || "\u2014"}</td>
                <td className="py-2 pr-4 text-text-muted">{ev.detail || "\u2014"}</td>
                <td className="py-2 pr-4 text-text-muted">
                  {ev.createdAt ? ev.createdAt.toLocaleDateString() : "\u2014"}
                </td>
                <td className="py-2 text-right">
                  <button
                    type="button"
                    onClick={() => handleResolve(ev.id)}
                    disabled={resolving !== null}
                    className="rounded border border-border px-3 py-1 text-xs hover:bg-background disabled:opacity-50"
                  >
                    {resolving === ev.id ? "Resolving..." : "Resolve"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
