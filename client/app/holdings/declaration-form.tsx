"use client";

import { useState } from "react";
import { useDebounce } from "@/hooks/use-debounce";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { qk } from "@/lib/query-keys";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  listBrokersAndAccounts,
  listInstruments,
  createHoldingDeclaration,
  updateHoldingDeclaration,
  protoDateToStr,
} from "@/lib/portfolio-api";
import type { BrokerAccounts } from "@/lib/portfolio-api";
import { currentTicker, lineLabel } from "@/lib/identifiers";
import { LINE_DETAIL, lineOf, NO_CURRENCY_KNOWN } from "@/lib/listing";
import type { HoldingDeclaration, Instrument, Listing } from "@/gen/api/v1/api_pb";

function todayStr(): string {
  return new Date().toISOString().slice(0, 10);
}

/**
 * Which share count a declared quantity is denominated in. The wire carries a date;
 * the form offers the two readings that date can have, because a raw date picker
 * asks a question users cannot answer.
 */
type ShareCountBasis = "as-of" | "today";

/** The stored basis read back as one of the two choices the form offers. */
function initialBasis(decl: HoldingDeclaration): ShareCountBasis {
  const stored = decl.shareCountBasis ? protoDateToStr(decl.shareCountBasis) : "";
  const asOf = decl.asOfDate ? protoDateToStr(decl.asOfDate) : "";
  return stored && stored !== asOf ? "today" : "as-of";
}

/**
 * Ticker if the line has one, else the security's name, else the bare id, with
 * the currency line it is a quantity of. A holding is per line, so the label has
 * to say which.
 */
function instrumentDisplayLabel(decl: HoldingDeclaration): string {
  const ticker = currentTicker(decl.instrument, decl.listingId);
  const label = ticker || decl.instrument?.name || decl.instrumentId;
  return lineLabel(label, lineOf(decl.listingId, decl.instrument).currency);
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
  const [broker, setBroker] = useState(editing?.broker ?? "");
  const [account, setAccount] = useState(editing?.account ?? "");
  // The instrument the user picked, if any. Everything about the instrument is
  // derived from this or from the `editing` prop, so no effect copies the prop
  // into state.
  const [picked, setPicked] = useState<{
    id: string;
    label: string;
    listings: Listing[];
  } | null>(null);
  // Which line of the picked security the quantity is a quantity of. Empty is a
  // caller that has not said, which the server settles to the sole line and to
  // no line where the security has several.
  const [listingId, setListingId] = useState("");
  const [declaredQty, setDeclaredQty] = useState(editing?.declaredQty ?? "");
  const [asOfDate, setAsOfDate] = useState(editing?.asOfDate ? protoDateToStr(editing.asOfDate) : todayStr());
  // Which share count the quantity is in. A number copied off a statement of the
  // as-of date is in the share count current then; one copied off today's holdings
  // screen is in today's. A split between the two dates makes them differ by the
  // split factor, so the user says which rather than the system guessing.
  const [basis, setBasis] = useState<ShareCountBasis>(
    editing ? initialBasis(editing) : "as-of"
  );
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Instrument search
  const [instrumentSearch, setInstrumentSearch] = useState("");
  const debouncedInstrumentSearch = useDebounce(instrumentSearch);

  const { data: brokerAccounts = [] } = useAuthedQuery<BrokerAccounts[]>({
    queryKey: qk.brokersAndAccounts(),
    queryFn: listBrokersAndAccounts,
  });

  // Each search term is its own cache entry, so a late reply cannot overwrite
  // the results for a newer term -- which is what the cancelled flag was for.
  const { data: searchResults = [], isFetching: searchLoading } = useAuthedQuery({
    queryKey: qk.instruments(debouncedInstrumentSearch, ""),
    queryFn: () => listInstruments({ search: debouncedInstrumentSearch }),
    select: (res) => res.instruments,
    enabled: debouncedInstrumentSearch.length >= 2,
  });

  const instrumentId = picked?.id ?? editing?.instrumentId ?? "";
  const instrumentLabel =
    picked?.label ?? (editing?.instrument ? instrumentDisplayLabel(editing) : "");

  // The lines the picked security has, and the currency of the one chosen. Only
  // a new checkpoint picks: editing declares nothing new, and the update request
  // carries no line for the same reason.
  const lines = picked?.listings ?? [];
  const pickedCurrency = lines.find((l) => l.id === listingId)?.currency ?? "";

  const accounts = brokerAccounts.find((b) => b.broker === broker)?.accounts ?? [];

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!broker || !instrumentId || !declaredQty || !asOfDate) {
      setError("All fields are required.");
      return;
    }
    setSubmitting(true);
    const shareCountBasis = basis === "today" ? todayStr() : asOfDate;
    try {
      if (editing) {
        await updateHoldingDeclaration({
          id: editing.id,
          declaredQty,
          asOfDate,
          shareCountBasis,
        });
      } else {
        await createHoldingDeclaration({
          broker,
          account,
          instrumentId,
          declaredQty,
          asOfDate,
          shareCountBasis,
          listingId,
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
    const ticker = currentTicker(inst);
    setPicked({ id: inst.id, label: ticker || inst.name || inst.id, listings: inst.listings });
    // A security quoted in one currency has one answer, so the form states it
    // rather than asking. Several, and the user says which; none, and there is
    // no line to declare against.
    setListingId(inst.listings.length === 1 ? inst.listings[0].id : "");
    // Clearing the term disables the search query, so the results go with it.
    setInstrumentSearch("");
  };

  const inputClass =
    "w-full rounded-md border border-border bg-background px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-accent focus:outline-hidden focus:ring-1 focus:ring-accent";

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <h3 className="text-lg font-semibold text-text-primary">
        {editing ? "Edit Checkpoint" : "New Checkpoint"}
      </h3>
      <p className="text-sm text-text-muted">
        Enter the number of units you held at a specific date. If this is the earliest
        checkpoint for the holding, the system calculates an opening balance so that your
        records show this quantity on that date. If it is a later one, it is checked
        against what your transactions add up to.
      </p>

      {error && <ErrorAlert>{error}</ErrorAlert>}

      {/* What is being edited, including the line: the quantity is a quantity of
          one currency line, and a checkpoint cannot be moved to another one. */}
      {editing && (
        <p data-testid="declaration-editing" className="text-sm text-text-primary">
          <span className="font-medium">{instrumentDisplayLabel(editing)}</span>
          <span className="text-text-muted">
            {" "}
            at {editing.broker}
            {editing.account ? ` / ${editing.account}` : ""}
          </span>
        </p>
      )}

      {!editing && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
                Broker
              </label>
              <select
                data-testid="declaration-broker"
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
                data-testid="declaration-account"
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
                <span className="text-sm font-medium text-text-primary">
                  {lineLabel(instrumentLabel, pickedCurrency)}
                </span>
                <button
                  type="button"
                  onClick={() => {
                    setPicked({ id: "", label: "", listings: [] });
                    setListingId("");
                  }}
                  className="text-xs text-accent-dark hover:underline"
                >
                  Change
                </button>
              </div>
            ) : (
              <>
                <input
                  data-testid="declaration-instrument-search"
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
                      const ticker = currentTicker(inst);
                      return (
                        <button
                          key={inst.id}
                          data-testid="declaration-instrument-option"
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

          {/* Which currency line, where the security has more than one. A
              holding is per line -- two lines of one security are an FX rate
              apart -- so the two are declared separately rather than added. */}
          {instrumentId && lines.length > 1 && (
            <div>
              <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
                Currency Line
              </label>
              <select
                data-testid="declaration-listing"
                value={listingId}
                onChange={(e) => setListingId(e.target.value)}
                className={inputClass}
              >
                <option value="">Not stated</option>
                {lines.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.currency}
                  </option>
                ))}
              </select>
              <p className="mt-1 text-xs text-text-muted">
                {listingId
                  ? "The quantity is a quantity of this line."
                  : "Left unstated, the checkpoint sits on no line and is checked only against transactions that name none either."}
              </p>
            </div>
          )}
          {instrumentId && lines.length === 0 && (
            <p data-testid="declaration-no-line" className="text-xs text-text-muted">
              {LINE_DETAIL[NO_CURRENCY_KNOWN]}
            </p>
          )}
        </>
      )}

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
            Units Held
          </label>
          <input
            data-testid="declaration-qty"
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
            data-testid="declaration-as-of-date"
            type="date"
            value={asOfDate}
            onChange={(e) => setAsOfDate(e.target.value)}
            className={inputClass}
          />
        </div>
      </div>

      <fieldset>
        <legend className="mb-1 block text-xs font-semibold uppercase tracking-wider text-text-muted">
          Share Count
        </legend>
        <p className="mb-2 text-sm text-text-muted">
          If the instrument has split since the as of date, the same holding is a
          different number of shares before and after. Tell us which one you counted.
        </p>
        <div className="space-y-1">
          {(
            [
              ["as-of", "The share count on the as of date (from a statement or contract note)"],
              ["today", "Today's share count (from a current holdings screen)"],
            ] as const
          ).map(([value, label]) => (
            <label key={value} className="flex items-start gap-2 text-sm text-text-primary">
              <input
                type="radio"
                name="share-count-basis"
                value={value}
                checked={basis === value}
                onChange={() => setBasis(value)}
                className="mt-0.5"
              />
              <span>{label}</span>
            </label>
          ))}
        </div>
      </fieldset>

      <div className="flex gap-3">
        <button
          data-testid="declaration-submit"
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
