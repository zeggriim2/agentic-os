import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { Square } from "lucide-react";
import { errorMessage } from "@/api/client";
import { subscribeEvents, useStreamStatus } from "@/api/events";
import { useRun, useStopRun } from "@/api/queries";
import type { RunEvent } from "@/api/types";
import { SubjectLink, TruncatedNotice } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { CodeBlock, prettyJson } from "@/components/ui/CodeBlock";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, DefList, Mono, Skeleton } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { DegradedBanner, EmptyState, ErrorState } from "@/components/ui/States";
import { Tabs } from "@/components/ui/Tabs";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { cn } from "@/lib/cn";
import { formatDuration, formatNumber, formatParisTime } from "@/lib/format";
import { roleLabel, RUN_ACTIVE } from "@/lib/states";

function eventTone(name: string): string {
  if (name.includes("failed") || name.includes("error")) return "text-danger";
  if (name.includes("completed")) return "text-success";
  if (name.startsWith("tool.")) return "text-info";
  return "text-fg-muted";
}

function EventStream({ events, live, truncated }: { events: RunEvent[]; live: boolean; truncated?: boolean }) {
  const ref = useRef<HTMLOListElement>(null);
  const [follow, setFollow] = useState(true);
  useEffect(() => {
    if (follow && ref.current) ref.current.scrollTop = ref.current.scrollHeight;
  }, [events.length, follow]);
  if (!events.length) return <EmptyState title={live ? "En attente d'événements…" : "Aucun événement persisté"} />;
  return (
    <div>
      {truncated && <TruncatedNotice what="2 000 événements" />}
      <div className="mb-2 flex items-center justify-between text-xs text-fg-subtle">
        <span>{events.length} événement(s)</span>
        <label className="inline-flex cursor-pointer items-center gap-1.5">
          <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} className="accent-[rgb(var(--accent))]" />
          Suivre
        </label>
      </div>
      <ol ref={ref} tabIndex={0} aria-live={live ? "polite" : undefined} className="focus-ring scroll-thin max-h-[480px] overflow-y-auto rounded-md border border-border bg-panel font-mono text-xs">
        {events.map((e) => (
          <li key={e.seq} className="grid grid-cols-[48px_72px_150px_1fr] gap-2 border-b border-border/60 px-3 py-1.5 last:border-0">
            <span className="tabular-nums text-fg-subtle">#{e.seq}</span>
            <span className="tabular-nums text-fg-subtle">{e.created_at ? formatParisTime(e.created_at) : ""}</span>
            <span className={cn("truncate", eventTone(e.event))}>{e.event}</span>
            <span className="truncate text-fg-muted" title={prettyJson(e.data)}>
              {e.data === null || e.data === undefined ? "" : typeof e.data === "string" ? e.data : JSON.stringify(e.data)}
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

export default function RunDetailPage() {
  const id = Number(useParams().id);
  const q = useRun(id);
  const stop = useStopRun(id);
  const toast = useToast();
  const stream = useStreamStatus();
  const [tab, setTab] = useState("events");
  const [confirm, setConfirm] = useState(false);
  const [live, setLive] = useState<RunEvent[]>([]);

  useEffect(() => {
    setLive([]);
    return subscribeEvents((type, p) => {
      if (type !== "run.event") return;
      const rid = p.run_id;
      if (rid !== id || typeof p.seq !== "number") return;
      const ev: RunEvent = { seq: p.seq, event: String(p.event ?? "event"), data: p.data, created_at: typeof p.created_at === "string" ? p.created_at : new Date().toISOString() };
      setLive((xs) => (xs.some((x) => x.seq === ev.seq) ? xs : [...xs, ev].slice(-2000)));
    });
  }, [id]);

  const events = useMemo(() => {
    const m = new Map<number, RunEvent>();
    for (const e of q.data?.events ?? []) m.set(e.seq, e);
    for (const e of live) if (!m.has(e.seq)) m.set(e.seq, e);
    return Array.from(m.values()).sort((a, b) => a.seq - b.seq);
  }, [q.data?.events, live]);

  if (q.isPending)
    return (
      <>
        <PageHeader title={<Skeleton className="h-6 w-48" />} />
        <Skeleton className="h-64" />
      </>
    );
  if (!q.data)
    return (
      <>
        <PageHeader title={`Run #${id}`} breadcrumbs={[{ label: "Runs", to: "/runs" }]} />
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      </>
    );
  const r = q.data;
  const active = RUN_ACTIVE.has(r.status);

  return (
    <>
      <PageHeader
        breadcrumbs={[{ label: "Runs", to: "/runs" }]}
        title={`Run #${r.id} — ${roleLabel(r.agent_role)}`}
        meta={
          <>
            <StateBadge state={r.status} />
            {r.job_kind && <span className="font-mono">{r.job_kind}</span>}
            {r.subject_type && r.subject_id !== undefined && <SubjectLink type={r.subject_type} id={r.subject_id} />}
            {active && (
              <span className={cn("inline-flex items-center gap-1", stream === "connected" ? "text-success" : "text-warning")}>
                ● {stream === "connected" ? "flux en direct" : "flux déconnecté"}
              </span>
            )}
          </>
        }
        actions={
          active && (
            <Button variant="danger" size="sm" onClick={() => setConfirm(true)} icon={<Square className="h-3.5 w-3.5" aria-hidden />}>
              Stop
            </Button>
          )
        }
      />
      {q.isError && (
        <div className="mb-4">
          <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
        </div>
      )}
      {r.error && (
        <div role="alert" className="mb-4 rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
          {r.error}
        </div>
      )}
      <div className="grid gap-4 xl:grid-cols-[1fr_320px]">
        <Card className="min-w-0 p-4">
          <Tabs
            label="Sections du run"
            value={tab}
            onChange={setTab}
            tabs={[
              { id: "events", label: "Événements", count: events.length },
              { id: "output", label: "Sortie" },
              { id: "artifact", label: "Livrable" },
            ]}
          >
            {tab === "events" && <EventStream events={events} live={active} truncated={r.events_truncated} />}
            {tab === "output" && (r.output ? <CodeBlock value={r.output} json={false} maxHeight="max-h-[600px]" className="[&_pre]:whitespace-pre-wrap" /> : <EmptyState title="Aucune sortie" />)}
            {tab === "artifact" &&
              (r.artifact !== null && r.artifact !== undefined ? <CodeBlock value={r.artifact} maxHeight="max-h-[600px]" /> : <EmptyState title="Aucun livrable" description="Le livrable est le dernier bloc JSON de la sortie, validé par schéma strict." />)}
          </Tabs>
        </Card>
        <div className="space-y-4">
          <Card>
            <CardHeader title="Tokens" />
            <div className="grid grid-cols-3 divide-x divide-border text-center">
              {[
                ["Entrée", r.input_tokens],
                ["Sortie", r.output_tokens],
                ["Total", r.total_tokens],
              ].map(([l, v]) => (
                <div key={l as string} className="p-3">
                  <p className="text-2xs text-fg-subtle">{l}</p>
                  <p className="mt-0.5 font-semibold tabular-nums text-fg">{formatNumber(v as number | null)}</p>
                </div>
              ))}
            </div>
          </Card>
          <Card>
            <CardHeader title="Détails" />
            <div className="p-4">
              <DefList
                items={[
                  ["Modèle", r.model ? <Mono>{r.model}</Mono> : "—"],
                  ["Fournisseur", r.provider ?? "—"],
                  ["Profil Hermes", <Mono key="p">{r.hermes_profile}</Mono>],
                  ["Run Hermes", r.hermes_run_id ? <Mono>{r.hermes_run_id}</Mono> : "—"],
                  ["Job", `#${r.job_id}`],
                  ["Démarré", <Time key="s" value={r.started_at} />],
                  ["Terminé", <Time key="f" value={r.finished_at} />],
                  ["Durée", formatDuration(r.started_at, r.finished_at)],
                ]}
              />
            </div>
          </Card>
        </div>
      </div>
      <ConfirmDialog
        open={confirm}
        onClose={() => setConfirm(false)}
        onConfirm={() =>
          stop.mutate(undefined, {
            onSuccess: () => {
              setConfirm(false);
              toast.success("Arrêt demandé", "Le sujet passera à l'état Bloqué (raison « arrêté »).");
            },
            onError: (e) => toast.error("Arrêt impossible", errorMessage(e)),
          })
        }
        loading={stop.isPending}
        title="Arrêter ce run ?"
        description="Le run Hermes est stoppé, le job annulé et le sujet passe à l'état Bloqué. Vous pourrez relancer ensuite."
        confirmLabel="Arrêter le run"
        tone="danger"
      />
    </>
  );
}
