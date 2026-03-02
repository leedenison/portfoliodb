"use client";

import { AppShell } from "@/app/components/app-shell";
import { useAuth } from "@/contexts/auth-context";
import { SignInButton } from "@/app/components/sign-in";
import { PortfolioList } from "@/app/components/portfolio-list";

export default function PortfoliosPage() {
  const { state, authError } = useAuth();

  return (
    <AppShell>
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        {state.status === "loading" && (
          <p className="text-slate-500">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center">
            <h1 className="text-4xl font-bold tracking-tight text-slate-800">
              Portfolios
            </h1>
            <p className="mt-3 text-slate-600">Sign in to view portfolios.</p>
            <p className="mt-6">
              <SignInButton />
            </p>
            {authError && (
              <p className="mt-4 rounded bg-red-50 px-4 py-2 text-sm text-red-700">
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
