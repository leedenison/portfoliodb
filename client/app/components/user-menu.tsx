"use client";

import { useAuth } from "@/contexts/auth-context";

export function UserMenu() {
  const { state, signOut } = useAuth();
  if (state.status !== "authenticated") return null;
  return (
    <div className="flex items-center gap-3">
      <span className="text-sm text-slate-600">{state.email}</span>
      <button
        type="button"
        onClick={() => signOut()}
        className="rounded border border-slate-300 px-3 py-1 text-sm hover:bg-slate-100"
      >
        Log out
      </button>
    </div>
  );
}
