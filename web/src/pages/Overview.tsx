import { useState } from "react";
import { Link } from "react-router-dom";
import { Activity, AlertOctagon, AlertTriangle, ArrowRight, Ban, CheckSquare, Coins, Info, Pause, Play, Zap } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useApprovals, useOverview, useRuns, useSetDispatch } from "@/api/queries";
import type { BlockedRef, Overview, OverviewAlert } from "@/api/types";
import { ApprovalList, RunsTable, Timeline } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, Skeleton } from "@/components/ui/primitives";
import { DegradedBanner, EmptyState, ErrorState, ListSkeleton } from "@/components/ui/States";
import { useToast } from "@/components/ui/Toast";
import { TokenChart } from "@/components/ui/TokenChart";
import { cn } from "@/lib/cn";
import { formatCompact, formatNumber } from "@/lib/format";

function Stat({
  label,
  value,
  icon,
  to,
  tone,
  hint,
}: {
  label: string;
  value: string;
  icon: React.ReactNode;
  to?: string;
  tone?: string;
  hint?: string;
}) {
  const body = (
    <div className="flex items-start justify-between gap-3 p-4">
      <div>
        <p className="text-xs font-medium text-fg-muted">{label}</p>
        <p className={cn("mt-1 text-2xl font-semibold tabular-nums tracking-tight", tone ?? "text-fg")}>{value}</p>
        {hint && <p className="mt-0.5 text-2xs text-fg-subtle">{hint}</p>}
      </div>
      <span className="rounded-md border border-border bg-panel p-1.5 text-fg-subtle" aria-hidden>
        {icon}
      </span>
    </div>
  );
  return to ? (
    <Link to={to} className="card focus-ring block transition-colors hover:border-border-strong">
      {body}
    </Link>
  ) : (
    <div className="card">{body}</div>
  );
}

const count = (v: number | BlockedRef[]) => (Array.isArray(v) ? v.length : v ?? 0);

function AlertRow({ a }: { a: OverviewAlert }) {
  const crit = a.level === "error" || a.level === "critical";
  const warn = a.level === "warning";
  const Icon = crit ? AlertOctagon : warn ? AlertTriangle : Info;
  const cls = crit ? "border-danger/30 bg-danger/10 text-danger" : warn ? "border-warning/30 bg-warning/10 text-warning" : "border-info/30 bg-info/10 text-info";
  const content = (
    <>
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />
      <span className="flex-1 text-fg">{a.message}</span>
      {a.link && <ArrowRight className="mt-0.5 h-4 w-4 shrink-0" aria-hidden />}
    </>
  );
  const base = cn("flex items-start gap-2 rounded-md border px-3 py-2 text-sm", cls);
  if (a.link && a.link.startsWith("/")) {
    return (
      <Link to={a.link} className={cn(base, "focus-ring hover:brightness-110")}>
        {content}
      </Link>
    );
  }
  return <div className={base}>{content}</div>;
}

function DispatchCard({ ov }: { ov: Overview }) {
  const [open, setOpen] = useState(false);
  const m = useSetDispatch();
  const toast = useToast();
  const paused = ov.dispatch_paused;
  const confirm = () =>
    m.mutate(!paused, {
      onSuccess: () => {
        setOpen(false);
        toast.success(paused ? "Dispatch repris" : "Dispatch mis en pause");
      },
      onError: (e) => toast.error("Action impossible", errorMessage(e)),
    });
  return (
    <Card>
      <CardHeader title="Dispatch" icon={<Zap className="h-4 w-4 text-fg-subtle" aria-hidden />} />
      <div className="flex items-center justify-between gap-3 p-4">
        <div>
          <p className={cn("text-sm font-medium", paused ? "text-warning" : "text-success")}>{paused ? "En pause" : "Actif"}</p>
          <p className="mt-0.5 text-xs text-fg-muted">
            {paused ? "Aucun nouveau job n'est pris. Les runs en cours continuent." : "Les workers prennent les jobs en file."}
          </p>
        </div>
        <Button
          variant={paused ? "primary" : "secondary"}
          size="sm"
          onClick={() => setOpen(true)}
          icon={paused ? <Play className="h-3.5 w-3.5" aria-hidden /> : <Pause className="h-3.5 w-3.5" aria-hidden />}
        >
          {paused ? "Reprendre" : "Mettre en pause"}
        </Button>
      </div>
      <ConfirmDialog
        open={open}
        onClose={() => setOpen(false)}
        onConfirm={confirm}
        loading={m.isPending}
        title={paused ? "Reprendre le dispatch ?" : "Mettre le dispatch en pause ?"}
        description={
          paused
            ? "Les workers recommenceront à prendre les jobs en file."
            : "Aucun nouveau job ne sera pris. Les runs déjà en cours continueront jusqu'à leur fin."
        }
        confirmLabel={paused ? "Reprendre" : "Mettre en pause"}
        tone={paused ? "primary" : "danger"}
      />
    </Card>
  );
}

function BlockedCard({ ov }: { ov: Overview }) {
  const lists: { type: "feature" | "ticket"; items: BlockedRef[] }[] = [];
  if (Array.isArray(ov.blocked.features)) lists.push({ type: "feature", items: ov.blocked.features });
  if (Array.isArray(ov.blocked.tickets)) lists.push({ type: "ticket", items: ov.blocked.tickets });
  const nf = count(ov.blocked.features);
  const nt = count(ov.blocked.tickets);
  return (
    <Card>
      <CardHeader
        title="Éléments bloqués"
        icon={<Ban className="h-4 w-4 text-fg-subtle" aria-hidden />}
        actions={
          nf + nt > 0 ? (
            <Link to="/pipeline" className="focus-ring rounded text-xs text-accent hover:underline">
              Pipeline
            </Link>
          ) : undefined
        }
      />
      {nf + nt === 0 ? (
        <EmptyState title="Rien de bloqué" className="py-6" />
      ) : (
        <div className="space-y-3 p-4">
          <div className="flex gap-6 text-sm">
            <Link to="/features?state=BLOCKED" className="focus-ring rounded hover:text-accent">
              <span className="text-lg font-semibold tabular-nums text-danger">{nf}</span> <span className="text-fg-muted">feature(s)</span>
            </Link>
            <Link to="/pipeline" className="focus-ring rounded hover:text-accent">
              <span className="text-lg font-semibold tabular-nums text-danger">{nt}</span> <span className="text-fg-muted">ticket(s)</span>
            </Link>
          </div>
          {lists.map((l) =>
            l.items.slice(0, 5).map((it) => (
              <Link
                key={`${l.type}-${it.id}`}
                to={`/${l.type === "feature" ? "features" : "tickets"}/${it.id}`}
                className="focus-ring block rounded px-1 text-sm hover:bg-hover/50"
              >
                <span className="text-fg">{it.key ?? it.title ?? `${l.type} #${it.id}`}</span>
                {it.state_reason && <span className="ml-2 text-xs text-fg-muted">{it.state_reason}</span>}
              </Link>
            )),
          )}
        </div>
      )}
    </Card>
  );
}

export default function OverviewPage() {
  const q = useOverview();
  const pending = useApprovals({ status: "pending" });
  const active = useRuns({ status: "running" });
  const activeRuns = active.data?.pages.flatMap((p) => p.items) ?? [];

  if (q.isPending) {
    return (
      <>
        <PageHeader title="Vue d'ensemble" />
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-[86px]" />
          ))}
        </div>
        <Skeleton className="mt-4 h-48" />
      </>
    );
  }
  if (!q.data) {
    return (
      <>
        <PageHeader title="Vue d'ensemble" />
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      </>
    );
  }
  const ov = q.data;
  const blockedTotal = count(ov.blocked.features) + count(ov.blocked.tickets);
  const today = ov.tokens_today_split;

  return (
    <>
      <PageHeader title="Vue d'ensemble" description="Ce qui demande votre attention, puis l'état du système." />
      {q.isError && (
        <div className="mb-4">
          <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
        </div>
      )}

      {/* À décider — en tête */}
      <Card className={cn("mb-4", ov.pending_approvals > 0 && "border-review/40")}>
        <CardHeader
          title={
            <>
              À décider
              {ov.pending_approvals > 0 && (
                <span className="rounded-full bg-review/15 px-2 text-xs font-semibold tabular-nums text-review">{ov.pending_approvals}</span>
              )}
            </>
          }
          description="Approbations en attente de votre décision."
          icon={<CheckSquare className="h-4 w-4 text-review" aria-hidden />}
          actions={
            <Link to="/approvals" className="focus-ring rounded text-xs text-accent hover:underline">
              Tout voir
            </Link>
          }
        />
        {pending.isPending ? (
          <ListSkeleton rows={2} />
        ) : pending.data === undefined ? (
          <ErrorState error={pending.error} onRetry={() => void pending.refetch()} />
        ) : (
          <ApprovalList approvals={pending.data.slice(0, 6)} empty="Aucune décision en attente." />
        )}
      </Card>

      {ov.alerts.length > 0 && (
        <section aria-label="Alertes" className="mb-4 space-y-2">
          {ov.alerts.map((a, i) => (
            <AlertRow key={i} a={a} />
          ))}
        </section>
      )}

      <div className="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat label="Approbations en attente" value={formatNumber(ov.pending_approvals)} icon={<CheckSquare className="h-4 w-4" />} to="/approvals" tone={ov.pending_approvals ? "text-review" : undefined} />
        <Stat label="Éléments bloqués" value={formatNumber(blockedTotal)} icon={<Ban className="h-4 w-4" />} to="/pipeline" tone={blockedTotal ? "text-danger" : undefined} />
        <Stat label="Runs actifs" value={formatNumber(ov.active_runs)} icon={<Activity className="h-4 w-4" />} to="/runs?status=running" />
        <Stat
          label="Tokens nouveaux aujourd'hui"
          value={formatCompact(today ? today.fresh_input + today.output : ov.tokens_today)}
          hint={today ? `+ ${formatCompact(today.cache_read)} relus du cache` : undefined}
          icon={<Coins className="h-4 w-4" />}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <Card>
            <CardHeader
              title="Runs actifs"
              icon={<Activity className="h-4 w-4 text-fg-subtle" aria-hidden />}
              actions={
                <Link to="/runs" className="focus-ring rounded text-xs text-accent hover:underline">
                  Tous les runs
                </Link>
              }
            />
            {active.isPending ? (
              <ListSkeleton rows={2} />
            ) : active.data === undefined ? (
              <ErrorState error={active.error} onRetry={() => void active.refetch()} />
            ) : (
              <RunsTable runs={activeRuns} empty="Aucun run en cours." />
            )}
          </Card>
          <Card>
            <CardHeader title="Activité récente" />
            <div className="p-4">
              <Timeline entries={ov.recent_activity} showSubject emptyLabel="Aucune activité récente." />
            </div>
          </Card>
        </div>
        <div className="space-y-4">
          <Card>
            <CardHeader
              title="Tokens — 7 jours"
              description={
                today
                  ? `Aujourd'hui : ${formatNumber(today.fresh_input)} en entrée nouvelle, ${formatNumber(today.output)} en sortie, ${formatNumber(today.cache_read)} relus du cache`
                  : `Aujourd'hui : ${formatNumber(ov.tokens_today)}`
              }
              icon={<Coins className="h-4 w-4 text-fg-subtle" aria-hidden />}
            />
            <div className="p-4">{ov.tokens_7d.length ? <TokenChart data={ov.tokens_7d} /> : <EmptyState title="Aucune donnée" className="py-4" />}</div>
          </Card>
          <DispatchCard ov={ov} />
          <BlockedCard ov={ov} />
        </div>
      </div>
    </>
  );
}
