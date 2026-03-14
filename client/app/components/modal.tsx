"use client";

import { useEffect, useRef } from "react";

export function Modal({
  open,
  onClose,
  title,
  closable = true,
  className,
  children,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  closable?: boolean;
  className?: string;
  children: React.ReactNode;
}) {
  const backdropRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || !closable) return;
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [open, closable, onClose]);

  if (!open) return null;

  return (
    <div
      ref={backdropRef}
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={(e) => {
        if (closable && e.target === backdropRef.current) onClose();
      }}
    >
      <div
        className={
          "flex max-h-[80vh] w-full flex-col rounded-lg bg-surface shadow-xl sm:max-h-[600px] " +
          (className ?? "max-w-lg")
        }
      >
        <div className="flex items-center justify-between border-b border-border px-5 py-4">
          <h2 className="font-display text-lg font-bold text-text-primary">
            {title}
          </h2>
          {closable && (
            <button
              type="button"
              onClick={onClose}
              className="rounded-md p-1 text-text-muted transition-colors hover:bg-primary-light/15 hover:text-text-primary"
            >
              <svg
                className="h-5 w-5"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
                strokeWidth={2}
              >
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  d="M6 18L18 6M6 6l12 12"
                />
              </svg>
            </button>
          )}
        </div>
        {children}
      </div>
    </div>
  );
}
