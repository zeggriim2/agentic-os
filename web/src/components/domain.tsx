import { useState } from "react";
import { Link } from "react-router-dom";
import { Bot, ChevronRight, Cog, User } from "lucide-react";
import type { Approval, AuditEntry, Run } from "@/api/types";
import { cn } from "@/lib/cn";
import { formatNumber, shortSha } from "@/lib/format";
import { approvalKindLabel, roleLabel } from "@/lib/states";
import { CodeBlock } from "./ui/CodeBlock";
import { StateBadge, Tag } from "./ui/StateBadge";
import { EmptyState } from "./ui/States";
import { Time } from "./ui/Time";

export function subjectPath(type: string | null | undefined, id: number | null | undefined): string | null {
  if (!type || id === null || id === undefined) return null;
  switch (type) {
    case "feature":
      return `/features/${id}`;
    case "ticket":
      return `/tickets/${id}`;
    case "approval":
      return `/approvals/${id}`;
    case "run":
      return `/runs/${id}`;
    case "project":
      return `/projects`;
    case "agent":
      return `/agents`;
    default:
      return null;
  }
}

const SUBJECT_LABELS: Record<string, string> = {
  feature: "Feature",
  ticket: "Ticket",
  approval: "Approbation",
  run: "Run",
  project: "Projet",
  agent: "Agent",
};

export function SubjectLink({ type, id }: { type: string | null | undefined; id: number | null | undefined }) {
  const label = `${SUBJECT_LABELS[type ?? ""] ?? type ?? "—"}${id !== null && id !== undefined ? ` #${id}` : ""}`;
  const to = subjectPath(type, id);
  return to ? (
    <Link to={to} className="focus-ring rounded text-accent hover:underline">
      {label}
    </Link>
  ) : (
    <span>{label}</span>
  );
}

export function Actor({ actor }: { actor: string }) {
  const [kind, name] = actor.includes(":") ? [actor.split(":")[0], actor.slice(actor.indexOf(":") + 1)] : ["system", actor];
  const Icon = kind === "user" ? User : kind === "agent" ? Bot : Cog;
  const label = kind === "agent" ? roleLabel(name) : kind === "system" ? "Système" : name;
  return (
    <span className="inline-flex items-center gap-1.5 text-fg" title={actor}>
      <Icon className="h-3.5 w-3.5 text-fg-subtle" aria-hidden />
      {label}
    </span>
  );
}

/** Différences de premier niveau entre before et after. */
export function diffKeys(before: unknown, after: unknown): { key: string; before: unknown; after: unknown }[] {
  const b = before && typeof before === "object" && !Array.isArray(before) ? (before as Record<string, unknown>) : null;
  const a = after && typeof after === "object" && !Array.isArray(after) ? (after as Record<string, unknown>) : null;
  if (!b && !a) return [];
  const keys = Array.from(new Set([...Object.keys(b ?? {}), ...Object.keys(a ?? {})])).sort();
  return keys
    .filter((k) => JSON.stringify(b?.[k]) !== JSON.stringify(a?.[k]))
    .map((k) => ({ key: k, before: b?.[k], after: a?.[k] }));
}

const short = (v: unknown) => {
  if (v === undefined) return "∅";
  const s = typeof v === "string" ? v : JSON.stringify(v);
  return s.length > 80 ? `${s.slice(0, 77)}…` : s;
};

export function AuditDiff({ entry }: { entry: AuditEntry }) {
  const changes = diffKeys(entry.before, entry.after);
  return (
    <div className="space-y-3">
      {changes.length > 0 && (
        <table className="w-full text-xs">
          <caption className="sr-only">Champs modifiés</caption>
          <thead>
            <tr className="text-left text-fg-subtle">
              <th className="py-1 pr-3 font-medium">Champ</th>
              <th className="py-1 pr-3 font-medium">Avant</th>
              <th className="py-1 font-medium">Après</th>
            </tr>
          </thead>
          <tbody>
            {changes.map((c) => (
              <tr key={c.key} className="border-t border-border align-top">
                <td className="py-1.5 pr-3 font-mono text-fg">{c.key}</td>
                <td className="py-1.5 pr-3 font-mono text-danger line-through decoration-danger/40">{short(c.before)}</td>
                <td className="py-1.5 font-mono text-success">{short(c.after)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="grid gap-3 md:grid-cols-2">
        <div>
          <p className="label-caps mb-1">Avant</p>
          <CodeBlock value={entry.before ?? null} maxHeight="max-h-64" />
        </div>
        <div>
          <p className="label-caps mb-1">Après</p>
          <CodeBlock value={entry.after ?? null} maxHeight="max-h-64" />
        </div>
      </div>
    </div>
  );
}

/** Chronologie d'audit (feature, ticket, activité récente). */
export function Timeline({ entries, showSubject, emptyLabel = "Aucun événement." }: { entries: AuditEntry[]; showSubject?: boolean; emptyLabel?: string }) {
  const [open, setOpen] = useState<number | null>(null);
  if (!entries.length) return <EmptyState title={emptyLabel} />;
  return (
    <ol className="relative space-y-0">
      {entries.map((e, i) => {
        const isOpen = open === e.id;
        const hasDiff = e.before != null || e.after != null;
        return (
          <li key={e.id ?? i} className="relative pl-6">
            <span aria-hidden className={cn("absolute left-[7px] top-0 w-px bg-border", i === entries.length - 1 ? "h-3" : "h-full")} />
            <span aria-hidden className="absolute left-[3px] top-2.5 h-[9px] w-[9px] rounded-full border-2 border-bg bg-border-strong" />
            <div className="pb-3">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                <Actor actor={e.actor} />
                <span className="font-mono text-xs text-fg-muted">{e.action}</span>
                {showSubject && <SubjectLink type={e.subject_type} id={e.subject_id} />}
                {e.sha && <Tag className="font-mono">{shortSha(e.sha)}</Tag>}
                {e.run_id && (
                  <Link to={`/runs/${e.run_id}`} className="focus-ring rounded text-xs text-accent hover:underline">
                    run #{e.run_id}
                  </Link>
                )}
                <Time value={e.at} className="ml-auto text-xs text-fg-subtle" />
              </div>
              {hasDiff && (
                <button
                  type="button"
                  onClick={() => setOpen(isOpen ? null : e.id)}
                  aria-expanded={isOpen}
                  className="focus-ring mt-1 inline-flex items-center gap-1 rounded text-xs text-fg-subtle hover:text-fg"
                >
                  <ChevronRight className={cn("h-3 w-3 transition-transform", isOpen && "rotate-90")} aria-hidden />
                  Détails
                </button>
              )}
              {isOpen && (
                <div className="mt-2">
                  <AuditDiff entry={e} />
                </div>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

export function RunsTable({ runs, empty = "Aucun run." }: { runs: Run[]; empty?: string }) {
  if (!runs.length) return <EmptyState title={empty} />;
  return (
    <div className="scroll-thin overflow-x-auto">
      <table className="w-full min-w-[640px] text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-fg-subtle">
            <th className="px-3 py-2 font-medium">Run</th>
            <th className="px-3 py-2 font-medium">Rôle</th>
            <th className="px-3 py-2 font-medium">Statut</th>
            <th className="px-3 py-2 font-medium">Modèle</th>
            <th className="px-3 py-2 text-right font-medium">Tokens</th>
            <th className="px-3 py-2 text-right font-medium">Démarré</th>
          </tr>
        </thead>
        <tbody>
          {runs.map((r) => (
            <tr key={r.id} className="border-b border-border/60 last:border-0 hover:bg-hover/50">
              <td className="px-3 py-2">
                <Link to={`/runs/${r.id}`} className="focus-ring rounded font-medium text-fg hover:text-accent">
                  #{r.id}
                </Link>
                {r.job_kind && <span className="ml-2 font-mono text-xs text-fg-subtle">{r.job_kind}</span>}
              </td>
              <td className="px-3 py-2 text-fg-muted">{roleLabel(r.agent_role)}</td>
              <td className="px-3 py-2">
                <StateBadge state={r.status} />
              </td>
              <td className="px-3 py-2 font-mono text-xs text-fg-muted">{r.model ?? "—"}</td>
              <td className="px-3 py-2 text-right tabular-nums text-fg-muted">{formatNumber(r.total_tokens)}</td>
              <td className="px-3 py-2 text-right text-xs text-fg-subtle">
                <Time value={r.started_at ?? r.created_at} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ApprovalList({ approvals, empty = "Aucune approbation." }: { approvals: Approval[]; empty?: string }) {
  if (!approvals.length) return <EmptyState title={empty} />;
  return (
    <ul className="divide-y divide-border">
      {approvals.map((a) => (
        <li key={a.id}>
          <Link to={`/approvals/${a.id}`} className="focus-ring flex items-center gap-3 px-3 py-2.5 hover:bg-hover/50">
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-medium text-fg">{a.title}</p>
              <p className="truncate text-xs text-fg-muted">
                {approvalKindLabel(a.kind)} · <span className="text-fg-subtle">{a.effect}</span>
              </p>
            </div>
            <StateBadge state={a.status} />
            <Time value={a.created_at} className="hidden shrink-0 whitespace-nowrap text-right text-xs text-fg-subtle sm:block" />
          </Link>
        </li>
      ))}
    </ul>
  );
}
