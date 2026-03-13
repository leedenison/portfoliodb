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
        <aside className="w-52 shrink-0 border-r border-border bg-surface py-8">
          <nav className="flex flex-col gap-1 px-3">
            {navItems.map(({ href, label }, i) => {
              const isActive =
                pathname === href ||
                (href !== "/holdings" && pathname.startsWith(href + "/"));
              return (
                <Link
                  key={href}
                  href={href}
                  className={
                    "relative rounded-md px-4 py-2.5 text-sm font-medium tracking-wide transition-all " +
                    (isActive
                      ? "bg-primary-dark/5 font-semibold text-primary-dark before:absolute before:left-0 before:top-1 before:bottom-1 before:w-[3px] before:rounded-full before:bg-accent"
                      : "text-text-muted hover:bg-primary-light/15 hover:text-text-primary")
                  }
                  style={{ animationDelay: `${i * 50}ms` }}
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
