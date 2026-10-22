import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { AlertTriangle, CheckCircle2, Info, X, XCircle } from "lucide-react";
import { cn } from "@/lib/cn";

type ToastKind = "success" | "error" | "info" | "warning";
interface ToastItem {
  id: number;
  kind: ToastKind;
  title: string;
  description?: string;
}
interface ToastApi {
  push: (kind: ToastKind, title: string, description?: string) => void;
  success: (title: string, description?: string) => void;
  error: (title: string, description?: string) => void;
  info: (title: string, description?: string) => void;
}

const Ctx = createContext<ToastApi | null>(null);

export function useToast(): ToastApi {
  const c = useContext(Ctx);
  if (!c) throw new Error("useToast hors ToastProvider");
  return c;
}

const ICONS = { success: CheckCircle2, error: XCircle, info: Info, warning: AlertTriangle };
const COLORS: Record<ToastKind, string> = {
  success: "text-success",
  error: "text-danger",
  info: "text-info",
  warning: "text-warning",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const seq = useRef(0);
  const dismiss = useCallback((id: number) => setItems((xs) => xs.filter((x) => x.id !== id)), []);
  const push = useCallback(
    (kind: ToastKind, title: string, description?: string) => {
      const id = ++seq.current;
      setItems((xs) => [...xs.slice(-3), { id, kind, title, description }]);
      setTimeout(() => dismiss(id), kind === "error" ? 8000 : 4500);
    },
    [dismiss],
  );
  const api = useMemo<ToastApi>(
    () => ({
      push,
      success: (t, d) => push("success", t, d),
      error: (t, d) => push("error", t, d),
      info: (t, d) => push("info", t, d),
    }),
    [push],
  );
  return (
    <Ctx.Provider value={api}>
      {children}
      <div aria-live="polite" aria-relevant="additions" className="pointer-events-none fixed bottom-4 right-4 z-[60] flex w-[min(92vw,380px)] flex-col gap-2">
        {items.map((t) => {
          const Icon = ICONS[t.kind];
          return (
            <div
              key={t.id}
              role={t.kind === "error" ? "alert" : "status"}
              className="pointer-events-auto flex animate-slide-in items-start gap-3 rounded-lg border border-border bg-elevated p-3 shadow-pop"
            >
              <Icon className={cn("mt-0.5 h-4 w-4 shrink-0", COLORS[t.kind])} aria-hidden />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-fg">{t.title}</p>
                {t.description && <p className="mt-0.5 break-words text-xs text-fg-muted">{t.description}</p>}
              </div>
              <button
                type="button"
                onClick={() => dismiss(t.id)}
                className="focus-ring -m-1 rounded p-1 text-fg-subtle hover:text-fg"
                aria-label="Fermer la notification"
              >
                <X className="h-3.5 w-3.5" aria-hidden />
              </button>
            </div>
          );
        })}
      </div>
    </Ctx.Provider>
  );
}
