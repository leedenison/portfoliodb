"use client";

import { useQueryClient } from "@tanstack/react-query";
import { AppShell } from "@/app/components/app-shell";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { useAuth } from "@/contexts/auth-context";
import { usePortfolio } from "@/contexts/portfolio-context";
import { useUploadModal } from "@/contexts/upload-modal-context";
import { usePagination } from "@/hooks/use-pagination";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { listTxs } from "@/lib/portfolio-api";
import { getBrokerLabel } from "@/lib/csv/converters";
import { ACCOUNT_TYPE_LABEL, TX_TYPE_LABEL } from "@/lib/tx-type";
import { IdentifierType } from "@/gen/api/v1/api_pb";
import type { PortfolioTx } from "@/gen/api/v1/api_pb";
import { timestampDate } from "@bufbuild/protobuf/wkt";

export default function TxsPage() {
  const { state, authError } = useAuth();
  const { selected: selectedPortfolio } = usePortfolio();
  const { openUploadModal } = useUploadModal();
  const queryClient = useQueryClient();
  const portfolioId = selectedPortfolio?.id;

  if (state.status === "loading") {
    return (
      <AppShell>
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-text-muted">Loading...</p>
        </div>
      </AppShell>
    );
  }

  if (state.status === "unauthenticated") {
    return (
      <AppShell>
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8 text-center">
          <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
            Transactions
          </h1>
          <p className="mt-3 text-text-muted">Sign in to view transactions.</p>
          {authError && (
            <p className="mt-4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
              {authError}
            </p>
          )}
        </div>
      </AppShell>
    );
  }

  return (
    <AppShell>
      <div data-testid="page-transactions" className="flex flex-1 flex-col items-center px-4 py-8">
        <div className="w-full max-w-5xl animate-fade-in space-y-5">
          <div className="flex flex-wrap items-baseline gap-3">
            <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
              Transactions
            </h2>
            <button
              type="button"
              onClick={() =>
                openUploadModal(() =>
                  queryClient.invalidateQueries({ queryKey: qk.txs(portfolioId) })
                )
              }
              className="ml-auto rounded-md bg-accent px-3.5 py-1.5 text-sm font-semibold text-white transition-colors hover:bg-accent-dark"
            >
              Upload transactions
            </button>
          </div>

          {/* Keyed on the portfolio: switching it remounts the list, which
              returns it to page 1. */}
          <TxList key={portfolioId ?? "all"} portfolioId={portfolioId} />
        </div>
      </div>
    </AppShell>
  );
}

function TxList({ portfolioId }: { portfolioId: string | undefined }) {
  const {
    items: txs,
    loading,
    error,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
  } = usePagination<PortfolioTx>({
    queryKey: qk.txs(portfolioId),
    queryFn: async (pageToken) => {
      const result = await listTxs({ portfolioId, pageToken });
      return {
        items: result.txs,
        // ListTxs returns no count.
        totalCount: 0,
        nextPageToken: result.nextPageToken,
      };
    },
  });

  return (
    <>
      {loading && <p className="text-text-muted">Loading transactions...</p>}
      {!loading && error && <ErrorAlert>{errorMessage(error)}</ErrorAlert>}
      {!loading && !error && (
        <>
              <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-xs">
                <table data-testid="transactions-table" className="w-full min-w-[720px] border-collapse text-sm">
                  <thead>
                    <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/3">
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Date
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Broker
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Account
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Instrument
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Type
                      </th>
                      <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Quantity
                      </th>
                      <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Unit Price
                      </th>
                      <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Adj Qty
                      </th>
                      <th className="px-4 py-3 text-right text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Adj Price
                      </th>
                      <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                        Currency
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {txs.length === 0 ? (
                      <tr>
                        <td
                          colSpan={10}
                          className="px-4 py-8 text-center text-text-muted"
                        >
                          No transactions found.
                        </td>
                      </tr>
                    ) : (
                      txs.map((ptx, i) => (
                        <TxRow key={i} ptx={ptx} />
                      ))
                    )}
                  </tbody>
                </table>
              </div>

              <PaginationControls
                pageIndex={pageIndex}
                hasPrev={hasPrev}
                hasNext={hasNext}
                onPrev={goPrev}
                onNext={goNext}
              />
        </>
      )}
    </>
  );
}

function TxRow({ ptx }: { ptx: PortfolioTx }) {
  const tx = ptx.tx;
  if (!tx) return null;

  const isSynthetic = !!tx.syntheticPurpose;
  const accountTypeLabel = ACCOUNT_TYPE_LABEL[tx.accountType];
  const ticker = ptx.instrument?.identifiers?.find(
    (id) => id.type === IdentifierType.MIC_TICKER || id.type === IdentifierType.OPENFIGI_TICKER
  )?.value;
  const label = ticker || ptx.instrument?.name || tx.instrumentDescription || "\u2014";
  const currency = tx.tradingCurrency || tx.settlementCurrency || "";

  return (
    <tr data-testid="tx-row" data-tx-instrument={label} className={
      "border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10" +
      (isSynthetic ? " opacity-60" : "")
    }>
      <td className="px-4 py-3 text-text-muted">
        {tx.timestamp ? timestampDate(tx.timestamp).toLocaleDateString() : "\u2014"}
      </td>
      <td className="px-4 py-3 text-text-muted">
        {getBrokerLabel(ptx.broker)}
      </td>
      <td className="px-4 py-3 text-text-muted">
        {ptx.account || "\u2014"}
      </td>
      <td data-testid="tx-instrument" className="px-4 py-3 font-medium text-text-primary">
        {label}
      </td>
      <td className="px-4 py-3 text-text-muted">
        <span className="flex flex-wrap items-center gap-1.5">
          {isSynthetic ? (
            <span className="inline-block rounded-sm bg-primary-dark/10 px-1.5 py-0.5 text-xs font-medium text-primary-dark">
              {tx.syntheticPurpose}
            </span>
          ) : (
            TX_TYPE_LABEL[tx.type] ?? "Unknown"
          )}
          {accountTypeLabel && (
            <span
              data-testid="tx-account-type"
              className="inline-block rounded-sm bg-text-muted/10 px-1.5 py-0.5 text-xs font-medium text-text-muted"
            >
              {accountTypeLabel}
            </span>
          )}
        </span>
      </td>
      <td data-testid="tx-qty" className="px-4 py-3 text-right font-mono tabular-nums text-text-primary">
        {parseFloat(tx.quantity.toFixed(4))}
      </td>
      <td className="px-4 py-3 text-right font-mono tabular-nums text-text-muted">
        {tx.unitPrice !== undefined ? tx.unitPrice.toFixed(2) : "\u2014"}
      </td>
      <td data-testid="tx-adj-qty" className="px-4 py-3 text-right font-mono tabular-nums text-text-muted">
        {tx.splitAdjustedQuantity !== undefined && tx.splitAdjustedQuantity !== tx.quantity
          ? parseFloat(tx.splitAdjustedQuantity.toFixed(4))
          : "\u2014"}
      </td>
      <td data-testid="tx-adj-price" className="px-4 py-3 text-right font-mono tabular-nums text-text-muted">
        {tx.splitAdjustedUnitPrice !== undefined && tx.splitAdjustedUnitPrice !== tx.unitPrice
          ? tx.splitAdjustedUnitPrice.toFixed(2)
          : "\u2014"}
      </td>
      <td className="px-4 py-3 text-text-muted">
        {currency || "\u2014"}
      </td>
    </tr>
  );
}
