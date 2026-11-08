import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { useNavigate } from "react-router-dom";
import { CheckSquare, Search, X } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useApprovals, useDecideApproval, usePipeline } from "@/api/queries";
import type { Approval } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Kbd } from "@/components/ui/primitives";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { cn } from "@/lib/cn";
import { approvalKindLabel } from "@/lib/states";

const subjectLink = (a: Approval) =>
  a.subject_type === "ticket" ? `/tickets/${a.subject_id}` : a.subject_type === "feature" ? `/features/${a.subject_id}` : `/approvals/${a.id}`;

// ------------------------------------------------------------------ « À décider »

function DecisionItem({ a, onOpen }: { a: Approval; onOpen: (to: string) => void }) {
  const m = useDecideApproval(a.id);
  const toast = useToast();
  const [rejecting, setRejecting] = useState(false);
  const [comment, setComment] = useState("");
  const decide = (decision: "approved" | "rejected") =>
    m.mutate(
      { decision, comment: comment.trim() },
      {
        onSuccess: () => toast.success(decision === "approved" ? "Approuvé" : "Refusé", a.title),
        onError: (e) => toast.error("Décision refusée", errorMessage(e)),
      },
    );
  return (
    <li className="rounded-lg border border-review/40 bg-review/10 p-3" data-testid={`decision-${a.id}`}>
      <div className="flex items-baseline justify-between gap-2 text-2xs text-fg-subtle">
        <span>{approvalKindLabel(a.kind)}</span>
        <Time value={a.created_at} />
      </div>
      <div className="mt-0.5 font-semibold text-fg">{a.title}</div>
      <p className="mt-1 text-xs text-fg-muted">{a.effect}</p>
      {rejecting ? (
        <div className="mt-2 space-y-2">
          <textarea
            aria-label={`Raison du refus : ${a.title}`}
            className="focus-ring w-full rounded-md border border-border bg-surface p-2 text-sm"
            rows={2}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="Pourquoi ? L'agent s'en servira."
            autoFocus
          />
          <div className="flex gap-2">
            <Button size="sm" variant="danger" loading={m.isPending} disabled={!comment.trim()} onClick={() => decide("rejected")}>
              Confirmer le refus
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setRejecting(false)}>
              Annuler
            </Button>
          </div>
        </div>
      ) : (
        <div className="mt-2 flex flex-wrap gap-2">
          <Button size="sm" variant="primary" loading={m.isPending} onClick={() => decide("approved")}>
            Approuver
          </Button>
          <Button size="sm" variant="secondary" onClick={() => setRejecting(true)}>
            Refuser
          </Button>
          <Button size="sm" variant="ghost" onClick={() => onOpen(`/approvals/${a.id}`)}>
            Détails
          </Button>
          <Button size="sm" variant="ghost" onClick={() => onOpen(subjectLink(a))}>
            Ouvrir {a.subject_type === "ticket" ? "le ticket" : "la feature"}
          </Button>
        </div>
      )}
    </li>
  );
}

export function DecisionsDrawer({ open, onClose, items }: { open: boolean; onClose: () => void; items: Approval[] }) {
  const nav = useNavigate();
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    document.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("button")?.focus();
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-40">
      <div className="absolute inset-0 bg-black/40" aria-hidden onClick={onClose} />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label="À décider"
        className="absolute inset-y-0 right-0 flex w-full max-w-md animate-slide-in flex-col border-l border-border bg-panel shadow-pop"
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <h2 className="text-base font-semibold text-fg">À décider</h2>
          <button type="button" onClick={onClose} className="focus-ring rounded-md p-1.5 text-fg-muted hover:bg-hover" aria-label="Fermer">
            <X className="h-4 w-4" aria-hidden />
          </button>
        </div>
        <div className="scroll-thin flex-1 overflow-y-auto p-4">
          {items.length === 0 ? (
            <p className="text-sm text-fg-muted">Rien n'attend ta décision. Les agents te préviendront ici.</p>
          ) : (
            <ul className="space-y-3">
              {items.map((a) => (
                <DecisionItem
                  key={a.id}
                  a={a}
                  onOpen={(to) => {
                    onClose();
                    nav(to);
                  }}
                />
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ Ctrl+K

export interface PaletteEntry {
  id: string;
  label: string;
  hint: string;
  to: string;
  keywords?: string;
}

const PAGES: PaletteEntry[] = [
  { id: "p-board", label: "Tableau", hint: "page", to: "/" },
  { id: "p-overview", label: "Vue d'ensemble", hint: "page", to: "/overview" },
  { id: "p-approvals", label: "Approbations", hint: "page", to: "/approvals" },
  { id: "p-features", label: "Features", hint: "page", to: "/features" },
  { id: "p-new", label: "Nouvelle feature", hint: "action", to: "/features?new=1", keywords: "créer idée" },
  { id: "p-runs", label: "Runs", hint: "page", to: "/runs" },
  { id: "p-agents", label: "Agents et modèles", hint: "page", to: "/agents" },
  { id: "p-projects", label: "Projets", hint: "page", to: "/projects" },
  { id: "p-audit", label: "Audit", hint: "page", to: "/audit" },
  { id: "p-health", label: "Santé", hint: "page", to: "/health" },
];

const norm = (s: string) =>
  s
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase();

/** Filtre par mots (tous présents), sans accents ni casse ; garde l'ordre d'origine. */
export function filterEntries(entries: PaletteEntry[], q: string): PaletteEntry[] {
  const words = norm(q).split(/\s+/).filter(Boolean);
  if (!words.length) return entries;
  return entries.filter((e) => {
    const hay = norm(`${e.label} ${e.hint} ${e.keywords ?? ""}`);
    return words.every((w) => hay.includes(w));
  });
}

export function CommandPalette({ open, onClose, approvals }: { open: boolean; onClose: () => void; approvals: Approval[] }) {
  const nav = useNavigate();
  const pipeline = usePipeline();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const entries = useMemo(() => {
    const out: PaletteEntry[] = approvals.map((a) => ({
      id: `a-${a.id}`,
      label: a.title,
      hint: "à décider",
      to: `/approvals/${a.id}`,
      keywords: "valider approuver decision",
    }));
    for (const col of pipeline.data?.features.columns ?? [])
      for (const f of col.items) out.push({ id: `f-${f.id}`, label: `F${f.id} · ${f.title}`, hint: `feature · ${f.project}`, to: `/features/${f.id}` });
    for (const col of pipeline.data?.tickets.columns ?? [])
      for (const t of col.items) out.push({ id: `t-${t.id}`, label: t.title, hint: `ticket · ${t.feature_title ?? ""}`, to: `/tickets/${t.id}` });
    return [...out, ...PAGES];
  }, [approvals, pipeline.data]);
  const shown = useMemo(() => filterEntries(entries, q).slice(0, 12), [entries, q]);
  useEffect(() => {
    if (open) {
      setQ("");
      setSel(0);
    }
  }, [open]);
  useEffect(() => setSel(0), [q]);
  if (!open) return null;
  const go = (e: PaletteEntry | undefined) => {
    if (!e) return;
    onClose();
    nav(e.to);
  };
  const onKey = (e: ReactKeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSel((s) => Math.min(s + 1, shown.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSel((s) => Math.max(s - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(shown[sel]);
    } else if (e.key === "Escape") onClose();
  };
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/45 px-4 pt-[12vh]" onClick={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Aller à"
        className="w-full max-w-xl overflow-hidden rounded-xl border border-border bg-elevated shadow-pop"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 border-b border-border px-3">
          <Search className="h-4 w-4 text-fg-subtle" aria-hidden />
          <input
            autoFocus
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={shown[sel] ? `pal-${shown[sel].id}` : undefined}
            aria-label="Feature, ticket, décision ou page"
            placeholder="Feature, ticket, décision ou page…"
            className="h-12 flex-1 bg-transparent text-base text-fg outline-none placeholder:text-fg-subtle"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={onKey}
          />
          <Kbd>Échap</Kbd>
        </div>
        <ul id="palette-list" role="listbox" className="scroll-thin max-h-[50vh] overflow-y-auto py-1">
          {shown.length === 0 && <li className="px-4 py-3 text-sm text-fg-muted">Aucun résultat.</li>}
          {shown.map((e, i) => (
            <li
              key={e.id}
              id={`pal-${e.id}`}
              role="option"
              aria-selected={i === sel}
              onMouseEnter={() => setSel(i)}
              onClick={() => go(e)}
              className={cn("flex cursor-pointer items-center justify-between gap-3 px-4 py-2 text-sm", i === sel ? "bg-accent/15 text-fg" : "text-fg-muted")}
            >
              <span className="truncate">{e.label}</span>
              <span className={cn("shrink-0 text-xs", e.hint === "à décider" ? "text-review" : "text-fg-subtle")}>{e.hint}</span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

// ------------------------------------------------------------------ barre du haut

export function TopBar() {
  const approvals = useApprovals({ status: "pending" });
  const items = approvals.data ?? [];
  const [drawer, setDrawer] = useState(false);
  const [palette, setPalette] = useState(false);
  const [bump, setBump] = useState(false);
  const prev = useRef<number | null>(null);
  useEffect(() => {
    if (prev.current !== null && items.length > prev.current) {
      setBump(true);
      const t = setTimeout(() => setBump(false), 600);
      return () => clearTimeout(t);
    }
    prev.current = items.length;
  }, [items.length]);
  useEffect(() => {
    prev.current = items.length;
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  return (
    <>
      <div className="sticky top-0 z-30 hidden h-12 items-center gap-3 border-b border-border bg-bg/95 px-6 backdrop-blur lg:flex">
        <button
          type="button"
          onClick={() => setPalette(true)}
          className="focus-ring flex h-8 w-80 items-center gap-2 rounded-md border border-border bg-surface px-2.5 text-sm text-fg-subtle hover:border-border-strong"
        >
          <Search className="h-4 w-4" aria-hidden />
          <span className="flex-1 text-left">Aller à, rechercher…</span>
          <Kbd>Ctrl K</Kbd>
        </button>
        <button
          type="button"
          onClick={() => setDrawer(true)}
          data-testid="decisions-button"
          className={cn(
            "focus-ring ml-auto flex h-8 items-center gap-2 rounded-md px-3 text-sm font-semibold",
            items.length ? "bg-review/15 text-review hover:bg-review/25" : "text-fg-muted hover:bg-hover",
            bump && "animate-bump",
          )}
        >
          <CheckSquare className="h-4 w-4" aria-hidden />À décider
          <span className={cn("rounded-full px-1.5 text-xs tabular-nums", items.length ? "bg-review text-bg" : "bg-hover text-fg-muted")}>{items.length}</span>
        </button>
      </div>
      <DecisionsDrawer open={drawer} onClose={() => setDrawer(false)} items={items} />
      <CommandPalette open={palette} onClose={() => setPalette(false)} approvals={items} />
    </>
  );
}
