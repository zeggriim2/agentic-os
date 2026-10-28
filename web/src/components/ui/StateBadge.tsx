import { cn } from "@/lib/cn";
import { stateMeta, TONE_CLASSES, type Tone } from "@/lib/states";
import type { ReactNode } from "react";

/** Badge d'état unique pour toute l'application (feature, ticket, run, approbation, CI, sync…). */
export function StateBadge({ state, className, label }: { state: string | null | undefined; className?: string; label?: string }) {
  const meta = stateMeta(state);
  const t = TONE_CLASSES[meta.tone];
  return (
    <span
      data-tone={meta.tone}
      data-state={state ?? ""}
      title={state ?? undefined}
      className={cn(
        "inline-flex max-w-full items-center gap-1.5 whitespace-nowrap rounded-full border px-2 py-0.5 text-2xs font-medium leading-4",
        t.badge,
        className,
      )}
    >
      <span aria-hidden className={cn("h-1.5 w-1.5 shrink-0 rounded-full", t.dot)} />
      <span className="truncate">{label ?? meta.label}</span>
    </span>
  );
}

export function ToneBadge({ tone, children, className, title }: { tone: Tone; children: ReactNode; className?: string; title?: string }) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex items-center gap-1 whitespace-nowrap rounded-full border px-2 py-0.5 text-2xs font-medium leading-4",
        TONE_CLASSES[tone].badge,
        className,
      )}
    >
      {children}
    </span>
  );
}

export function Tag({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("inline-flex items-center rounded border border-border bg-panel px-1.5 py-0.5 text-2xs font-medium text-fg-muted", className)}>
      {children}
    </span>
  );
}
