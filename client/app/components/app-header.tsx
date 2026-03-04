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
    <header className="flex items-center justify-between border-b border-primary-dark bg-primary px-6 py-3 text-white shadow-sm">
      <Link
        href="/"
        className="flex items-center gap-2 transition-opacity hover:opacity-90"
      >
        <Image
          src="/logo-inverted.png"
          alt="PortfolioDB"
          width={36}
          height={36}
          className="h-9 w-9 object-contain"
        />
        <span className="text-lg font-semibold tracking-tight">
          PortfolioDB
        </span>
      </Link>
      <nav className="flex items-center gap-6">
        {isAdmin && (
          <Link
            href="/admin"
            className="text-sm font-medium transition-colors hover:opacity-90"
          >
            Admin
          </Link>
        )}
        <UserMenu inverted />
        <SignInButton inverted />
      </nav>
    </header>
  );
}
