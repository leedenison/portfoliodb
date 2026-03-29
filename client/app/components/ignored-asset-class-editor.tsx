"use client";

import { useCallback, useEffect, useState } from "react";
import { ErrorAlert } from "@/app/components/error-alert";
import {
  getIgnoredAssetClasses,
  setIgnoredAssetClasses,
  countIgnoredTxs,
  listBrokersAndAccounts,
} from "@/lib/portfolio-api";
import type { IgnoredAssetClassRule, BrokerAccounts } from "@/lib/portfolio-api";

// Asset classes that have tx type mappings (selectable for ignoring).
const IGNORABLE_ASSET_CLASSES = [
  "CASH",
  "STOCK",
  "OPTION",
  "FUTURE",
  "FIXED_INCOME",
  "MUTUAL_FUND",
  "UNKNOWN",
] as const;

const ASSET_CLASS_LABELS: Record<string, string> = {
  CASH: "Cash",
  STOCK: "Stock",
  OPTION: "Option",
  FUTURE: "Future",
  FIXED_INCOME: "Fixed Income",
  MUTUAL_FUND: "Mutual Fund",
  UNKNOWN: "Other",
};

// Key for a rule: "broker" or "broker:account"
function ruleKey(broker: string, account: string, assetClass: string): string {
  return `${broker}:${account}:${assetClass}`;
}

function parseRuleKey(key: string): { broker: string; account: string; assetClass: string } {
  const parts = key.split(":");
  return { broker: parts[0], account: parts[1], assetClass: parts[2] };
}

function rulesToKeySet(rules: IgnoredAssetClassRule[]): Set<string> {
  return new Set(rules.map((r) => ruleKey(r.broker, r.account, r.assetClass)));
}

function keySetToRules(keys: Set<string>): IgnoredAssetClassRule[] {
  return Array.from(keys).map((k) => {
    const { broker, account, assetClass } = parseRuleKey(k);
    return { broker, account, assetClass };
  });
}

interface Props {
  authStatus: string;
}

export function IgnoredAssetClassEditor({ authStatus }: Props) {
  const [brokerAccounts, setBrokerAccounts] = useState<BrokerAccounts[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [savedKeys, setSavedKeys] = useState<Set<string>>(new Set());
  const [editKeys, setEditKeys] = useState<Set<string>>(new Set());
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [confirmDialog, setConfirmDialog] = useState<{
    txCount: number;
    declarationCount: number;
    rules: IgnoredAssetClassRule[];
  } | null>(null);

  const hasChanges = !setsEqual(savedKeys, editKeys);

  const loadData = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [ba, rules] = await Promise.all([
        listBrokersAndAccounts(),
        getIgnoredAssetClasses(),
      ]);
      setBrokerAccounts(ba);
      const keys = rulesToKeySet(rules);
      setSavedKeys(keys);
      setEditKeys(new Set(keys));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (authStatus !== "authenticated") return;
    loadData();
  }, [authStatus, loadData]);

  // Check if a broker-level ignore is active for the given asset class.
  function isBrokerIgnored(broker: string, assetClass: string): boolean {
    return editKeys.has(ruleKey(broker, "", assetClass));
  }

  // Check if an account-level ignore is active.
  function isAccountIgnored(broker: string, account: string, assetClass: string): boolean {
    return editKeys.has(ruleKey(broker, account, assetClass));
  }

  // Effective state: account is ignored if broker-level OR account-level is set.
  function isEffectivelyIgnored(broker: string, account: string, assetClass: string): boolean {
    return isBrokerIgnored(broker, assetClass) || isAccountIgnored(broker, account, assetClass);
  }

  // Toggle broker-level ignore for an asset class.
  function toggleBrokerAssetClass(broker: string, assetClass: string) {
    setEditKeys((prev) => {
      const next = new Set(prev);
      const brokerKey = ruleKey(broker, "", assetClass);
      if (next.has(brokerKey)) {
        // Remove broker-level rule.
        next.delete(brokerKey);
        // Also remove any account-level rules for this broker+assetClass.
        const ba = brokerAccounts.find((b) => b.broker === broker);
        if (ba) {
          for (const acct of ba.accounts) {
            next.delete(ruleKey(broker, acct, assetClass));
          }
        }
      } else {
        // Add broker-level rule and remove any account-level rules (broker-level covers all).
        next.add(brokerKey);
        const ba = brokerAccounts.find((b) => b.broker === broker);
        if (ba) {
          for (const acct of ba.accounts) {
            next.delete(ruleKey(broker, acct, assetClass));
          }
        }
      }
      return next;
    });
  }

  // Toggle account-level ignore for an asset class.
  function toggleAccountAssetClass(broker: string, account: string, assetClass: string) {
    setEditKeys((prev) => {
      const next = new Set(prev);
      const brokerKey = ruleKey(broker, "", assetClass);
      const accountKey = ruleKey(broker, account, assetClass);

      if (next.has(brokerKey)) {
        // "Explode" broker-level: remove broker rule, add account rules for all OTHER accounts.
        next.delete(brokerKey);
        const ba = brokerAccounts.find((b) => b.broker === broker);
        if (ba) {
          for (const acct of ba.accounts) {
            if (acct !== account) {
              next.add(ruleKey(broker, acct, assetClass));
            }
          }
        }
        // The toggled account is now UN-ignored (we removed broker-level and didn't add its account rule).
      } else if (next.has(accountKey)) {
        // Remove account-level rule.
        next.delete(accountKey);
      } else {
        // Add account-level rule.
        next.add(accountKey);
        // Check if all accounts are now ignored -> consolidate to broker-level.
        const ba = brokerAccounts.find((b) => b.broker === broker);
        if (ba) {
          const allIgnored = ba.accounts.every((acct) =>
            acct === account ? true : next.has(ruleKey(broker, acct, assetClass))
          );
          if (allIgnored) {
            // Consolidate to broker-level.
            next.add(brokerKey);
            for (const acct of ba.accounts) {
              next.delete(ruleKey(broker, acct, assetClass));
            }
          }
        }
      }
      return next;
    });
  }

  async function handleSave() {
    const rules = keySetToRules(editKeys);
    setSaving(true);
    setError(null);
    try {
      const { txCount, declarationCount } = await countIgnoredTxs(rules);
      if (txCount > 0 || declarationCount > 0) {
        setConfirmDialog({ txCount, declarationCount, rules });
        setSaving(false);
        return;
      }
      await doSave(rules);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setSaving(false);
    }
  }

  async function doSave(rules: IgnoredAssetClassRule[]) {
    setSaving(true);
    setError(null);
    try {
      await setIgnoredAssetClasses(rules);
      const keys = rulesToKeySet(rules);
      setSavedKeys(keys);
      setEditKeys(new Set(keys));
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
      setConfirmDialog(null);
    }
  }

  function handleCancel() {
    setEditKeys(new Set(savedKeys));
  }

  if (loading) {
    return (
      <div className="rounded-lg border border-border bg-surface p-5">
        <p className="text-sm text-text-muted">Loading...</p>
      </div>
    );
  }

  return (
    <>
      <div className="rounded-lg border border-border bg-surface p-5">
        <h3 className="text-sm font-medium text-text-primary">
          Ignored Transaction Types
        </h3>
        <p className="mt-1 text-xs text-text-muted">
          Transactions of ignored asset classes will be skipped during upload and
          existing matching transactions will be deleted.
        </p>

        {error && <ErrorAlert>{error}</ErrorAlert>}

        {brokerAccounts.length === 0 ? (
          <p className="mt-3 text-sm text-text-muted">
            No brokers or accounts found. Upload transactions first.
          </p>
        ) : (
          <div className="mt-3 space-y-4">
            {brokerAccounts.map((ba) => (
              <BrokerSection
                key={ba.broker}
                broker={ba.broker}
                accounts={ba.accounts}
                isBrokerIgnored={isBrokerIgnored}
                isAccountIgnored={isAccountIgnored}
                isEffectivelyIgnored={isEffectivelyIgnored}
                onToggleBroker={toggleBrokerAssetClass}
                onToggleAccount={toggleAccountAssetClass}
              />
            ))}
          </div>
        )}

        {brokerAccounts.length > 0 && (
          <div className="mt-4 flex items-center gap-3">
            <button
              onClick={handleSave}
              disabled={saving || !hasChanges}
              className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-primary-dark disabled:opacity-50"
            >
              {saving ? "Saving..." : "Save"}
            </button>
            {hasChanges && (
              <button
                onClick={handleCancel}
                disabled={saving}
                className="rounded-md border border-border px-3 py-1.5 text-sm text-text-primary transition-colors hover:bg-surface-hover"
              >
                Cancel
              </button>
            )}
            {saved && (
              <span className="text-xs text-green-600 dark:text-green-400">
                Saved
              </span>
            )}
          </div>
        )}
      </div>

      {confirmDialog && (
        <ConfirmDialog
          txCount={confirmDialog.txCount}
          declarationCount={confirmDialog.declarationCount}
          onConfirm={() => doSave(confirmDialog.rules)}
          onCancel={() => {
            setConfirmDialog(null);
            setSaving(false);
          }}
        />
      )}
    </>
  );
}

// --- Broker section with expandable accounts ---

function BrokerSection({
  broker,
  accounts,
  isBrokerIgnored,
  isAccountIgnored,
  isEffectivelyIgnored,
  onToggleBroker,
  onToggleAccount,
}: {
  broker: string;
  accounts: string[];
  isBrokerIgnored: (broker: string, assetClass: string) => boolean;
  isAccountIgnored: (broker: string, account: string, assetClass: string) => boolean;
  isEffectivelyIgnored: (broker: string, account: string, assetClass: string) => boolean;
  onToggleBroker: (broker: string, assetClass: string) => void;
  onToggleAccount: (broker: string, account: string, assetClass: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);

  return (
    <div className="rounded-md border border-border/50">
      <div className="px-3 py-2">
        <button
          onClick={() => setExpanded(!expanded)}
          className="flex w-full items-center gap-2 text-left"
        >
          <span className="text-xs text-text-muted">{expanded ? "\u25BC" : "\u25B6"}</span>
          <span className="text-sm font-semibold text-text-primary">{broker}</span>
        </button>
        <div className="ml-5 mt-1 flex flex-wrap gap-x-4 gap-y-1">
          {IGNORABLE_ASSET_CLASSES.map((ac) => (
            <label
              key={ac}
              className="flex items-center gap-1.5 cursor-pointer"
            >
              <input
                type="checkbox"
                checked={isBrokerIgnored(broker, ac)}
                onChange={() => onToggleBroker(broker, ac)}
                className="rounded border-border text-primary focus:ring-primary/30"
              />
              <span className="text-xs text-text-primary">
                {ASSET_CLASS_LABELS[ac]}
              </span>
            </label>
          ))}
        </div>
      </div>

      {expanded && accounts.length > 0 && (
        <div className="border-t border-border/50 px-3 py-2">
          <div className="ml-5 space-y-2">
            {accounts.map((acct) => (
              <div key={acct}>
                <p className="text-xs font-medium text-text-muted">{acct}</p>
                <div className="mt-0.5 flex flex-wrap gap-x-4 gap-y-1">
                  {IGNORABLE_ASSET_CLASSES.map((ac) => (
                    <label
                      key={ac}
                      className="flex items-center gap-1.5 cursor-pointer"
                    >
                      <input
                        type="checkbox"
                        checked={isEffectivelyIgnored(broker, acct, ac)}
                        onChange={() => onToggleAccount(broker, acct, ac)}
                        className="rounded border-border text-primary focus:ring-primary/30"
                      />
                      <span className="text-xs text-text-primary">
                        {ASSET_CLASS_LABELS[ac]}
                      </span>
                    </label>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// --- Confirmation dialog ---

function ConfirmDialog({
  txCount,
  declarationCount,
  onConfirm,
  onCancel,
}: {
  txCount: number;
  declarationCount: number;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="w-full max-w-md rounded-lg border border-border bg-surface p-6 shadow-lg">
        <h3 className="text-lg font-semibold text-text-primary">
          Confirm Deletion
        </h3>
        <div className="mt-3 space-y-2 text-sm text-text-primary">
          {txCount > 0 && (
            <p>
              This will delete{" "}
              <span className="font-semibold">{txCount}</span> existing
              transaction{txCount !== 1 ? "s" : ""}.
            </p>
          )}
          {declarationCount > 0 && (
            <p>
              This will also delete{" "}
              <span className="font-semibold">{declarationCount}</span> holding
              declaration{declarationCount !== 1 ? "s" : ""}.
            </p>
          )}
          <p className="text-xs text-text-muted">
            This action cannot be undone. Future uploads will skip transactions
            of the ignored types.
          </p>
        </div>
        <div className="mt-4 flex justify-end gap-3">
          <button
            onClick={onCancel}
            className="rounded-md border border-border px-3 py-1.5 text-sm text-text-primary transition-colors hover:bg-surface-hover"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            className="rounded-md bg-red-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm transition-colors hover:bg-red-700"
          >
            Delete and Save
          </button>
        </div>
      </div>
    </div>
  );
}

function setsEqual(a: Set<string>, b: Set<string>): boolean {
  if (a.size !== b.size) return false;
  for (const v of a) {
    if (!b.has(v)) return false;
  }
  return true;
}
