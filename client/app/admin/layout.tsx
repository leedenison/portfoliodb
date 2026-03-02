"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { AppHeader } from "../components/app-header";
import { useAuth } from "@/contexts/auth-context";

const adminNav = [
  { href: "/admin", label: "Overview" },
  { href: "/admin/id-token", label: "ID token" },
];

export default function AdminLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();
  const { state } = useAuth();
  const isAdmin = state.status === "authenticated" && state.role === "admin";

  if (state.status === "loading") {
    return (
      <main className="flex min-h-screen flex-col">
        <AppHeader />
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-slate-500">Loading…</p>
        </div>
      </main>
    );
  }

  if (!isAdmin) {
    return (
      <main className="flex min-h-screen flex-col">
        <AppHeader />
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8">
          <h1 className="text-xl font-semibold text-slate-800">Access denied</h1>
          <p className="mt-2 text-slate-600">Admin role required.</p>
          <Link
            href="/"
            className="mt-4 text-sm text-slate-600 underline hover:text-slate-800"
          >
            Back to home
          </Link>
        </div>
      </main>
    );
  }

  return (
    <main className="flex min-h-screen flex-col">
      <AppHeader />
      <div className="flex flex-1 gap-8 px-4 py-8">
        <nav className="w-40 shrink-0 border-r border-slate-200 pr-6">
          <ul className="space-y-2">
            {adminNav.map(({ href, label }) => (
              <li key={href}>
                <Link
                  href={href}
                  className={`block text-sm ${
                    pathname === href
                      ? "font-medium text-slate-800"
                      : "text-slate-600 underline hover:text-slate-800"
                  }`}
                >
                  {label}
                </Link>
              </li>
            ))}
          </ul>
        </nav>
        <div className="min-w-0 flex-1">{children}</div>
      </div>
    </main>
  );
}
