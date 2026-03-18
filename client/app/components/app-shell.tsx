"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { AppHeader } from "./app-header";
import { UploadModal } from "./upload-modal";

const navItems = [
  { href: "/holdings", label: "Holdings" },
  { href: "/transactions", label: "Transactions", disabled: true },
  { href: "/performance", label: "Performance", disabled: true },
  { href: "/analysis", label: "Analysis", disabled: true },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  return (
    <div className="flex min-h-screen flex-col bg-background">
      <AppHeader />
      <div className="relative z-0 flex flex-1">
        <aside className="w-52 shrink-0 border-r border-border bg-surface py-8">
          <nav className="flex flex-col gap-1 px-3">
            {navItems.map(({ href, label, disabled }) => {
              if (disabled) {
                return (
                  <span
                    key={href}
                    className="relative cursor-default rounded-md px-4 py-2.5 text-sm font-medium tracking-wide text-text-muted opacity-40"
                  >
                    {label}
                  </span>
                );
              }
              const isActive =
                pathname === href || pathname.startsWith(href + "/");
              return (
                <Link
                  key={href}
                  href={href}
                  className={
                    "relative rounded-md px-4 py-2.5 text-sm font-medium tracking-wide transition-all " +
                    (isActive
                      ? "bg-primary-dark/5 font-semibold text-primary-dark dark:text-primary before:absolute before:left-0 before:top-1 before:bottom-1 before:w-[3px] before:rounded-full before:bg-accent"
                      : "text-text-muted hover:bg-primary-light/15 hover:text-text-primary")
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
      <UploadModal />
    </div>
  );
}
