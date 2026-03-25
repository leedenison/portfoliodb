"use client";

import { useEffect } from "react";
import Image from "next/image";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AppHeader } from "./components/app-header";
import { SignInButton } from "./components/sign-in";
import { useAuth } from "@/contexts/auth-context";

export default function Home() {
  const router = useRouter();
  const { state, authError } = useAuth();

  useEffect(() => {
    if (state.status === "authenticated") {
      router.replace("/holdings");
    }
  }, [state.status, router]);

  if (state.status === "authenticated") {
    return (
      <main className="flex min-h-screen flex-col bg-background">
        <AppHeader />
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-text-muted">Loading…</p>
        </div>
      </main>
    );
  }

  return (
    <main data-testid="page-signin" className="flex min-h-screen flex-col bg-background">
      <AppHeader />
      <div className="bg-grid relative flex flex-1 flex-col items-center px-4 py-16 md:py-24">
        {/* Gradient fade over grid */}
        <div className="pointer-events-none absolute inset-0 bg-gradient-to-b from-background via-background/80 to-background" />

        {state.status === "loading" && (
          <p className="relative text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <section className="relative flex max-w-2xl flex-col items-center text-center">
            <div className="mb-8 flex animate-fade-in justify-center">
              <Image
                src="/logo.png"
                alt=""
                width={80}
                height={80}
                className="h-20 w-20 object-contain"
              />
            </div>
            <h1 className="animate-fade-in-d1 font-display text-4xl font-bold tracking-tight text-text-primary md:text-5xl lg:text-6xl">
              Portfolio tracking for equities, options and futures
            </h1>
            <p className="mt-5 animate-fade-in-d2 text-lg text-text-muted">
              Manage holdings, upload transactions, and keep your portfolios in one place.
            </p>
            <div className="mt-8 animate-fade-in-d3">
              <SignInButton />
            </div>
            {authError && (
              <p className="mt-6 animate-fade-in-d4 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </section>
        )}
      </div>
    </main>
  );
}
