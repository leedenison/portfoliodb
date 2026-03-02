"use client";

import { SignInButton } from "./components/sign-in";
import { AppHeader } from "./components/app-header";
import { PortfolioList } from "./components/portfolio-list";
import { useAuth } from "@/contexts/auth-context";

export default function Home() {
  const { state, authError } = useAuth();

  return (
    <main className="flex min-h-screen flex-col">
      <AppHeader />
      <div className="flex flex-1 flex-col items-center px-4 py-8">
        {state.status === "loading" && (
          <p className="text-slate-500">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <div className="flex flex-1 flex-col items-center justify-center">
            <h1 className="text-4xl font-bold tracking-tight text-slate-800">
              Welcome to PortfolioDB
            </h1>
            <p className="mt-3 text-lg text-slate-600">
              Track holdings for equities, options and futures.
            </p>
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
    </main>
  );
}
