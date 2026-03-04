"use client";

import { useAuth } from "@/contexts/auth-context";

export function UserMenu({ inverted }: { inverted?: boolean }) {
  const { state, signOut } = useAuth();
  if (state.status !== "authenticated") return null;
  return (
    <div className="flex items-center gap-3">
      <span
        className={
          inverted
            ? "text-sm text-white/90"
            : "text-sm text-text-muted"
        }
      >
        {state.email}
      </span>
      <button
        type="button"
        onClick={() => signOut()}
        className={
          inverted
            ? "rounded-lg border border-white/80 bg-transparent px-3 py-1.5 text-sm font-medium text-white transition-colors hover:bg-white/20"
            : "rounded-lg border border-border bg-surface px-3 py-1.5 text-sm font-medium text-text-primary transition-colors hover:bg-primary-light/20"
        }
      >
        Log out
      </button>
    </div>
  );
}
