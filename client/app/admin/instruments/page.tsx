"use client";

import { useMemo, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import { PaginationControls } from "@/app/components/pagination-controls";
import { usePagination } from "@/hooks/use-pagination";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { listInstruments } from "@/lib/portfolio-api";
import { useDebounce } from "@/hooks/use-debounce";
import { AssetClass, IdentifierType } from "@/gen/type/v1/type_pb";
import type { Instrument, InstrumentIdentifier, Listing } from "@/gen/api/v1/api_pb";
import { ALL_ASSET_CLASSES, DEFAULT_ASSET_CLASSES, ASSET_CLASS_LABELS } from "@/lib/asset-class";
import { lineLabel } from "@/lib/identifiers";
import { venueLabel } from "@/lib/listing";

const IDENTIFIER_LABELS: Record<number, string> = {
  [IdentifierType.ISIN]: "ISIN",
  [IdentifierType.CUSIP]: "CUSIP",
  [IdentifierType.SEDOL]: "SEDOL",
  [IdentifierType.CINS]: "CINS",
  [IdentifierType.WERTPAPIER]: "Wertpapier",
  [IdentifierType.OCC]: "OCC",
  [IdentifierType.OPRA]: "OPRA",
  [IdentifierType.FUT_OPT]: "Fut/Opt",
  [IdentifierType.OPENFIGI_GLOBAL]: "FIGI Global",
  [IdentifierType.OPENFIGI_SHARE_CLASS]: "FIGI Share",
  [IdentifierType.OPENFIGI_COMPOSITE]: "FIGI Composite",
  [IdentifierType.MIC_TICKER]: "Ticker",
  [IdentifierType.OPENFIGI_TICKER]: "Ticker",
  [IdentifierType.BROKER_DESCRIPTION]: "Broker Desc",
  [IdentifierType.CURRENCY]: "Currency",
  [IdentifierType.FX_PAIR]: "FX Pair",
};

function idLabel(id: InstrumentIdentifier): string {
  return IDENTIFIER_LABELS[id.type] ?? String(id.type);
}

// A name with a closed validity interval is one the instrument has given up --
// an option's pre-split OCC symbol, say. It is kept so a file naming it still
// resolves, and shown so an administrator can see why the instrument answers to
// two symbols. See docs/adr/0055-identifier-validity-is-an-interval.md.
function isCurrent(id: InstrumentIdentifier): boolean {
  return !id.validBefore;
}

/** The currency of the line a derivative delivers, empty where it names none. */
function underlyingCurrency(inst: Instrument): string {
  return (
    inst.underlying?.listings.find((l) => l.id === inst.underlyingListingId)
      ?.currency ?? ""
  );
}

/** What is known about a line beyond its currency: where it is quoted, and when
 * it was tradeable. */
function listingTitle(l: Listing): string {
  const window = [l.validFrom ?? "", l.validBefore ?? ""].some(Boolean)
    ? `${l.validFrom ?? "always"} to ${l.validBefore ?? "now"}`
    : "";
  return [venueLabel(l.venues), window].filter(Boolean).join(" - ");
}

export default function AdminInstrumentsPage() {
  const [search, setSearch] = useState("");
  const debouncedSearch = useDebounce(search);
  const [activeClasses, setActiveClasses] = useState<Set<AssetClass>>(
    () => new Set(DEFAULT_ASSET_CLASSES)
  );

  const toggleClass = (cls: AssetClass) => {
    setActiveClasses((prev) => {
      const next = new Set(prev);
      if (next.has(cls)) next.delete(cls);
      else next.add(cls);
      return next;
    });
  };

  // Sorted join so the key is stable when the set's contents have not changed.
  const assetClassesKey = useMemo(
    () => [...activeClasses].sort().join(","),
    [activeClasses]
  );

  return (
    <div className="space-y-5">
      <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
        Instruments
      </h2>

      <div className="flex flex-wrap items-end gap-3">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search by name or ticker..."
          className="w-full max-w-sm rounded-md border border-border bg-surface px-3 py-2 text-sm text-text-primary placeholder:text-text-muted focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary/30"
        />
      </div>

      <div className="space-y-3">
        <div className="flex flex-wrap gap-1.5">
          {ALL_ASSET_CLASSES.map((cls) => {
            const active = activeClasses.has(cls);
            return (
              <button
                key={cls}
                type="button"
                onClick={() => toggleClass(cls)}
                className={
                  "rounded-md border px-2.5 py-1 text-xs font-medium transition-colors " +
                  (active
                    ? "border-primary bg-primary-dark/10 text-primary-dark"
                    : "border-border bg-surface text-text-muted hover:bg-primary-light/15")
                }
              >
                {ASSET_CLASS_LABELS[cls] || String(cls)}
              </button>
            );
          })}
        </div>
      </div>

      {/* Keyed on the filters: a change remounts the list, which returns it to
          page 1 and collapses any expanded row. */}
      <InstrumentList
        key={`${debouncedSearch}|${assetClassesKey}`}
        search={debouncedSearch}
        assetClassesKey={assetClassesKey}
      />
    </div>
  );
}

function InstrumentList({
  search,
  assetClassesKey,
}: {
  search: string;
  assetClassesKey: string;
}) {
  const [expandedId, setExpandedId] = useState<string | null>(null);

  const {
    items: instruments,
    totalCount,
    loading,
    error,
    pageIndex,
    hasPrev,
    hasNext,
    goNext,
    goPrev,
  } = usePagination<Instrument>({
    queryKey: qk.instruments(search, assetClassesKey),
    queryFn: async (pageToken) => {
      const classes = assetClassesKey
        ? (assetClassesKey.split(",").map(Number) as AssetClass[])
        : [];
      const result = await listInstruments({
        search,
        assetClasses: classes.length < ALL_ASSET_CLASSES.length ? classes : [],
        pageToken,
      });
      return {
        items: result.instruments,
        totalCount: result.totalCount,
        nextPageToken: result.nextPageToken,
      };
    },
  });

  return (
    <>
      {!loading && (
        <span className="block font-mono text-xs text-text-muted">{totalCount} total</span>
      )}
      {loading && (
        <p className="text-text-muted">Loading instruments...</p>
      )}
      {!loading && error && <ErrorAlert>{errorMessage(error)}</ErrorAlert>}
      {!loading && !error && (
        <>
          <div className="overflow-x-auto rounded-md border border-border bg-surface shadow-xs">
            <table className="w-full min-w-[480px] border-collapse text-sm">
              <thead>
                <tr className="border-b-2 border-primary-dark/10 bg-primary-dark/3">
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Name
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Asset Class
                  </th>
                  {/* One column for the lines rather than one exchange and one
                      currency: currency is a fact about a line and a security
                      quoted in two has two of them. */}
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Listings
                  </th>
                  <th className="px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-muted">
                    Status
                  </th>
                </tr>
              </thead>
              <tbody>
                {instruments.length === 0 ? (
                  <tr>
                    <td
                      colSpan={4}
                      className="px-4 py-8 text-center text-text-muted"
                    >
                      {search
                        ? "No instruments match your search."
                        : "No instruments in the system yet."}
                    </td>
                  </tr>
                ) : (
                  instruments.map((inst) => {
                    const identified = inst.identified;
                    const expanded = expandedId === inst.id;
                    return (
                      <tr
                        key={inst.id}
                        data-testid="instrument-row"
                        data-instrument-name={inst.name}
                        className="group cursor-pointer border-b border-border/40 transition-colors last:border-0 hover:bg-primary-light/10"
                        onClick={() =>
                          setExpandedId(expanded ? null : inst.id)
                        }
                      >
                        <td
                          className="px-4 py-3 font-medium text-text-primary"
                          colSpan={expanded ? 4 : 1}
                        >
                          {expanded ? (
                            <ExpandedDetail inst={inst} />
                          ) : (
                            (inst.name || inst.id)
                          )}
                        </td>
                        {!expanded && (
                          <>
                            <td className="px-4 py-3 text-text-muted">
                              {ASSET_CLASS_LABELS[inst.assetClass] || "\u2014"}
                            </td>
                            <td className="px-4 py-3">
                              <ListingChips inst={inst} />
                            </td>
                            <td className="px-4 py-3">
                              <span
                                className={
                                  "inline-block rounded-sm px-1.5 py-0.5 text-xs font-medium " +
                                  (identified
                                    ? "bg-primary-dark/10 text-primary-dark"
                                    : "bg-accent-soft/60 text-accent-dark")
                                }
                              >
                                {identified
                                  ? "Identified"
                                  : "Unidentified"}
                              </span>
                            </td>
                          </>
                        )}
                      </tr>
                    );
                  })
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

/**
 * The security's currency lines, disclosed by their currency. A security with
 * none says so: nothing has stated a currency for it, so it has no line to be
 * quoted on and nothing it holds can be priced.
 */
function ListingChips({ inst }: { inst: Instrument }) {
  if (inst.listings.length === 0) {
    return (
      <span
        data-testid="instrument-no-listing"
        className="inline-block rounded-sm bg-accent-soft/60 px-1.5 py-0.5 text-xs font-medium text-accent-dark"
      >
        No currency line
      </span>
    );
  }
  return (
    <span className="flex flex-wrap gap-1">
      {inst.listings.map((l) => (
        <span
          key={l.id}
          data-testid="instrument-listing"
          data-listing-currency={l.currency}
          title={listingTitle(l)}
          className="inline-block rounded-sm bg-primary-dark/10 px-1.5 py-0.5 font-mono text-xs text-primary-dark"
        >
          {l.currency}
        </span>
      ))}
    </span>
  );
}

function ExpandedDetail({ inst }: { inst: Instrument }) {
  const identified = inst.identified;
  const brokerDescs = inst.identifiers.filter(
    (id) => id.type === IdentifierType.BROKER_DESCRIPTION
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-display text-base font-bold tracking-tight text-text-primary">
          {(inst.name || inst.id)}
        </span>
        <span
          className={
            "inline-block rounded-sm px-1.5 py-0.5 text-xs font-medium " +
            (identified
              ? "bg-primary-dark/10 text-primary-dark"
              : "bg-accent-soft/60 text-accent-dark")
          }
        >
          {identified ? "Identified" : "Unidentified"}
        </span>
      </div>

      {/* Metadata row */}
      <div className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-text-muted">
        {inst.assetClass !== AssetClass.ASSET_CLASS_UNSPECIFIED && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Class
            </span>{" "}
            {ASSET_CLASS_LABELS[inst.assetClass]}
          </span>
        )}
        <span className="flex items-center gap-1.5">
          <span className="font-semibold uppercase tracking-wider">Lines</span>{" "}
          <ListingChips inst={inst} />
        </span>
        {inst.underlyingId && (
          <span>
            <span className="font-semibold uppercase tracking-wider">
              Underlying
            </span>{" "}
            {/* The line the contract delivers, not just the security: a strike is
                a price and a price is in a currency, so a deliverable in the USD
                line is a different contract from one in the GBP line. */}
            <span>
              {lineLabel(
                inst.underlying?.name || inst.underlyingId,
                underlyingCurrency(inst)
              )}
            </span>
          </span>
        )}
      </div>

      {/* Identifiers, one block per grain. A name that names the security is a
          different claim from one that names a line of it, and the flat list
          this replaced could not say which a row was. */}
      <IdentifierBlock heading="Names the security" ids={inst.identifiers} />
      {inst.listings.map((l) => (
        <IdentifierBlock
          key={l.id}
          heading={`Names the ${l.currency} line`}
          headingTitle={listingTitle(l)}
          ids={l.identifiers}
          empty="No names on this line"
        />
      ))}
      <IdentifierBlock
        heading="Names no line"
        headingTitle="Listing-grain names from a result that stated no currency"
        ids={inst.unplacedIdentifiers}
      />

      {/* Broker descriptions */}
      {brokerDescs.length > 0 && (
        <div className="space-y-1">
          <h4 className="text-xs font-semibold uppercase tracking-wider text-text-muted">
            Broker Descriptions
          </h4>
          <div className="flex flex-wrap gap-1.5">
            {brokerDescs.map((id) => (
              <span
                key={`${id.domain}-${id.value}`}
                className="inline-flex items-center gap-1 rounded-sm bg-accent-soft/30 px-1.5 py-0.5 font-mono text-xs"
              >
                <span className="text-text-primary">{id.value}</span>
                {id.domain && (
                  <span className="text-text-muted">({id.domain})</span>
                )}
              </span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * One grain's canonical names, current ones first and then the ones the
 * instrument has given up, most recent first. A name with a closed validity
 * interval is kept so a file naming it still resolves, and shown so an
 * administrator can see why the instrument answers to two symbols. See
 * docs/adr/0055-identifier-validity-is-an-interval.md.
 *
 * A block with nothing in it and nothing to say about that is not drawn: an
 * instrument holds no names at most grains, and an empty heading per grain would
 * be most of the panel.
 */
function IdentifierBlock({
  heading,
  headingTitle,
  ids,
  empty,
}: {
  heading: string;
  headingTitle?: string;
  ids: InstrumentIdentifier[];
  empty?: string;
}) {
  const canonical = ids
    .filter((id) => id.canonical)
    .sort((a, b) =>
      isCurrent(a) === isCurrent(b)
        ? (b.validBefore ?? "").localeCompare(a.validBefore ?? "")
        : Number(isCurrent(b)) - Number(isCurrent(a))
    );
  if (canonical.length === 0 && !empty) return null;

  return (
    <div className="space-y-1">
      <h4
        title={headingTitle || undefined}
        className="text-xs font-semibold uppercase tracking-wider text-text-muted"
      >
        {heading}
      </h4>
      {canonical.length === 0 ? (
        <p className="text-xs text-text-muted">{empty}</p>
      ) : (
        <div className="flex flex-wrap gap-1.5">
          {canonical.map((id) => (
            <span
              key={`${id.type}-${id.value}-${id.validFrom ?? ""}`}
              data-testid="instrument-identifier"
              data-identifier-type={idLabel(id)}
              data-identifier-current={isCurrent(id) ? "true" : "false"}
              title={
                isCurrent(id)
                  ? undefined
                  : `No longer in force from ${id.validBefore}`
              }
              className={
                "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 font-mono text-xs " +
                (isCurrent(id)
                  ? "bg-primary-dark/10"
                  : "bg-text-muted/10 opacity-60")
              }
            >
              <span
                className={
                  "font-semibold " +
                  (isCurrent(id) ? "text-primary-dark" : "text-text-muted")
                }
              >
                {idLabel(id)}
              </span>
              <span className="text-text-primary">{id.value}</span>
              {id.domain && (
                <span className="text-text-muted">({id.domain})</span>
              )}
              {!isCurrent(id) && (
                <span className="text-text-muted">until {id.validBefore}</span>
              )}
            </span>
          ))}
        </div>
      )}
    </div>
  );
}
