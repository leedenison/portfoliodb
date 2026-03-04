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
    <div className="flex min-h-screen flex-col bg-background">
      <AppHeader />
      <div className="flex flex-1">
        <aside className="w-52 shrink-0 border-r border-border bg-surface py-6">
          <nav className="flex flex-col gap-0.5 px-3">
            {navItems.map(({ href, label }) => {
              const isActive =
                pathname === href ||
                (href !== "/holdings" && pathname.startsWith(href + "/"));
              return (
                <Link
                  key={href}
                  href={href}
                  className={
                    "rounded-lg px-3 py-2.5 text-sm font-medium transition-colors " +
                    (isActive
                      ? "bg-primary-light/30 text-primary-dark"
                      : "text-text-muted hover:bg-primary-light/20 hover:text-text-primary")
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
