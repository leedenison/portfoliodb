"use client";

import Link from "next/link";
import Image from "next/image";
import { SignInButton } from "./sign-in";
import { UserMenu } from "./user-menu";
import { useAuth } from "@/contexts/auth-context";

export function AppHeader() {
  const { state } = useAuth();
  const isAdmin = state.status === "authenticated" && state.role === "admin";

  return (
    <header className="header-geo accent-bar flex items-center justify-between bg-primary-dark px-6 py-3.5 text-white">
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
      <nav className="flex items-center gap-6">
        {isAdmin && (
          <Link
            href="/admin"
            className="text-sm font-medium tracking-wide uppercase transition-colors hover:text-accent"
          >
            Admin
          </Link>
        )}
        <UserMenu inverted />
        <SignInButton />
      </nav>
    </header>
  );
}
