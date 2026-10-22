import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import { cn } from "@/lib/cn";

export interface TabDef {
  id: string;
  label: ReactNode;
  count?: number;
}

/** Onglets WAI-ARIA (flèches gauche/droite, Début/Fin). */
export function Tabs({
  tabs,
  value,
  onChange,
  children,
  label,
}: {
  tabs: TabDef[];
  value: string;
  onChange: (id: string) => void;
  children: ReactNode;
  label: string;
}) {
  const base = useId();
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});
  const onKey = (e: KeyboardEvent) => {
    const i = tabs.findIndex((t) => t.id === value);
    let n = -1;
    if (e.key === "ArrowRight") n = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") n = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") n = 0;
    else if (e.key === "End") n = tabs.length - 1;
    if (n >= 0) {
      e.preventDefault();
      const t = tabs[n]!;
      onChange(t.id);
      refs.current[t.id]?.focus();
    }
  };
  return (
    <div>
      <div role="tablist" aria-label={label} onKeyDown={onKey} className="scroll-thin flex gap-1 overflow-x-auto border-b border-border">
        {tabs.map((t) => {
          const sel = t.id === value;
          return (
            <button
              key={t.id}
              ref={(el) => {
                refs.current[t.id] = el;
              }}
              role="tab"
              type="button"
              id={`${base}-tab-${t.id}`}
              aria-selected={sel}
              aria-controls={`${base}-panel`}
              tabIndex={sel ? 0 : -1}
              onClick={() => onChange(t.id)}
              className={cn(
                "focus-ring relative -mb-px flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 px-3 py-2 text-sm font-medium transition-colors",
                sel ? "border-accent text-fg" : "border-transparent text-fg-muted hover:text-fg",
              )}
            >
              {t.label}
              {typeof t.count === "number" && (
                <span className="rounded-full bg-hover px-1.5 text-2xs tabular-nums text-fg-muted">{t.count}</span>
              )}
            </button>
          );
        })}
      </div>
      <div role="tabpanel" id={`${base}-panel`} aria-labelledby={`${base}-tab-${value}`} className="pt-4">
        {children}
      </div>
    </div>
  );
}
