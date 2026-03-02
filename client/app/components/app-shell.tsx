"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { AppHeader } from "./app-header";

const navItems = [
  { href: "/holdings", label: "Holdings" },
  { href: "/portfolios", label: "Portfolios" },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  return (
    <div className="flex min-h-screen flex-col">
      <AppHeader />
      <div className="flex flex-1">
        <aside className="w-52 shrink-0 border-r border-slate-200 bg-white py-4">
          <nav className="flex flex-col gap-0.5 px-2">
            {navItems.map(({ href, label }) => {
              const isActive =
                pathname === href ||
                (href !== "/holdings" && pathname.startsWith(href + "/"));
              return (
                <Link
                  key={href}
                  href={href}
                  className={
                    "rounded px-3 py-2 text-sm font-medium transition-colors " +
                    (isActive
                      ? "bg-slate-100 text-slate-900"
                      : "text-slate-600 hover:bg-slate-50 hover:text-slate-800")
                  }
                >
                  {label}
                </Link>
              );
            })}
          </nav>
        </aside>
        <main className="flex flex-1 flex-col">{children}</main>
      </div>
    </div>
  );
}
