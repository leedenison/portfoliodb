"use client";

import { GoogleLogin } from "@react-oauth/google";
import { useAuth } from "@/contexts/auth-context";

export function SignInButton() {
  const { signIn, state } = useAuth();

  if (state.status === "authenticated") return null;
  if (state.status === "loading") {
    return <span className="text-slate-500">Loading…</span>;
  }
  return (
    <GoogleLogin
      onSuccess={async (credentialResponse) => {
        const idToken = credentialResponse.credential;
        if (idToken) await signIn(idToken);
      }}
      onError={() => {}}
      useOneTap={false}
      theme="filled_black"
      size="medium"
      text="signin_with"
    />
  );
}
