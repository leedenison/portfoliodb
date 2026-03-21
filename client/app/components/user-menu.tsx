"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { useAuth } from "@/contexts/auth-context";

export function UserMenu({ inverted }: { inverted?: boolean }) {
  const { state, signOut } = useAuth();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const pathname = usePathname();

  // Close on click outside.
  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, [open]);

  // Close on Escape.
  useEffect(() => {
    if (!open) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open]);

  // Close when navigating.
  useEffect(() => {
    setOpen(false);
  }, [pathname]);

  if (state.status !== "authenticated") return null;

  const isAdmin = state.role === "admin";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={
          "flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition-colors " +
          (inverted
            ? "text-white/90 hover:bg-white/15"
            : "text-text-muted hover:bg-primary-light/15")
        }
      >
        {state.email}
        <svg
          className={
            "h-4 w-4 transition-transform " + (open ? "rotate-180" : "")
          }
          fill="none"
          viewBox="0 0 24 24"
          stroke="currentColor"
          strokeWidth={2}
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="M19 9l-7 7-7-7"
          />
        </svg>
      </button>

      {open && (
        <div className="absolute right-0 top-full z-50 mt-1 w-56 overflow-hidden rounded-lg bg-white dark:bg-surface shadow-lg ring-1 ring-black/5 dark:ring-white/10">
          <div className="border-b border-border px-4 py-3">
            <p className="truncate text-sm font-medium text-text-primary">
              {state.email}
            </p>
          </div>

          <div className="py-1">
            <Link
              href="/uploads"
              className="block px-4 py-2 text-sm text-text-primary transition-colors hover:bg-primary-light/10"
            >
              Uploads
            </Link>
            <Link
              href="/settings"
              className="block px-4 py-2 text-sm text-text-primary transition-colors hover:bg-primary-light/10"
            >
              Settings
            </Link>
            {isAdmin && (
              <Link
                href="/admin"
                className="block px-4 py-2 text-sm text-text-primary transition-colors hover:bg-primary-light/10"
              >
                Admin
              </Link>
            )}
          </div>

          <div className="border-t border-border py-1">
            <button
              type="button"
              onClick={() => signOut()}
              className="block w-full px-4 py-2 text-left text-sm text-text-primary transition-colors hover:bg-primary-light/10"
            >
              Log out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
