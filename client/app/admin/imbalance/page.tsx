"use client";

import { useState } from "react";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { dayAfter } from "@/lib/dates";
import { formatCurrency } from "@/lib/format";
import { getBrokerLabel } from "@/lib/csv/converters";
import { ACCOUNT_TYPE_LABEL, TX_TYPE_LABEL } from "@/lib/tx-type";
import { ErrorAlert } from "@/app/components/error-alert";
import { listResidualBalances, type ResidualBalance } from "@/lib/portfolio-api";
import { ageInDays, groupByBroker, isMoney, transferAgeBucket } from "@/lib/residual-balance";
import { AccountType, AssetClass, TxType, type Broker } from "@/gen/api/v1/api_pb";

type Tab = "imbalance" | "transfers";

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick?: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      className={
        active
          ? "border-b-2 border-accent px-4 py-2 text-sm font-medium text-accent"
          : "px-4 py-2 text-sm font-medium text-text-muted hover:text-text-primary"
      }
      onClick={onClick}
    >
      {children}
    </button>
  );
}

/**
 * A balance in a currency is money; one in a security is a quantity of shares.
 * They are formatted differently and never added together.
 */
function amount(b: { balance: string; commodity: string; assetClass: AssetClass }): string {
  if (isMoney(b)) return formatCurrency(b.balance, b.commodity);
  // Grouped for readability, but formatted from the string so the digits are the
  // ones the server sent.
  const qty = new Intl.NumberFormat(undefined, { maximumFractionDigits: 4 }).format(
    b.balance as unknown as number,
  );
  return `${qty} ${b.commodity}`;
}

export default function AdminImbalancePage() {
  const [tab, setTab] = useState<Tab>("imbalance");
  const [dateFrom, setDateFrom] = useState("");
  const [dateTo, setDateTo] = useState("");

  const {
    data: balances = [],
    isPending: loading,
    error: loadError,
  } = useAuthedQuery<ResidualBalance[]>({
    queryKey: qk.residualBalances(dateFrom, dateTo),
    queryFn: () => {
      // The pickers are inclusive; the API bound is exclusive.
      const before = dayAfter(dateTo);
      return listResidualBalances({
        periodFrom: dateFrom ? new Date(dateFrom) : undefined,
        periodBefore: before ? new Date(before) : undefined,
      });
    },
  });

  const error = loadError ? errorMessage(loadError, "Failed to load residual balances") : null;
  const imbalances = balances.filter((b) => b.accountType === AccountType.IMBALANCE);
  const transfers = balances
    .filter((b) => b.accountType === AccountType.TRANSFER_CLEARING)
    .sort((a, b) => (a.oldestTimestamp?.getTime() ?? Infinity) - (b.oldestTimestamp?.getTime() ?? Infinity));

  return (
    <div data-testid="page-imbalance" className="space-y-4">
      <h1 className="font-display text-xl font-bold text-text-primary">Imbalance</h1>
      <p className="max-w-3xl text-sm text-text-muted">
        What each broker converter failed to capture. A group whose postings do not sum
        to zero has its residual routed to {ACCOUNT_TYPE_LABEL[AccountType.IMBALANCE]},
        so the size of these balances is a direct measure of how lossy each converter
        is. Balances are per commodity and are never converted between them.
      </p>

      {error && <ErrorAlert>{error}</ErrorAlert>}

      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          From
          <input
            type="date"
            value={dateFrom}
            onChange={(e) => setDateFrom(e.target.value)}
            className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-text-muted">
          To
          <input
            type="date"
            value={dateTo}
            onChange={(e) => setDateTo(e.target.value)}
            className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text-primary focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
          />
        </label>
        {(dateFrom || dateTo) && (
          <button
            type="button"
            onClick={() => {
              setDateFrom("");
              setDateTo("");
            }}
            className="rounded-sm border border-border px-3 py-1.5 text-xs hover:bg-background"
          >
            Clear
          </button>
        )}
      </div>

      <div className="flex gap-0 border-b border-border">
        <TabButton active={tab === "imbalance"} onClick={() => setTab("imbalance")}>
          Imbalance
        </TabButton>
        <TabButton active={tab === "transfers"} onClick={() => setTab("transfers")}>
          Unmatched transfers
        </TabButton>
      </div>

      {tab === "imbalance" && (
        <ImbalanceTab rows={imbalances} loading={loading} hasError={error !== null} />
      )}
      {tab === "transfers" && (
        <TransfersTab rows={transfers} loading={loading} hasError={error !== null} />
      )}
    </div>
  );
}

function ImbalanceTab({
  rows,
  loading,
  hasError,
}: {
  rows: ResidualBalance[];
  loading: boolean;
  hasError: boolean;
}) {
  if (loading && rows.length === 0) return <p className="text-text-muted">Loading balances...</p>;
  if (rows.length === 0 && !hasError) {
    return <p className="text-text-muted">No imbalances. Every group balances.</p>;
  }

  const groups = groupByBroker(rows);
  return (
    <div className="space-y-3">
      <p className="max-w-3xl text-xs text-text-muted">
        Expect uncategorised income to dominate until each converter emits the income
        and expense legs of its single-row dividends and charges. The tx type separates
        the two: an {TX_TYPE_LABEL[TxType.INCOME]} balance is a dividend we do not categorise yet,
        a trade balance is a fee the broker did not report. A negative balance is value
        arriving from outside the ledger, which is what an uncategorised dividend looks
        like.
      </p>
      <table data-testid="imbalance-table" className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-border text-text-muted">
            <th className="py-2 pr-4 font-medium">Account</th>
            <th className="py-2 pr-4 font-medium">Commodity</th>
            <th className="py-2 pr-4 font-medium">Event</th>
            <th className="py-2 pr-4 text-right font-medium">Balance</th>
            <th className="py-2 text-right font-medium">Postings</th>
          </tr>
        </thead>
        {groups.map((g) => (
          <tbody key={g.broker} data-testid="imbalance-broker-group">
            <tr className="border-b border-border bg-primary-dark/3">
              <th colSpan={3} className="py-2 pr-4 text-left font-display font-semibold text-text-primary">
                {getBrokerLabel(g.broker as Broker)}
              </th>
              <td
                data-testid="imbalance-subtotal"
                className="py-2 pr-4 text-right font-mono text-text-primary"
              >
                {g.subtotals.map((s) => (
                  <span key={s.commodity} className="ml-3 inline-block">
                    {amount(s)}
                  </span>
                ))}
              </td>
              <td className="py-2 text-right font-mono text-text-muted">
                {g.subtotals.reduce((n, s) => n + s.postingCount, 0)}
              </td>
            </tr>
            {g.rows.map((r) => (
              <tr
                key={`${r.account}|${r.instrumentId}|${r.txType}`}
                data-testid="imbalance-row"
                className="border-b border-border"
              >
                <td className="py-2 pr-4 pl-4 font-mono text-text-primary">{r.account || "\u2014"}</td>
                <td className="py-2 pr-4 font-mono text-text-muted">{r.commodity}</td>
                <td className="py-2 pr-4 text-text-muted">{TX_TYPE_LABEL[r.txType] ?? r.txType}</td>
                <td className="py-2 pr-4 text-right font-mono tabular-nums text-text-primary">
                  {amount(r)}
                </td>
                <td className="py-2 text-right font-mono tabular-nums text-text-muted">
                  {r.postingCount}
                </td>
              </tr>
            ))}
          </tbody>
        ))}
      </table>
      <p className="text-xs text-text-muted">
        Drilling into the groups behind a balance is not implemented yet.
      </p>
    </div>
  );
}

function TransfersTab({
  rows,
  loading,
  hasError,
}: {
  rows: ResidualBalance[];
  loading: boolean;
  hasError: boolean;
}) {
  if (loading && rows.length === 0) return <p className="text-text-muted">Loading transfers...</p>;
  if (rows.length === 0 && !hasError) {
    return <p className="text-text-muted">No transfer balances.</p>;
  }

  const now = new Date();
  return (
    <div className="space-y-3">
      <div className="max-w-3xl rounded-md border border-border bg-surface p-3 text-xs text-text-muted">
        <p className="font-semibold text-text-primary">
          These are every imported transfer, not the unmatched ones.
        </p>
        <p className="mt-1">
          Each side of a journal is posted against a clearing account and nothing
          pairs the two, so a completed transfer is indistinguishable from one whose
          second side never arrived. Both are listed here. Until transfer matching
          lands, a balance below says a transfer was imported, and its age is the age
          of the posting rather than of anything missing.
        </p>
      </div>
      <table data-testid="transfers-table" className="w-full text-left text-sm">
        <thead>
          <tr className="border-b border-border text-text-muted">
            <th className="py-2 pr-4 font-medium">Broker</th>
            <th className="py-2 pr-4 font-medium">Account</th>
            <th className="py-2 pr-4 font-medium">Commodity</th>
            <th className="py-2 pr-4 font-medium">Event</th>
            <th className="py-2 pr-4 text-right font-medium">Balance</th>
            <th className="py-2 text-right font-medium">Waiting</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const bucket = transferAgeBucket(r.oldestTimestamp, now);
            const days = ageInDays(r.oldestTimestamp, now);
            return (
              <tr
                key={`${r.broker}|${r.account}|${r.instrumentId}|${r.txType}`}
                data-testid="transfer-row"
                data-age-bucket={bucket}
                className="border-b border-border"
              >
                <td className="py-2 pr-4 text-text-primary">{getBrokerLabel(r.broker as Broker)}</td>
                <td className="py-2 pr-4 font-mono text-text-primary">{r.account || "\u2014"}</td>
                <td className="py-2 pr-4 font-mono text-text-muted">{r.commodity}</td>
                <td className="py-2 pr-4 text-text-muted">{TX_TYPE_LABEL[r.txType] ?? r.txType}</td>
                <td className="py-2 pr-4 text-right font-mono tabular-nums text-text-primary">
                  {amount(r)}
                </td>
                <td className="py-2 text-right font-mono tabular-nums text-text-muted">
                  {days === null ? "\u2014" : `${days}d`}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
