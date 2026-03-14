"use client";

import { usePortfolio } from "@/contexts/portfolio-context";

export function PortfolioSelectorChip() {
  const { selected, openModal } = usePortfolio();

  return (
    <button
      type="button"
      onClick={openModal}
      className="flex items-center gap-1.5 rounded-lg bg-white/10 px-3 py-1.5 text-sm font-medium transition-colors hover:bg-white/20"
    >
      {selected?.name ?? "All Holdings"}
      <svg
        className="h-3.5 w-3.5 opacity-70"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
        strokeWidth={2}
      >
        <path strokeLinecap="round" strokeLinejoin="round" d="M19 9l-7 7-7-7" />
      </svg>
    </button>
  );
}
