export function ErrorAlert({ children }: { children: React.ReactNode }) {
  return (
    <p className="rounded-md bg-accent-soft/50 px-3 py-2 text-sm text-accent-dark">
      {children}
    </p>
  );
}
