"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { listTelemetryCounters, type TelemetryCounterRow } from "@/lib/portfolio-api";

type CounterEntry = { label: string; value: number };
type CounterHeading2 = { name: string; entries: CounterEntry[] };
type CounterHeading1 = { name: string; entries: CounterEntry[]; subheadings: CounterHeading2[] };
type CounterCard = { name: string; entries: CounterEntry[]; headings: CounterHeading1[] };
type CounterSection = { name: string; cards: CounterCard[] };

// groupCounters builds a 5-level hierarchy from flat dot-separated counter names:
//   segment 1 = section, segment 2 = card, segment 3 = heading 1, segment 4 = heading 2, segment 5 = entry.
// Counters with fewer segments terminate at the appropriate level.
function groupCounters(counters: TelemetryCounterRow[]): CounterSection[] {
  const sections = new Map<string, CounterCard[]>();

  const getOrCreate = <T,>(map: Map<string, T>, key: string, factory: () => T): T => {
    if (!map.has(key)) map.set(key, factory());
    return map.get(key)!;
  };

  // Index cards/headings by name within their parent for fast lookup.
  const cardIndex = new Map<string, Map<string, CounterCard>>();
  const h1Index = new Map<string, Map<string, CounterHeading1>>();
  const h2Index = new Map<string, Map<string, CounterHeading2>>();

  for (const c of counters) {
    const parts = c.name.split(".");
    const sectionName = parts[0] ?? c.name;

    if (!sections.has(sectionName)) {
      sections.set(sectionName, []);
      cardIndex.set(sectionName, new Map());
    }
    const sectionCards = sections.get(sectionName)!;
    const sectionCardIdx = cardIndex.get(sectionName)!;

    // Leaf label is always the last segment (or remaining segments joined for 5+).
    // Structural segments before the leaf determine nesting depth.

    // 1 segment: leaf at section level (no card)
    if (parts.length === 1) {
      const card = getOrCreate(sectionCardIdx, "", () => {
        const cd: CounterCard = { name: "", entries: [], headings: [] };
        sectionCards.push(cd);
        return cd;
      });
      card.entries.push({ label: parts[0], value: c.value });
      continue;
    }

    // 2 segments: section > card-level leaf (no heading)
    if (parts.length === 2) {
      const card = getOrCreate(sectionCardIdx, "", () => {
        const cd: CounterCard = { name: "", entries: [], headings: [] };
        sectionCards.push(cd);
        return cd;
      });
      card.entries.push({ label: parts[1], value: c.value });
      continue;
    }

    // 3+ segments: section > card > ...
    const cardName = parts[1];
    const card = getOrCreate(sectionCardIdx, cardName, () => {
      const cd: CounterCard = { name: cardName, entries: [], headings: [] };
      sectionCards.push(cd);
      return cd;
    });

    // 3 segments: leaf in card (no heading)
    if (parts.length === 3) {
      card.entries.push({ label: parts[2], value: c.value });
      continue;
    }

    // 4+ segments: card > heading 1 > ...
    const h1Name = parts[2];
    const h1Key = `${sectionName}.${cardName}.${h1Name}`;
    if (!h1Index.has(h1Key)) h1Index.set(h1Key, new Map());
    const cardH1Idx = h1Index.get(h1Key)!;

    const h1 = getOrCreate(cardH1Idx, h1Name, () => {
      const hd: CounterHeading1 = { name: h1Name, entries: [], subheadings: [] };
      card.headings.push(hd);
      return hd;
    });

    // 4 segments: leaf under heading 1 (no subheading)
    if (parts.length === 4) {
      h1.entries.push({ label: parts[3], value: c.value });
      continue;
    }

    // 5+ segments: heading 1 > heading 2 > leaf
    const h2Name = parts[3];
    const h2Key = `${h1Key}.${h2Name}`;
    if (!h2Index.has(h2Key)) h2Index.set(h2Key, new Map());
    const h1H2Idx = h2Index.get(h2Key)!;

    const h2 = getOrCreate(h1H2Idx, h2Name, () => {
      const hd: CounterHeading2 = { name: h2Name, entries: [] };
      h1.subheadings.push(hd);
      return hd;
    });

    // 5+ segments: remaining segments joined as leaf label
    h2.entries.push({ label: parts.slice(4).join("."), value: c.value });
  }

  const result: CounterSection[] = [];
  for (const [name, cards] of sections) {
    result.push({ name, cards });
  }
  return result;
}

function titleCase(name: string): string {
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
                {titleCase(section.name)}
              </h2>
              <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2">
                {section.cards.map((card) => (
                  <div
                    key={card.name}
                    className="rounded-lg border border-border bg-surface p-4"
                  >
                    {card.name && (
                      <h3 className="mb-3 text-sm font-semibold uppercase tracking-wide text-text-muted">
                        {titleCase(card.name)}
                      </h3>
                    )}
                    <EntryList entries={card.entries} />
                    {card.headings.map((h1) => (
                      <div key={h1.name} className="mt-3 first:mt-0">
                        <h4 className="mb-2 border-b border-border pb-1 text-sm font-medium text-text-primary">
                          {titleCase(h1.name)}
                        </h4>
                        <EntryList entries={h1.entries} />
                        {h1.subheadings.map((h2) => (
                          <div key={h2.name} className="mb-2 ml-3 mt-1">
                            <h5 className="mb-1 text-xs font-medium text-text-muted">
                              {titleCase(h2.name)}
                            </h5>
                            <EntryList entries={h2.entries} />
                          </div>
                        ))}
                      </div>
                    ))}
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

function EntryList({ entries }: { entries: CounterEntry[] }) {
  if (entries.length === 0) return null;
  return (
    <ul className="space-y-1">
      {entries.map((entry) => (
        <li
          key={entry.label}
          className="flex items-baseline justify-between gap-4 rounded px-2 py-0.5 transition-colors hover:bg-primary-light/10"
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
  );
}
