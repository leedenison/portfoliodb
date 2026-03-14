export function PaginationControls({
  pageIndex,
  hasPrev,
  hasNext,
  onPrev,
  onNext,
}: {
  pageIndex: number;
  hasPrev: boolean;
  hasNext: boolean;
  onPrev: () => void;
  onNext: () => void;
}) {
  if (!hasPrev && !hasNext) return null;

  return (
    <div className="flex items-center justify-between pt-2">
      <button
        type="button"
        onClick={onPrev}
        disabled={!hasPrev}
        className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm font-medium disabled:opacity-40 hover:enabled:bg-primary-light/15"
      >
        Previous
      </button>
      <span className="font-mono text-xs text-text-muted">
        Page {pageIndex + 1}
      </span>
      <button
        type="button"
        onClick={onNext}
        disabled={!hasNext}
        className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm font-medium disabled:opacity-40 hover:enabled:bg-primary-light/15"
      >
        Next
      </button>
    </div>
  );
}
