"use client";

import { createContext, useCallback, useContext, useState } from "react";
import type { Portfolio } from "@/lib/portfolio-api";

interface PortfolioContextValue {
  selected: Portfolio | null;
  setSelected: (p: Portfolio | null) => void;
  modalOpen: boolean;
  openModal: () => void;
  closeModal: () => void;
}

const PortfolioContext = createContext<PortfolioContextValue | null>(null);

export function PortfolioProvider({ children }: { children: React.ReactNode }) {
  const [selected, setSelected] = useState<Portfolio | null>(null);
  const [modalOpen, setModalOpen] = useState(false);

  const openModal = useCallback(() => setModalOpen(true), []);
  const closeModal = useCallback(() => setModalOpen(false), []);

  return (
    <PortfolioContext.Provider value={{ selected, setSelected, modalOpen, openModal, closeModal }}>
      {children}
    </PortfolioContext.Provider>
  );
}

export function usePortfolio() {
  const ctx = useContext(PortfolioContext);
  if (!ctx) throw new Error("usePortfolio must be used within PortfolioProvider");
  return ctx;
}
