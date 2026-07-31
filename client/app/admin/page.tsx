"use client";

import { useAuthedQuery } from "@/hooks/use-authed-query";
import { qk } from "@/lib/query-keys";
import Link from "next/link";
import {
  listIdentifierPlugins,
  listDescriptionPlugins,
  listPricePlugins,
  listWorkers,
  countUnhandledCorporateEvents,
  WorkerState,
  type WorkerRow,
} from "@/lib/portfolio-api";

const dashboardCards: {
  id: string;
  title: string;
  href: string;
  description: string;
  disabled?: boolean;
}[] = [
  {
    id: "instruments",
    title: "Instruments",
    href: "/admin/instruments",
    description:
      "Browse and inspect instrument reference data, identifiers and status.",
  },
  {
    id: "prices",
    title: "Prices",
    href: "/admin/prices",
    description:
      "Browse cached EOD prices and manage price fetch blocks.",
  },
  {
    id: "identifier",
    title: "Identifier plugins",
    href: "/admin/plugins/identifier",
    description:
      "Enable/disable identification plugins and edit config (API keys, precedence).",
  },
  {
    id: "description",
    title: "Description plugins",
    href: "/admin/plugins/description",
    description:
      "Enable/disable description plugins that extract identifier hints from broker text.",
  },
  {
    id: "price",
    title: "Price plugins",
    href: "/admin/plugins/price",
    description:
      "Enable/disable price plugins that fetch end-of-day prices for identified instruments.",
  },
  {
    id: "workers",
    title: "Workers",
    href: "/admin/workers",
    description: "Background worker status and manual fetch triggers.",
  },
  {
    id: "telemetry",
    title: "Telemetry",
    href: "/admin/telemetry",
    description: "View Redis-backed counters (portfoliodb:counters:*).",
  },
  {
    id: "tools",
    title: "Authentication",
    href: "/admin/tools",
    description: "View session token and fetch a Google ID token for scripts.",
  },
  {
    id: "corporate-events",
    title: "Corporate Events",
    href: "/admin/corporate-events",
    description:
      "Review and resolve unhandled corporate events (splits, dividends).",
  },
  {
    id: "logs",
    title: "Logs",
    href: "/admin/logs",
    description: "View system logs and notable events.",
    disabled: true,
  },
];

export default function AdminOverviewPage() {
  // Five separate queries rather than one Promise.all whose catch swallowed
  // everything: a card whose summary fails now blanks on its own instead of
  // taking the other four with it. The plugin lists share their keys with the
  // plugin pages, so navigating there is a cache hit.
  const names = (list: { displayName: string; pluginId: string }[]) =>
    list.map((p) => ({ displayName: p.displayName || p.pluginId }));

  const { data: identifierPlugins = [] } = useAuthedQuery({
    queryKey: qk.plugins("identifier"),
    queryFn: listIdentifierPlugins,
    select: names,
  });
  const { data: descriptionPlugins = [] } = useAuthedQuery({
    queryKey: qk.plugins("description"),
    queryFn: listDescriptionPlugins,
    select: names,
  });
  const { data: pricePlugins = [] } = useAuthedQuery({
    queryKey: qk.plugins("price"),
    queryFn: listPricePlugins,
    select: names,
  });
  const { data: workers = [] } = useAuthedQuery<WorkerRow[]>({
    queryKey: qk.workers(),
    queryFn: listWorkers,
  });
  const { data: unhandledEventCount = 0 } = useAuthedQuery<number>({
    queryKey: qk.unhandledCorporateEventCount(),
    queryFn: countUnhandledCorporateEvents,
  });

  function pluginSummary(
    id: string
  ): string | null {
    if (id === "identifier" && identifierPlugins.length > 0) {
      return identifierPlugins.map((p) => p.displayName).join(", ");
    }
    if (id === "description" && descriptionPlugins.length > 0) {
      return descriptionPlugins.map((p) => p.displayName).join(", ");
    }
    if (id === "price" && pricePlugins.length > 0) {
      return pricePlugins.map((p) => p.displayName).join(", ");
    }
    if (id === "workers" && workers.length > 0) {
      return workers
        .map((w) => `${w.name}: ${w.state === WorkerState.RUNNING ? "running" : "idle"}`)
        .join(", ");
    }
    if (id === "corporate-events") {
      return unhandledEventCount > 0
        ? `${unhandledEventCount} unhandled`
        : "No unhandled events";
    }
    return null;
  }

  return (
    <div className="space-y-6">
      <h1 className="font-display text-2xl font-bold tracking-tight text-text-primary">Dashboard</h1>
      <p className="text-sm text-text-muted">
        Quick links to admin tools. Use the sidebar for full navigation.
      </p>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        {dashboardCards.map((card) => {
          const summary = pluginSummary(card.id);
          if (card.disabled) {
            return (
              <div
                key={card.id}
                className="block cursor-default rounded-md border border-border bg-surface p-5 opacity-40 shadow-sm"
              >
                <h2 className="font-display font-semibold text-text-primary">{card.title}</h2>
                <p className="mt-1.5 text-sm text-text-muted">{card.description}</p>
              </div>
            );
          }
          return (
            <Link
              key={card.id}
              href={card.href}
              className="group block rounded-md border border-border bg-surface p-5 shadow-sm transition-all hover:border-primary/40 hover:shadow-md"
            >
              <h2 className="font-display font-semibold text-text-primary group-hover:text-primary-dark">{card.title}</h2>
              <p className="mt-1.5 text-sm text-text-muted">{card.description}</p>
              {summary && (
                <p className={`mt-3 font-mono text-xs ${
                  card.id === "corporate-events" && unhandledEventCount > 0
                    ? "text-amber-600 dark:text-amber-400"
                    : "text-text-muted"
                }`}>
                  {summary}
                </p>
              )}
            </Link>
          );
        })}
      </div>
    </div>
  );
}
