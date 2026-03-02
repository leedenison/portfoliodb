"use client";

import { SignInButton } from "./components/sign-in";
import { UserMenu } from "./components/user-menu";
import { useAuth } from "@/contexts/auth-context";

export default function Home() {
  const { authError } = useAuth();
  return (
    <main className="flex min-h-screen flex-col">
      <header className="flex items-center justify-end gap-4 border-b border-slate-200 bg-white px-4 py-3">
        <UserMenu />
        <SignInButton />
      </header>
      <div className="flex flex-1 flex-col items-center justify-center px-4">
        <h1 className="text-4xl font-bold tracking-tight text-slate-800">
          PortfolioDB
        </h1>
        <p className="mt-3 text-lg text-slate-600">
          Track holdings for equities, options and futures.
        </p>
        {authError && (
          <p className="mt-4 rounded bg-red-50 px-4 py-2 text-sm text-red-700">
            {authError}
          </p>
        )}
      </div>
    </main>
  );
}
