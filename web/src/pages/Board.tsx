import { useEffect, useMemo, useRef } from "react";
import { Link } from "react-router-dom";
import { Bot } from "lucide-react";
import { useActivity, useOverview, usePipeline } from "@/api/queries";
import type { ActiveRun, ActivityItem, PipelineItem } from "@/api/types";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card } from "@/components/ui/primitives";
import { DegradedBanner, EmptyState, ErrorState } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { cn } from "@/lib/cn";
import { formatCompact, formatNumber } from "@/lib/format";
import { roleLabel, stateMeta } from "@/lib/states";

/** Colonnes du tableau en direct : où en est chaque ticket (et chaque feature en conception). */
export const BOARD_COLUMNS: { id: string; label: string; ticket: string[]; feature: string[] }[] = [
  { id: "design", label: "Spec et conception", ticket: [], feature: ["SPEC", "TECH_DESIGN", "ISSUES_OPENED", "READY_FOR_DEV"] },
  { id: "ready", label: "À démarrer", ticket: ["PLANNED", "READY_FOR_DEV"], feature: [] },
  { id: "dev", label: "Développement", ticket: ["DEV", "CHANGES_REQUESTED"], feature: [] },
  { id: "review", label: "Revue", ticket: ["REVIEW"], feature: [] },
  { id: "qa", label: "QA", ticket: ["QA"], feature: [] },
  {
    id: "you",
    label: "À toi",
    ticket: ["AWAITING_PR_APPROVAL", "PR_OPENED", "HUMAN_MERGE", "BLOCKED"],
    feature: ["SPEC_REVIEW", "NEEDS_CLARIFICATION", "TECH_DESIGN_REVIEW", "HUMAN_GATE", "ACCEPTANCE", "BLOCKED"],
  },
];

/** Ce que l'humain doit faire, par état (colonne « À toi »). */
const YOUR_MOVE: Record<string, string> = {
  SPEC_REVIEW: "Valider la spec",
  NEEDS_CLARIFICATION: "Répondre au PO",
  TECH_DESIGN_REVIEW: "Valider la conception",
  HUMAN_GATE: "Donner le feu vert",
  ACCEPTANCE: "Faire la recette",
  AWAITING_PR_APPROVAL: "Publier la PR",
  PR_OPENED: "PR ouverte, CI en cours",
  HUMAN_MERGE: "Merger la PR",
  BLOCKED: "Bloqué : à débloquer",
};

export interface BoardCard {
  item: PipelineItem;
  column: string;
  run?: ActiveRun;
}

/** Répartit le pipeline dans les colonnes et associe à chaque carte le run en cours sur son sujet. */
export function buildBoard(features: PipelineItem[], tickets: PipelineItem[], running: ActiveRun[]): Record<string, BoardCard[]> {
  const out: Record<string, BoardCard[]> = Object.fromEntries(BOARD_COLUMNS.map((c) => [c.id, []]));
  const runOf = (type: string, id: number) => running.find((r) => r.subject.type === type && r.subject.id === id);
  for (const it of tickets) {
    const col = BOARD_COLUMNS.find((c) => c.ticket.includes(it.state));
    if (col) (out[col.id] ??= []).push({ item: it, column: col.id, run: runOf("ticket", it.id) });
  }
  for (const it of features) {
    const col = BOARD_COLUMNS.find((c) => c.feature.includes(it.state));
    // Feature prête ou en cours de dev : ses tickets la représentent déjà.
    if (!col || (it.state === "READY_FOR_DEV" && (it.progress?.total ?? 0) > 0)) continue;
    (out[col.id] ??= []).push({ item: it, column: col.id, run: runOf("feature", it.id) });
  }
  return out;
}

function itemLink(it: PipelineItem) {
  return it.type === "ticket" ? `/tickets/${it.id}` : `/features/${it.id}`;
}

function cardTitle(it: PipelineItem) {
  if (it.type === "ticket" && it.key && it.title.startsWith(it.key + " · ")) return it.title.slice(it.key.length + 3);
  return it.title;
}

function Card_({ card, moved }: { card: BoardCard; moved: boolean }) {
  const { item: it, run } = card;
  const yours = card.column === "you";
  const blocked = it.state === "BLOCKED";
  return (
    <Link
      to={itemLink(it)}
      data-testid={`board-card-${it.type}-${it.id}`}
      className={cn(
        "focus-ring block rounded-lg border border-border bg-surface p-2.5 text-left transition-colors hover:border-border-strong",
        moved && "animate-arrive",
      )}
    >
      <div className="flex items-center justify-between gap-2 text-2xs text-fg-subtle">
        <span className="truncate">{it.type === "ticket" ? it.key : `Feature #${it.id}`}</span>
        <span className="shrink-0">{it.project}</span>
      </div>
      <div className="mt-0.5 line-clamp-2 font-medium text-fg">{cardTitle(it)}</div>
      {it.type === "ticket" && it.feature_title && <div className="mt-0.5 truncate text-xs text-fg-muted">{it.feature_title}</div>}
      {run ? (
        <div className="mt-2 rounded bg-progress/15 px-1.5 py-1 text-xs text-progress" data-testid="card-agent">
          <div className="flex items-center gap-1.5 font-semibold">
            <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-progress" aria-hidden />
            {roleLabel(run.role)} au travail
            <span className="ml-auto font-normal tabular-nums">{formatCompact(run.total_tokens)} tokens</span>
          </div>
          {run.last && (
            <div className="mt-0.5 truncate font-mono text-2xs opacity-90">{run.last.tool ? `${run.last.tool} · ${run.last.text}` : run.last.text}</div>
          )}
        </div>
      ) : yours ? (
        <div className={cn("mt-2 rounded px-1.5 py-1 text-xs font-semibold", blocked ? "bg-danger/15 text-danger" : "bg-review/15 text-review")}>
          {YOUR_MOVE[it.state] ?? stateMeta(it.state).label}
        </div>
      ) : (
        <div className="mt-2 text-xs text-fg-subtle">{stateMeta(it.state).label}</div>
      )}
      {it.progress && it.progress.total > 0 && (
        <div className="mt-2 flex items-center gap-2 text-2xs text-fg-subtle">
          <span className="h-1 flex-1 overflow-hidden rounded bg-hover">
            <span className="block h-full bg-success" style={{ width: `${(100 * it.progress.done) / it.progress.total}%` }} />
          </span>
          {it.progress.done}/{it.progress.total} tickets
        </div>
      )}
    </Link>
  );
}

/** Mémorise la colonne de chaque carte pour animer celles qui viennent d'en changer. */
function useMoved(board: Record<string, BoardCard[]>): Set<string> {
  const prev = useRef<Map<string, string> | null>(null);
  const moved = useMemo(() => {
    const now = new Map<string, string>();
    const out = new Set<string>();
    for (const [col, cards] of Object.entries(board)) for (const c of cards) now.set(`${c.item.type}-${c.item.id}`, col);
    if (prev.current) for (const [k, col] of now) if (prev.current.get(k) && prev.current.get(k) !== col) out.add(k);
    return { now, out };
  }, [board]);
  useEffect(() => {
    prev.current = moved.now;
  }, [moved]);
  return moved.out;
}

export function ActivityLine({ e }: { e: ActivityItem }) {
  const subject = e.subject.key || e.subject.title;
  return (
    <li className="animate-slide-in border-b border-border/60 py-1.5 text-xs last:border-0">
      <div className="flex items-baseline gap-1.5">
        <span className={cn("font-semibold", e.kind === "failed" ? "text-danger" : e.kind === "done" ? "text-success" : "text-progress")}>
          {roleLabel(e.role)}
        </span>
        <span className="truncate text-fg-subtle">{subject}</span>
        <span className="ml-auto shrink-0 text-2xs text-fg-subtle">
          <Time value={e.at} />
        </span>
      </div>
      <div className={cn("mt-0.5 break-words text-fg-muted", e.kind === "tool" && "font-mono text-2xs")}>
        {e.kind === "tool" && e.tool ? <span className="text-fg">{e.tool} </span> : null}
        {e.text}
      </div>
    </li>
  );
}

function SidePanel({ running, events }: { running: ActiveRun[]; events: ActivityItem[] }) {
  const overview = useOverview();
  return (
    <Card className="min-w-0 p-3">
      <h2 className="text-sm font-semibold text-fg">En ce moment</h2>
      {running.length === 0 ? (
        <p className="mt-1 text-xs text-fg-muted">Aucun agent ne travaille.</p>
      ) : (
        <ul className="mt-1" data-testid="running-agents">
          {running.map((r) => (
            <li key={r.run_id} className="flex items-start gap-2 border-b border-border/60 py-1.5 text-xs last:border-0">
              <span className="mt-1 h-2 w-2 shrink-0 animate-pulse rounded-full bg-progress" aria-hidden />
              <div className="min-w-0">
                <Link to={`/runs/${r.run_id}`} className="focus-ring font-semibold text-fg hover:text-accent">
                  {roleLabel(r.role)}
                </Link>
                <div className="truncate text-fg-muted">{r.subject.key ? `${r.subject.key} · ${r.subject.title}` : r.subject.title}</div>
              </div>
            </li>
          ))}
        </ul>
      )}
      <h2 className="mt-4 text-sm font-semibold text-fg">Activité</h2>
      {events.length === 0 ? (
        <p className="mt-1 text-xs text-fg-muted">Aucune activité récente.</p>
      ) : (
        <ul
          className="focus-ring scroll-thin mt-1 max-h-[52vh] overflow-y-auto rounded pr-1"
          aria-live="polite"
          aria-label="Activité des agents"
          tabIndex={0}
          data-testid="activity-feed"
        >
          {events.slice(0, 30).map((e) => (
            <ActivityLine key={e.id} e={e} />
          ))}
        </ul>
      )}
      {overview.data && <p className="mt-3 text-xs text-fg-subtle">{formatNumber(overview.data.tokens_today)} tokens aujourd'hui</p>}
    </Card>
  );
}

export default function BoardPage() {
  const pipeline = usePipeline();
  const activity = useActivity();
  const running = activity.data?.running ?? [];
  const board = useMemo(
    () => buildBoard(pipeline.data?.features.columns.flatMap((c) => c.items) ?? [], pipeline.data?.tickets.columns.flatMap((c) => c.items) ?? [], running),
    [pipeline.data, running],
  );
  const moved = useMoved(board);
  const total = Object.values(board).reduce((n, c) => n + c.length, 0);

  return (
    <>
      <PageHeader title="Tableau" description="Où en est chaque ticket, en direct. Clique une carte pour l'ouvrir." />
      {(pipeline.isError || activity.isError) && pipeline.data && (
        <div className="mb-4">
          <DegradedBanner error={pipeline.error ?? activity.error} onRetry={() => void pipeline.refetch()} />
        </div>
      )}
      {pipeline.isError && !pipeline.data ? (
        <Card>
          <ErrorState error={pipeline.error} onRetry={() => void pipeline.refetch()} />
        </Card>
      ) : (
        <div className="grid gap-4 2xl:grid-cols-[1fr_300px]">
          <div className="scroll-thin min-w-0 overflow-x-auto">
            {pipeline.data && total === 0 ? (
              <Card>
                <EmptyState
                  icon={<Bot className="h-5 w-5" aria-hidden />}
                  title="Rien en cours"
                  description="Crée une feature et lance sa spécification : elle apparaîtra ici."
                />
              </Card>
            ) : (
              <div className="grid min-w-[960px] grid-cols-6 gap-2" data-testid="live-board">
                {BOARD_COLUMNS.map((col) => (
                  <section key={col.id} aria-label={col.label} className="min-w-0">
                    <h2 className="flex items-center justify-between px-1 pb-1.5 text-xs font-semibold text-fg-muted">
                      {col.label}
                      <span className="tabular-nums">{board[col.id]?.length ?? 0}</span>
                    </h2>
                    <div
                      className={cn("flex min-h-[320px] flex-col gap-1.5 rounded-lg p-1.5", col.id === "you" ? "bg-review/10" : "bg-panel")}
                      data-testid={`col-${col.id}`}
                    >
                      {(board[col.id] ?? []).map((c) => (
                        <Card_ key={`${c.item.type}-${c.item.id}`} card={c} moved={moved.has(`${c.item.type}-${c.item.id}`)} />
                      ))}
                    </div>
                  </section>
                ))}
              </div>
            )}
          </div>
          <SidePanel running={running} events={activity.data?.events ?? []} />
        </div>
      )}
    </>
  );
}
