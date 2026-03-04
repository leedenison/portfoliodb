"use client";

import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { PortfolioList } from "@/app/components/portfolio-list";

export default function PortfoliosPage() {
  const { state, authError } = useAuth();

  return (
    <AppShell>
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center text-center">
            <h1 className="text-4xl font-bold tracking-tight text-text-primary">
              Portfolios
            </h1>
            <p className="mt-3 text-text-muted">Sign in to view portfolios.</p>
            {authError && (
              <p className="mt-4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </div>
        )}
        {state.status === "authenticated" && (
          <PortfolioList />
        )}
      </div>
    </AppShell>
  );
}
