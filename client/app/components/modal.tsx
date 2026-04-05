"use client";

import * as Dialog from "@radix-ui/react-dialog";

export function Modal({
  open,
  onClose,
  title,
  closable = true,
  className,
  children,
  "data-testid": dataTestId,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  closable?: boolean;
  className?: string;
  children: React.ReactNode;
  "data-testid"?: string;
}) {
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(isOpen) => {
        if (!isOpen && closable) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/40" />
        <Dialog.Content
          data-testid={dataTestId}
          onEscapeKeyDown={(e) => { if (!closable) e.preventDefault(); }}
          onPointerDownOutside={(e) => { if (!closable) e.preventDefault(); }}
          onInteractOutside={(e) => { if (!closable) e.preventDefault(); }}
          className={
            "fixed left-1/2 top-1/2 z-50 flex max-h-[80vh] w-full -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg bg-surface shadow-xl sm:max-h-[600px] " +
            (className ?? "max-w-lg")
          }
        >
          <div className="flex items-center justify-between border-b border-border px-5 py-4">
            <Dialog.Title className="font-display text-lg font-bold text-text-primary">
              {title}
            </Dialog.Title>
            {closable && (
              <Dialog.Close className="rounded-md p-1 text-text-muted transition-colors hover:bg-primary-light/15 hover:text-text-primary">
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
              </Dialog.Close>
            )}
          </div>
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
