"use client";

import { GoogleLogin } from "@react-oauth/google";
import { useAuth } from "@/contexts/auth-context";

export function SignInButton() {
  const { signIn, state } = useAuth();

  if (state.status === "authenticated") return null;
  if (state.status === "loading") {
    return <span className="text-text-muted">Loading…</span>;
  }
  return (
    <div className="[color-scheme:light]">
      <GoogleLogin
        onSuccess={async (credentialResponse) => {
          const idToken = credentialResponse.credential;
          if (idToken) await signIn(idToken);
        }}
        onError={() => { }}
        useOneTap={false}
        theme="outline"
        size="medium"
        text="signin_with"
      />
    </div>
  );
}
