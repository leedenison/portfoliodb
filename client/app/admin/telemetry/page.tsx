"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { listTelemetryCounters, type TelemetryCounterRow } from "@/lib/portfolio-api";

type CounterEntry = { label: string; value: number };
type CounterCard = { name: string; entries: CounterEntry[] };
type CounterSection = { name: string; cards: CounterCard[] };

function groupCounters(counters: TelemetryCounterRow[]): CounterSection[] {
  const sectionMap = new Map<string, Map<string, CounterEntry[]>>();

  for (const c of counters) {
    const parts = c.name.split(".");
    const section = parts[0] ?? c.name;
    const card = parts[1] ?? "";
    const label = parts.slice(2).join(".") || c.name;

    if (!sectionMap.has(section)) sectionMap.set(section, new Map());
    const cardMap = sectionMap.get(section)!;
    if (!cardMap.has(card)) cardMap.set(card, []);
    cardMap.get(card)!.push({ label, value: c.value });
  }

  const sections: CounterSection[] = [];
  for (const [name, cardMap] of sectionMap) {
    const cards: CounterCard[] = [];
    for (const [cardName, entries] of cardMap) {
      cards.push({ name: cardName, entries });
    }
    sections.push({ name, cards });
  }
  return sections;
}

function sectionTitle(name: string): string {
  const titles: Record<string, string> = {
    openfigi: "OpenFIGI",
    openai: "OpenAI",
  };
  return titles[name] ?? name.charAt(0).toUpperCase() + name.slice(1);
}

function cardTitle(name: string): string {
  return name
    .split("_")
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

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
        <h1 className="font-display text-xl font-bold text-text-primary">Telemetry</h1>
        <p className="mt-2 text-text-muted">Loading counters...</p>
      </div>
    );
  }

  const sections = groupCounters(counters);

  return (
    <div>
      <div className="flex items-center justify-between gap-4">
        <h1 className="font-display text-xl font-bold text-text-primary">Telemetry</h1>
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
      <p className="mt-1 text-sm text-text-muted">
        Counters are discovered from Redis (portfoliodb:counters:*).
      </p>
      {counters.length === 0 && !error ? (
        <p className="mt-4 text-text-muted">No counters yet.</p>
      ) : (
        <div className="mt-6 space-y-8">
          {sections.map((section) => (
            <div key={section.name}>
              <h2 className="font-display text-lg font-semibold text-text-primary">
                {sectionTitle(section.name)}
              </h2>
              <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2">
                {section.cards.map((card) => (
                  <div
                    key={card.name}
                    className="rounded-lg border border-border bg-surface p-4"
                  >
                    <h3 className="mb-3 text-sm font-semibold uppercase tracking-wide text-text-muted">
                      {cardTitle(card.name)}
                    </h3>
                    <ul className="space-y-1.5">
                      {card.entries.map((entry) => (
                        <li
                          key={entry.label}
                          className="flex items-baseline justify-between gap-4 rounded px-2 py-1 transition-colors hover:bg-primary-light/10"
                        >
                          <span className="font-mono text-sm text-text-primary">
                            {entry.label}
                          </span>
                          <span className="tabular-nums text-text-secondary">
                            {entry.value.toLocaleString()}
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
