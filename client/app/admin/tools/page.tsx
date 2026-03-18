"use client";

import { useEffect, useState } from "react";
import { GoogleLogin } from "@react-oauth/google";
import { useAuth } from "@/contexts/auth-context";
import { getSession } from "@/lib/auth-api";

export default function ToolsPage() {
  const { signIn, state } = useAuth();
  const [idToken, setIdToken] = useState<string | null>(null);
  const [sessionToken, setSessionToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (state.status !== "authenticated") {
      setSessionToken(null);
      return;
    }
    getSession()
      .then((res) => setSessionToken(res.sessionId || null))
      .catch(() => setSessionToken(null));
  }, [state.status]);

  return (
    <div className="space-y-4">
      <h1 className="font-display text-xl font-bold text-text-primary">Authentication</h1>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-text-primary">Session Token</h2>
        {state.status === "authenticated" && sessionToken ? (
          <div className="space-y-2">
            <p className="text-text-muted">
              Session token for <span className="font-medium text-text-primary">{state.status === "authenticated" ? state.user?.name : ""}</span>.
              Use this as the{" "}
              <code className="rounded bg-primary-dark/[0.06] px-1 py-0.5 font-mono text-xs">x-session-id</code>{" "}
              header when calling backend APIs directly.
            </p>
            <textarea
              id="session-token-value"
              readOnly
              rows={3}
              className="w-full rounded-md border border-border bg-primary-dark/[0.03] p-3 font-mono text-xs text-text-primary"
              value={sessionToken}
            />
          </div>
        ) : (
          <p className="text-text-muted">
            No active session.
          </p>
        )}
      </section>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-text-primary">ID Token</h2>
        <p className="text-text-muted">
          Need a different user? Sign in with another Google account to obtain an
          ID token you can send to the Auth endpoint to start a new session.
        </p>

        <div className="flex flex-wrap items-center gap-3 [color-scheme:light]">
          <GoogleLogin
            onSuccess={async (credentialResponse) => {
              const token = credentialResponse.credential ?? null;
              setError(null);
              if (token) {
                setIdToken(token);
                await signIn(token);
              } else {
                setError("No credential in response.");
              }
            }}
            onError={() => setError("Google sign-in failed.")}
            useOneTap={false}
            theme="outline"
            size="medium"
            text="signin_with"
          />
        </div>

        {error && (
          <p className="rounded-md bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
            {error}
          </p>
        )}

        {idToken && (
          <div className="space-y-2">
            <label
              htmlFor="id-token-value"
              className="block text-sm font-medium text-text-primary"
            >
              ID token (copy for scripts)
            </label>
            <textarea
              id="id-token-value"
              readOnly
              rows={6}
              className="w-full rounded-md border border-border bg-primary-dark/[0.03] p-3 font-mono text-xs text-text-primary"
              value={idToken}
            />
          </div>
        )}
      </section>
    </div>
  );
}
