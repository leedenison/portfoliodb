"use client";

import { GoogleOAuthProvider } from "@react-oauth/google";
import { AuthProvider } from "@/contexts/auth-context";
import { PortfolioProvider } from "@/contexts/portfolio-context";
import { UploadModalProvider } from "@/contexts/upload-modal-context";
import { SessionLostHandler } from "@/app/components/session-lost-handler";

const googleClientId =
  process.env.NEXT_PUBLIC_GOOGLE_CLIENT_ID ?? "";

export function Providers({ children }: { children: React.ReactNode }) {
  return (
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
  );
}
