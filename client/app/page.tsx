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
    <main className="flex min-h-screen flex-col bg-background">
      <AppHeader />
      <div className="flex flex-1 flex-col items-center px-4 py-12 md:py-20">
        {state.status === "loading" && (
          <p className="text-text-muted">Loading…</p>
        )}
        {state.status === "unauthenticated" && (
          <section className="flex max-w-2xl flex-col items-center text-center">
            <div className="mb-6 flex justify-center">
              <Image
                src="/logo.png"
                alt=""
                width={80}
                height={80}
                className="h-20 w-20 object-contain"
              />
            </div>
            <h1 className="text-4xl font-bold tracking-tight text-text-primary md:text-5xl">
              Portfolio tracking for equities, options and futures
            </h1>
            <p className="mt-4 text-lg text-text-muted">
              Manage holdings, upload transactions, and keep your portfolios in one place.
            </p>
            {authError && (
              <p className="mt-6 rounded-lg bg-accent-soft/50 px-4 py-2 text-sm text-accent-dark">
                {authError}
              </p>
            )}
          </section>
        )}
      </div>
    </main>
  );
}
