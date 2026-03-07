"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/contexts/auth-context";
import { registerSessionLostHandler } from "@/lib/session-lost";

/**
 * Registers the global session-lost callback so that when any API call
 * receives HTTP 401/403, the app invalidates the session and redirects to home.
 * Renders nothing.
 */
export function SessionLostHandler() {
  const router = useRouter();
  const { invalidateSession } = useAuth();

  useEffect(() => {
    return registerSessionLostHandler(() => {
      invalidateSession();
      router.replace("/");
    });
  }, [invalidateSession, router]);

  return null;
}
