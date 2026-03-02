"use client";

import { useState } from "react";
import { GoogleLogin } from "@react-oauth/google";
import { useAuth } from "@/contexts/auth-context";

export default function IdTokenPage() {
  const { signIn } = useAuth();
  const [idToken, setIdToken] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-slate-800">ID token</h1>
      <p className="text-slate-600">
        Use the button below to sign in with Google and obtain an ID token. You
        can copy the token for use in scripts (e.g. to call the Auth API or to
        pass credentials to other tools). The token is short-lived; request a
        new one when needed.
      </p>

      <div className="flex flex-wrap items-center gap-3">
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
          theme="filled_black"
          size="medium"
          text="signin_with"
        />
      </div>

      {error && (
        <p className="rounded bg-red-50 px-3 py-2 text-sm text-red-700">
          {error}
        </p>
      )}

      {idToken && (
        <div className="space-y-2">
          <label
            htmlFor="id-token-value"
            className="block text-sm font-medium text-slate-700"
          >
            ID token (copy for scripts)
          </label>
          <textarea
            id="id-token-value"
            readOnly
            rows={6}
            className="w-full rounded border border-slate-300 bg-slate-50 p-3 font-mono text-xs text-slate-800"
            value={idToken}
          />
        </div>
      )}
    </div>
  );
}
