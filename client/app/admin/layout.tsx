"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { AppHeader } from "../components/app-header";
import { useAuth } from "@/contexts/auth-context";

const adminNav = [
  { href: "/admin", label: "Dashboard" },
  {
    label: "Plugins",
    children: [
      { href: "/admin/plugins/description", label: "Description" },
      { href: "/admin/plugins/identifier", label: "Identifier" },
    ],
  },
  { href: "/admin/telemetry", label: "Telemetry" },
  { href: "/admin/id-token", label: "ID token" },
];

function isPluginsActive(pathname: string) {
  return pathname.startsWith("/admin/plugins");
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
              if ("children" in item && item.children) {
                const active = isPluginsActive(pathname);
                return (
                  <li key={item.label}>
                    <span
                      className={`block py-1 text-xs font-semibold uppercase tracking-wider ${
                        active ? "text-primary-dark" : "text-text-muted"
                      }`}
                    >
                      {item.label}
                    </span>
                    <ul className="mt-1 space-y-0.5 border-l-2 border-border pl-3">
                      {item.children.map(({ href, label }) => (
                        <li key={href}>
                          <Link
                            href={href}
                            className={`block py-0.5 text-sm transition-colors ${
                              pathname === href
                                ? "font-semibold text-primary-dark"
                                : "text-text-muted hover:text-primary"
                            }`}
                          >
                            {label}
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </li>
                );
              }
              const link = item as { href: string; label: string };
              return (
                <li key={link.href}>
                  <Link
                    href={link.href}
                    className={`relative block py-1 text-sm transition-colors ${
                      pathname === link.href
                        ? "font-semibold text-primary-dark before:absolute before:-left-[1px] before:top-0 before:bottom-0 before:w-[3px] before:rounded-full before:bg-accent"
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
          <div className="mx-auto max-w-4xl">{children}</div>
        </div>
      </div>
    </main>
  );
}
