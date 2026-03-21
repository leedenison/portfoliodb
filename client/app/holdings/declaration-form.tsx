"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  listBrokersAndAccounts,
  listInstruments,
  createHoldingDeclaration,
  updateHoldingDeclaration,
} from "@/lib/portfolio-api";
import type { BrokerAccounts } from "@/lib/portfolio-api";
import { IdentifierType } from "@/gen/api/v1/api_pb";
import type { HoldingDeclaration, Instrument } from "@/gen/api/v1/api_pb";

function todayStr(): string {
  return new Date().toISOString().slice(0, 10);
}

export function DeclarationForm({
  editing,
  onDone,
  onCancel,
}: {
  editing: HoldingDeclaration | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [brokerAccounts, setBrokerAccounts] = useState<BrokerAccounts[]>([]);
  const [broker, setBroker] = useState(editing?.broker ?? "");
  const [account, setAccount] = useState(editing?.account ?? "");
  const [instrumentId, setInstrumentId] = useState(editing?.instrumentId ?? "");
  const [instrumentLabel, setInstrumentLabel] = useState("");
  const [declaredQty, setDeclaredQty] = useState(editing?.declaredQty ?? "");
  const [asOfDate, setAsOfDate] = useState(editing?.asOfDate ?? todayStr());
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Instrument search
  const [instrumentSearch, setInstrumentSearch] = useState("");
  const [searchResults, setSearchResults] = useState<Instrument[]>([]);
  const [searchLoading, setSearchLoading] = useState(false);

  useEffect(() => {
    listBrokersAndAccounts()
      .then(setBrokerAccounts)
      .catch(() => {});
  }, []);

  // Set initial instrument label for editing
  useEffect(() => {
    if (editing?.instrument) {
      const ticker = editing.instrument.identifiers?.find(
        (id) => id.type === IdentifierType.TICKER
      )?.value;
      setInstrumentLabel(ticker || editing.instrument.name || editing.instrumentId);
    }
  }, [editing]);

  // Instrument search with debounce
  useEffect(() => {
    if (instrumentSearch.length < 2) {
      setSearchResults([]);
      return;
    }
    const timer = setTimeout(async () => {
      setSearchLoading(true);
      try {
        const res = await listInstruments({ search: instrumentSearch });
        setSearchResults(res.instruments);
      } catch {
        setSearchResults([]);
      } finally {
        setSearchLoading(false);
      }
    }, 300);
    return () => clearTimeout(timer);
  }, [instrumentSearch]);

  const accounts = brokerAccounts.find((b) => b.broker === broker)?.accounts ?? [];

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!broker || !instrumentId || !declaredQty || !asOfDate) {
      setError("All fields are required.");
      return;
    }
    setSubmitting(true);
    try {
      if (editing) {
        await updateHoldingDeclaration({
          id: editing.id,
          declaredQty,
          asOfDate,
        });
      } else {
        await createHoldingDeclaration({
          broker,
          account,
          instrumentId,
          declaredQty,
          asOfDate,
        });
      }
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSubmitting(false);
    }
  };

  const selectInstrument = (inst: Instrument) => {
    setInstrumentId(inst.id);
    const ticker = inst.identifiers?.find(
      (id) => id.type === IdentifierType.TICKER
    )?.value;
    setInstrumentLabel(ticker || inst.name || inst.id);
    setInstrumentSearch("");
    setSearchResults([]);
  };

  const inputClass =
    "w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent focus:outline-none focus:ring-1 focus:ring-accent";

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <h3 className="text-lg font-semibold text-text-primary">
        {editing ? "Edit Declaration" : "New Opening Balance Declaration"}
      </h3>
      <p className="text-sm text-text-muted">
        Declare the number of units you held at a specific date. The system will
        calculate an opening balance so that your records show this quantity on the
        date you specify.
      </p>

      {error && <ErrorAlert>{error}</ErrorAlert>}

      {!editing && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
                Broker
              </label>
              <select
                value={broker}
                onChange={(e) => { setBroker(e.target.value); setAccount(""); }}
                className={inputClass}
              >
                <option value="">Select broker</option>
                {brokerAccounts.map((b) => (
                  <option key={b.broker} value={b.broker}>
                    {b.broker}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
                Account
              </label>
              <select
                value={account}
                onChange={(e) => setAccount(e.target.value)}
                className={inputClass}
                disabled={!broker}
              >
                <option value="">Select account</option>
                {accounts.map((a) => (
                  <option key={a} value={a}>
                    {a || "(default)"}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="relative">
            <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
              Instrument
            </label>
            {instrumentId ? (
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium text-text-primary">{instrumentLabel}</span>
                <button
                  type="button"
                  onClick={() => { setInstrumentId(""); setInstrumentLabel(""); }}
                  className="text-xs text-accent-dark hover:underline"
                >
                  Change
                </button>
              </div>
            ) : (
              <>
                <input
                  type="text"
                  value={instrumentSearch}
                  onChange={(e) => setInstrumentSearch(e.target.value)}
                  placeholder="Search by ticker, name, or identifier..."
                  className={inputClass}
                />
                {searchLoading && (
                  <p className="mt-1 text-xs text-text-muted">Searching...</p>
                )}
                {searchResults.length > 0 && (
                  <div className="absolute z-10 mt-1 max-h-48 w-full overflow-y-auto rounded-md border border-border bg-surface shadow-lg">
                    {searchResults.map((inst) => {
                      const ticker = inst.identifiers?.find(
                        (id) => id.type === IdentifierType.TICKER
                      )?.value;
                      return (
                        <button
                          key={inst.id}
                          type="button"
                          onClick={() => selectInstrument(inst)}
                          className="block w-full px-3 py-2 text-left text-sm hover:bg-primary-light/10"
                        >
                          <span className="font-medium text-text-primary">
                            {ticker || inst.name || inst.id}
                          </span>
                          {inst.name && ticker && (
                            <span className="ml-2 text-text-muted">{inst.name}</span>
                          )}
                        </button>
                      );
                    })}
                  </div>
                )}
              </>
            )}
          </div>
        </>
      )}

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
            Units Held
          </label>
          <input
            type="number"
            step="any"
            value={declaredQty}
            onChange={(e) => setDeclaredQty(e.target.value)}
            placeholder="e.g. 150"
            className={inputClass}
          />
        </div>
        <div>
          <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
            As Of Date
          </label>
          <input
            type="date"
            value={asOfDate}
            onChange={(e) => setAsOfDate(e.target.value)}
            className={inputClass}
          />
        </div>
      </div>

      <div className="flex gap-3">
        <button
          type="submit"
          disabled={submitting}
          className="rounded-md bg-accent px-4 py-2 text-sm font-semibold text-white transition-colors hover:bg-accent-dark disabled:opacity-50"
        >
          {submitting ? "Saving..." : editing ? "Update" : "Create"}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border border-border px-4 py-2 text-sm font-medium text-text-muted transition-colors hover:bg-primary-light/10"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}
