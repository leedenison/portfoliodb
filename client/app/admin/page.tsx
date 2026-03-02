"use client";

import Link from "next/link";

export default function AdminPage() {
  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-slate-800">Admin</h1>
      <p className="text-slate-600">
        Use the links in the sidebar to access admin tools.
      </p>
      <ul className="list-inside list-disc space-y-1 text-sm text-slate-700">
        <li>
          <Link href="/admin/id-token" className="underline hover:text-slate-900">
            ID token
          </Link>
          — fetch a Google ID token for use in scripts (e.g. calling the API with
          <code className="mx-1 rounded bg-slate-100 px-1 font-mono text-xs">x-session-id</code>
          or for Auth).
        </li>
      </ul>
    </div>
  );
}
