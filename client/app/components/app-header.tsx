"use client";

import Link from "next/link";
import { SignInButton } from "./sign-in";
import { UserMenu } from "./user-menu";
import { useAuth } from "@/contexts/auth-context";

export function AppHeader() {
  const { state } = useAuth();
  const isAdmin = state.status === "authenticated" && state.role === "admin";

  return (
    <header className="flex items-center justify-end gap-4 border-b border-slate-200 bg-white px-4 py-3">
      {isAdmin && (
        <Link
          href="/admin"
          className="text-sm text-slate-600 underline hover:text-slate-800"
        >
          Admin
        </Link>
      )}
      <UserMenu />
      <SignInButton />
    </header>
  );
}
