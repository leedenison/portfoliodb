"use client";

import { useState } from "react";
import { GoogleLogin } from "@react-oauth/google";
import { useAuth } from "@/contexts/auth-context";

export default function ToolsPage() {
  const { signIn } = useAuth();
  const [idToken, setIdToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="space-y-4">
      <h1 className="font-display text-xl font-bold text-text-primary">Tools</h1>

      <section className="space-y-3">
        <h2 className="text-sm font-semibold text-text-primary">ID Token</h2>
        <p className="text-text-muted">
          Use the button below to sign in with Google and obtain an ID token. You
          can copy the token for use in scripts (e.g. to call the Auth API or to
          pass credentials to other tools). The token is short-lived; request a
          new one when needed.
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
