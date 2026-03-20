"use client";

import {
  Area,
  AreaChart,
  CartesianGrid,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { ValuationPointUI } from "@/lib/portfolio-api";

interface Props {
  points: ValuationPointUI[];
}

function formatCurrency(v: number): string {
  if (v >= 1_000_000) return `$${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `$${(v / 1_000).toFixed(1)}K`;
  return `$${v.toFixed(0)}`;
}

function formatDate(dateStr: string): string {
  const d = new Date(dateStr + "T00:00:00");
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function CustomTooltip({
  active,
  payload,
}: {
  active?: boolean;
  payload?: Array<{ payload: ValuationPointUI }>;
}) {
  if (!active || !payload?.length) return null;
  const pt = payload[0].payload;
  const d = new Date(pt.date + "T00:00:00");
  const dateLabel = d.toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
  return (
    <div className="rounded-md border border-border bg-surface px-3 py-2 text-sm shadow-md">
      <p className="font-medium text-text-primary">{dateLabel}</p>
      <p className="font-mono tabular-nums text-text-primary">
        ${pt.totalValue.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}
      </p>
      {pt.unpricedInstruments.length > 0 && (
        <div className="mt-1.5 border-t border-border pt-1.5">
          <p className="text-xs font-medium text-accent-dark">
            Missing price data:
          </p>
          {pt.unpricedInstruments.map((name) => (
            <p key={name} className="text-xs text-accent-dark">
              {name}
            </p>
          ))}
        </div>
      )}
    </div>
  );
}

interface UnpricedRegion {
  startDate: string;
  endDate: string;
  instruments: string[];
}

function computeUnpricedRegions(points: ValuationPointUI[]): UnpricedRegion[] {
  const regions: UnpricedRegion[] = [];
  let current: UnpricedRegion | null = null;

  for (const pt of points) {
    const sorted = [...pt.unpricedInstruments].sort();
    const key = sorted.join("\0");

    if (sorted.length === 0) {
      if (current) {
        regions.push(current);
        current = null;
      }
    } else if (!current || [...current.instruments].sort().join("\0") !== key) {
      if (current) regions.push(current);
      current = { startDate: pt.date, endDate: pt.date, instruments: sorted };
    } else {
      current.endDate = pt.date;
    }
  }
  if (current) regions.push(current);
  return regions;
}

// Compute tick values to avoid crowding (target ~6 ticks).
function dateTicks(points: ValuationPointUI[]): string[] {
  if (points.length <= 6) return points.map((p) => p.date);
  const step = Math.ceil(points.length / 6);
  const ticks: string[] = [];
  for (let i = 0; i < points.length; i += step) {
    ticks.push(points[i].date);
  }
  // Always include last point.
  if (ticks[ticks.length - 1] !== points[points.length - 1].date) {
    ticks.push(points[points.length - 1].date);
  }
  return ticks;
}

export function PerformanceChart({ points }: Props) {
  if (points.length === 0) {
    return (
      <div className="flex h-[400px] items-center justify-center text-text-muted">
        No valuation data for this period.
      </div>
    );
  }

  const regions = computeUnpricedRegions(points);
  const hasUnpriced = regions.length > 0;
  const ticks = dateTicks(points);

  return (
    <div>
      {hasUnpriced && (
        <p className="mb-3 rounded-md bg-accent-soft/50 px-3 py-2 text-xs text-accent-dark">
          Highlighted regions indicate periods with missing price data.
        </p>
      )}
      <ResponsiveContainer width="100%" height={400}>
        <AreaChart data={points} margin={{ top: 8, right: 16, bottom: 0, left: 8 }}>
          <defs>
            <linearGradient id="valGradient" x1="0" y1="0" x2="0" y2="1">
              <stop
                offset="5%"
                stopColor="rgb(var(--color-primary))"
                stopOpacity={0.3}
              />
              <stop
                offset="95%"
                stopColor="rgb(var(--color-primary))"
                stopOpacity={0.02}
              />
            </linearGradient>
          </defs>
          <CartesianGrid
            strokeDasharray="3 3"
            stroke="rgb(var(--color-border))"
            strokeOpacity={0.6}
          />
          <XAxis
            dataKey="date"
            tickFormatter={formatDate}
            ticks={ticks}
            tick={{ fontSize: 11, fill: "rgb(var(--color-text-muted))" }}
            axisLine={{ stroke: "rgb(var(--color-border))" }}
            tickLine={false}
          />
          <YAxis
            tickFormatter={formatCurrency}
            tick={{ fontSize: 11, fill: "rgb(var(--color-text-muted))" }}
            axisLine={false}
            tickLine={false}
            width={64}
          />
          <Tooltip content={<CustomTooltip />} />
          {regions.map((r, i) => (
            <ReferenceArea
              key={i}
              x1={r.startDate}
              x2={r.endDate}
              fill="rgb(var(--color-accent))"
              fillOpacity={0.12}
              stroke="none"
            />
          ))}
          <Area
            type="monotone"
            dataKey="totalValue"
            stroke="rgb(var(--color-primary))"
            strokeWidth={2}
            fill="url(#valGradient)"
            dot={false}
            activeDot={{ r: 4, fill: "rgb(var(--color-primary-dark))" }}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
