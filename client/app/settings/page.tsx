"use client";

import { useState } from "react";
import { AppShell } from "@/app/components/app-shell";
import { ErrorAlert } from "@/app/components/error-alert";
import { useAuth } from "@/contexts/auth-context";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthedQuery } from "@/hooks/use-authed-query";
import { errorMessage } from "@/lib/errors";
import { qk } from "@/lib/query-keys";
import { getDisplayCurrency, setDisplayCurrency } from "@/lib/portfolio-api";

const CURRENCIES = [
  "USD", "EUR", "GBP", "JPY", "CHF", "CAD", "AUD", "NZD",
  "SEK", "NOK", "DKK", "HKD", "SGD", "KRW", "INR", "CNY",
  "BRL", "MXN", "ZAR", "TRY", "PLN", "CZK", "HUF",
];

export default function SettingsPage() {
  const { state } = useAuth();
  const queryClient = useQueryClient();
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  // Shared with the performance page, which needs the same value to label its
  // chart -- one request between them rather than two.
  const { data: currency = "", error: loadError } = useAuthedQuery<string>({
    queryKey: qk.displayCurrency(),
    queryFn: getDisplayCurrency,
  });
  const error = saveError ?? (loadError ? errorMessage(loadError) : null);

  const handleChange = async (newCurrency: string) => {
    setSaving(true);
    setSaveError(null);
    setSaved(false);
    try {
      const result = await setDisplayCurrency(newCurrency);
      queryClient.setQueryData(qk.displayCurrency(), result);
      setSaved(true);
      setTimeout(() => setSaved(false), 2000);
    } catch (e) {
      setSaveError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <AppShell>
      <div data-testid="page-settings" className="flex flex-1 flex-col px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading...</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="font-display text-4xl font-bold tracking-tight text-text-primary">
              Settings
            </h1>
            <p className="mt-3 text-text-muted">Sign in to manage settings.</p>
          </div>
        )}
        {state.status === "authenticated" && (
          <div className="mx-auto w-full max-w-xl animate-fade-in space-y-6">
            <h2 className="font-display text-2xl font-bold tracking-tight text-text-primary">
              Settings
            </h2>

            {error && <ErrorAlert>{error}</ErrorAlert>}

            <div className="rounded-lg border border-border bg-surface p-5">
              <label
                htmlFor="display-currency"
                className="block text-sm font-medium text-text-primary"
              >
                Display Currency
              </label>
              <p className="mt-1 text-xs text-text-muted">
                Portfolio values and performance charts will be converted to this
                currency.
              </p>
              <div className="mt-3 flex items-center gap-3">
                <select
                  id="display-currency"
                  value={currency}
                  onChange={(e) => handleChange(e.target.value)}
                  disabled={saving}
                  className="rounded-md border border-border bg-white px-3 py-2 text-sm text-text-primary shadow-xs transition-colors hover:border-primary-light focus:border-primary focus:outline-hidden focus:ring-1 focus:ring-primary dark:bg-surface"
                >
                  {CURRENCIES.map((c) => (
                    <option key={c} value={c}>
                      {c}
                    </option>
                  ))}
                </select>
                {saving && (
                  <span className="text-xs text-text-muted">Saving...</span>
                )}
                {saved && (
                  <span className="text-xs text-green-600 dark:text-green-400">
                    Saved
                  </span>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </AppShell>
  );
}
