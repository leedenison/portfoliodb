"use client";

import React, { createContext, useCallback, useContext, useState } from "react";
import type { AuthResponsePayload } from "@/lib/auth-api";
import { auth as authApi, logout as logoutApi } from "@/lib/auth-api";

type AuthState =
  | { status: "loading" }
  | { status: "unauthenticated" }
  | { status: "authenticated"; user: AuthResponsePayload["user"]; email: string };

type AuthContextValue = {
  state: AuthState;
  authError: string | null;
  clearAuthError: () => void;
  signIn: (googleIdToken: string) => Promise<{ ok: boolean; error?: string }>;
  signOut: () => Promise<void>;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "unauthenticated" });
  const [authError, setAuthError] = useState<string | null>(null);

  const signIn = useCallback(async (googleIdToken: string) => {
    setAuthError(null);
    try {
      const res = await authApi(googleIdToken);
      if (res.user) {
        setState({
          status: "authenticated",
          user: res.user,
          email: res.user.email,
        });
        return { ok: true };
      }
      setState({ status: "unauthenticated" });
      return { ok: false, error: "No user in response" };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      setState({ status: "unauthenticated" });
      setAuthError(message);
      return { ok: false, error: message };
    }
  }, []);

  const signOut = useCallback(async () => {
    try {
      await logoutApi();
    } finally {
      setState({ status: "unauthenticated" });
    }
  }, []);

  const clearAuthError = useCallback(() => setAuthError(null), []);

  return (
    <AuthContext.Provider value={{ state, authError, clearAuthError, signIn, signOut }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
