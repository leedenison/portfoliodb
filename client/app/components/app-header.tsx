"use client";

import Link from "next/link";
import Image from "next/image";
import { SignInButton } from "./sign-in";
import { UserMenu } from "./user-menu";
import { PortfolioSelectorChip } from "./portfolio-selector-chip";
import { PortfolioSelectorModal } from "./portfolio-selector-modal";
import { useAuth } from "@/contexts/auth-context";

export function AppHeader() {
  const { state } = useAuth();

  return (
    <>
      <header className="header-geo accent-bar relative z-50 flex items-center justify-between bg-primary-dark px-6 py-3.5 text-white">
        <div className="flex items-center gap-4">
          <Link
            href="/"
            className="flex items-center gap-3 transition-opacity hover:opacity-90"
          >
            <Image
              src="/logo-inverted.png"
              alt="PortfolioDB"
              width={36}
              height={36}
              className="h-9 w-9 object-contain"
            />
            <span className="font-display text-lg font-bold tracking-tight">
              PortfolioDB
            </span>
          </Link>
          {state.status === "authenticated" && <PortfolioSelectorChip />}
        </div>
        <nav className="flex items-center gap-6">
          <UserMenu inverted />
          <SignInButton />
        </nav>
      </header>
      {state.status === "authenticated" && <PortfolioSelectorModal />}
    </>
  );
}
