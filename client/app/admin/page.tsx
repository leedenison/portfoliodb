"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import {
  listIdentifierPlugins,
  listDescriptionPlugins,
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
      "View price data coverage and retrieval status for instruments.",
    disabled: true,
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
    id: "logs",
    title: "Logs",
    href: "/admin/logs",
    description: "View system logs and notable events.",
    disabled: true,
  },
];

export default function AdminOverviewPage() {
  const [identifierPlugins, setIdentifierPlugins] = useState<
    { displayName: string }[]
  >([]);
  const [descriptionPlugins, setDescriptionPlugins] = useState<
    { displayName: string }[]
  >([]);

  const load = useCallback(async () => {
    try {
      const [idList, descList] = await Promise.all([
        listIdentifierPlugins(),
        listDescriptionPlugins(),
      ]);
      setIdentifierPlugins(idList.map((p) => ({ displayName: p.displayName || p.pluginId })));
      setDescriptionPlugins(descList.map((p) => ({ displayName: p.displayName || p.pluginId })));
    } catch {
      // Non-blocking: cards still work without the summary
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  function pluginSummary(
    id: string
  ): string | null {
    if (id === "identifier" && identifierPlugins.length > 0) {
      return identifierPlugins.map((p) => p.displayName).join(", ");
    }
    if (id === "description" && descriptionPlugins.length > 0) {
      return descriptionPlugins.map((p) => p.displayName).join(", ");
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
                <p className="mt-3 font-mono text-xs text-text-muted">
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
