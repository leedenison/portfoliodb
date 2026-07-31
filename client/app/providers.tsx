"use client";

import { useState } from "react";
import { QueryClientProvider } from "@tanstack/react-query";
import { GoogleOAuthProvider } from "@react-oauth/google";
import { AuthProvider } from "@/contexts/auth-context";
import { PortfolioProvider } from "@/contexts/portfolio-context";
import { UploadModalProvider } from "@/contexts/upload-modal-context";
import { SessionLostHandler } from "@/app/components/session-lost-handler";
import { createQueryClient } from "@/lib/query-client";

const googleClientId =
  process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID ?? "";

export function Providers({ children }: { children: React.ReactNode }) {
  // useState, not module scope: one client per mount, never rebuilt on re-render.
  const [queryClient] = useState(createQueryClient);

  return (
    // Outermost, because AuthProvider reads the session through a query.
    <QueryClientProvider client={queryClient}>
      <GoogleOAuthProvider clientId={googleClientId}>
        <AuthProvider>
          <PortfolioProvider>
            <UploadModalProvider>
              <SessionLostHandler />
              {children}
            </UploadModalProvider>
          </PortfolioProvider>
        </AuthProvider>
      </GoogleOAuthProvider>
    </QueryClientProvider>
  );
}
