"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { AppHeader } from "../components/app-header";
import { useAuth } from "@/contexts/auth-context";

type NavLink = { href: string; label: string; disabled?: boolean };
type NavSection = { section: string; children: NavLink[] };
type NavItem = NavLink | NavSection;

const adminNav: NavItem[] = [
  { href: "/admin", label: "Dashboard" },
  {
    section: "Reference Data",
    children: [
      { href: "/admin/instruments", label: "Instruments" },
      { href: "/admin/prices", label: "Prices" },
      { href: "/admin/corporate-events", label: "Corporate Events" },
      { href: "/admin/inflation", label: "Inflation" },
    ],
  },
  {
    section: "Plugins",
    children: [
      { href: "/admin/plugins/identifier", label: "Identifier" },
      { href: "/admin/plugins/description", label: "Description" },
      { href: "/admin/plugins/price", label: "Price" },
      { href: "/admin/plugins/inflation", label: "Inflation" },
    ],
  },
  {
    section: "Events",
    children: [
      { href: "/admin/corporate-events", label: "Corporate Events" },
    ],
  },
  {
    section: "Diagnostics",
    children: [
      { href: "/admin/logs", label: "Logs", disabled: true },
      { href: "/admin/telemetry", label: "Telemetry" },
      { href: "/admin/workers", label: "Workers" },
      { href: "/admin/tools", label: "Authentication" },
    ],
  },
];

function isSection(item: NavItem): item is NavSection {
  return "section" in item;
}

function isSectionActive(section: NavSection, pathname: string) {
  return section.children.some(
    (c) => !c.disabled && (pathname === c.href || pathname.startsWith(c.href + "/")),
  );
}

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
      <main className="flex min-h-screen flex-col bg-background">
        <AppHeader />
        <div className="flex flex-1 items-center justify-center px-4 py-8">
          <p className="text-text-muted">Loading…</p>
        </div>
      </main>
    );
  }

  if (!isAdmin) {
    return (
      <main className="flex min-h-screen flex-col bg-background">
        <AppHeader />
        <div className="flex flex-1 flex-col items-center justify-center px-4 py-8 text-center">
          <h1 className="font-display text-xl font-bold text-text-primary">Access denied</h1>
          <p className="mt-2 text-text-muted">Admin role required.</p>
          <Link
            href="/"
            className="mt-4 text-sm text-primary underline hover:text-primary-dark"
          >
            Back to home
          </Link>
        </div>
      </main>
    );
  }

  return (
    <main className="flex min-h-screen flex-col bg-background">
      <AppHeader />
      <div className="flex flex-1 gap-8 px-4 py-8">
        <nav className="w-48 shrink-0 border-r border-border pr-6">
          <ul className="space-y-1.5">
            {adminNav.map((item) => {
              if (isSection(item)) {
                const active = isSectionActive(item, pathname);
                return (
                  <li key={item.section}>
                    <span
                      className={`block py-1 text-xs font-semibold uppercase tracking-wider ${
                        active ? "text-primary-dark dark:text-primary" : "text-text-muted"
                      }`}
                    >
                      {item.section}
                    </span>
                    <ul className="mt-1 space-y-0.5 border-l-2 border-border pl-3">
                      {item.children.map(({ href, label, disabled }) => (
                        <li key={href}>
                          {disabled ? (
                            <span className="block cursor-default py-0.5 text-sm text-text-muted opacity-40">
                              {label}
                            </span>
                          ) : (
                            <Link
                              href={href}
                              className={`block py-0.5 text-sm transition-colors ${
                                pathname === href || pathname.startsWith(href + "/")
                                  ? "font-semibold text-primary-dark dark:text-primary"
                                  : "text-text-muted hover:text-primary"
                              }`}
                            >
                              {label}
                            </Link>
                          )}
                        </li>
                      ))}
                    </ul>
                  </li>
                );
              }
              const link = item as NavLink;
              return (
                <li key={link.href}>
                  <Link
                    href={link.href}
                    className={`relative block py-1 text-sm transition-colors ${
                      pathname === link.href
                        ? "font-semibold text-primary-dark dark:text-primary before:absolute before:-left-[1px] before:top-0 before:bottom-0 before:w-[3px] before:rounded-full before:bg-accent"
                        : "text-text-muted hover:text-primary"
                    }`}
                  >
                    {link.label}
                  </Link>
                </li>
              );
            })}
          </ul>
        </nav>
        <div className="min-w-0 flex-1 animate-fade-in">
          <div className="mx-auto max-w-[84rem]">{children}</div>
        </div>
      </div>
    </main>
  );
}
