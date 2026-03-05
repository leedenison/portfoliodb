"use client";

import Link from "next/link";

export default function AdminPage() {
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-text-primary">Admin</h1>
      <p className="text-text-muted">
        Use the links in the sidebar to access admin tools.
      </p>
      <ul className="list-inside list-disc space-y-1 text-sm text-text-primary">
        <li>
          <Link href="/admin/plugins" className="underline hover:text-primary">
            Identifier plugins
          </Link>
          — enable/disable identification plugins and edit config (API keys, precedence).
        </li>
        <li>
          <Link href="/admin/id-token" className="underline hover:text-primary">
            ID token
          </Link>
          — fetch a Google ID token for use in scripts (e.g. calling the API with
          <code className="mx-1 rounded bg-primary-light/20 px-1 font-mono text-xs">x-session-id</code>
          or for Auth).
        </li>
      </ul>
    </div>
  );
}
