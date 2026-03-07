"use client";

import React, { createContext, useCallback, useContext, useEffect, useState } from "react";
import type { AuthResponsePayload } from "@/lib/auth-api";
import { auth as authApi, getSession, logout as logoutApi } from "@/lib/auth-api";

type AuthState =
  | { status: "loading" }
  | { status: "unauthenticated" }
  | { status: "authenticated"; user: AuthResponsePayload["user"]; email: string; role: string };

type AuthContextValue = {
  state: AuthState;
  authError: string | null;
  clearAuthError: () => void;
  signIn: (googleIdToken: string) => Promise<{ ok: boolean; error?: string }>;
  signOut: () => Promise<void>;
  /** Mark session as invalid (e.g. after 401/403 from API). Redirect is handled by SessionLostHandler. */
  invalidateSession: () => void;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "loading" });
  const [authError, setAuthError] = useState<string | null>(null);

  useEffect(() => {
    getSession()
      .then((res) => {
        if (res.user) {
          setState({
            status: "authenticated",
            user: res.user,
            email: res.user.email,
            role: res.user.role ?? "user",
          });
        } else {
          setState({ status: "unauthenticated" });
        }
      })
      .catch(() => {
        setState({ status: "unauthenticated" });
      });
  }, []);

  const signIn = useCallback(async (googleIdToken: string) => {
    setAuthError(null);
    try {
      const res = await authApi(googleIdToken);
      if (res.user) {
        setState({
          status: "authenticated",
          user: res.user,
          email: res.user.email,
          role: res.user.role ?? "user",
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

  const invalidateSession = useCallback(() => {
    setState({ status: "unauthenticated" });
  }, []);

  return (
    <AuthContext.Provider value={{ state, authError, clearAuthError, signIn, signOut, invalidateSession }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
