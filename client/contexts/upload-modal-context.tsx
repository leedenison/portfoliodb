"use client";

import { createContext, useCallback, useContext, useState } from "react";

interface UploadModalState {
  isOpen: boolean;
  onComplete: (() => void) | null;
}

interface UploadModalContextValue {
  isOpen: boolean;
  openUploadModal: (onComplete?: () => void) => void;
  closeUploadModal: () => void;
  onComplete: (() => void) | null;
}

const UploadModalContext = createContext<UploadModalContextValue | null>(null);

export function UploadModalProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<UploadModalState>({ isOpen: false, onComplete: null });

  const openUploadModal = useCallback((onComplete?: () => void) => {
    setState({ isOpen: true, onComplete: onComplete ?? null });
  }, []);

  const closeUploadModal = useCallback(() => {
    setState({ isOpen: false, onComplete: null });
  }, []);

  return (
    <UploadModalContext.Provider
      value={{ isOpen: state.isOpen, openUploadModal, closeUploadModal, onComplete: state.onComplete }}
    >
      {children}
    </UploadModalContext.Provider>
  );
}

export function useUploadModal() {
  const ctx = useContext(UploadModalContext);
  if (!ctx) throw new Error("useUploadModal must be used within UploadModalProvider");
  return ctx;
}
