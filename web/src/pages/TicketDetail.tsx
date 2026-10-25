import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { CheckCircle2, CircleDashed, RotateCcw, XCircle } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useRun, useTicket, useTicketAction } from "@/api/queries";
import type { QaArtifact, ReviewArtifact, Run, TicketDetail } from "@/api/types";
import { ApprovalList, RunsTable, Timeline } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, DefList, ExternalLink, Mono, Skeleton } from "@/components/ui/primitives";
import { StateBadge, ToneBadge } from "@/components/ui/StateBadge";
import { DegradedBanner, EmptyState, ErrorState } from "@/components/ui/States";
import { Tabs } from "@/components/ui/Tabs";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { shortSha } from "@/lib/format";
import { TICKET_TERMINAL } from "@/lib/states";
import { CiBadge } from "./FeatureDetail";

function latest(runs: Run[], role: string): Run | undefined {
  return runs
    .filter((r) => r.agent_role === role)
    .sort((a, b) => (b.started_at ?? b.created_at).localeCompare(a.started_at ?? a.created_at))[0];
}

function isReview(a: unknown): a is ReviewArtifact {
  return !!a && typeof a === "object" && "verdict" in a && "findings" in a;
}
function isQa(a: unknown): a is QaArtifact {
  return !!a && typeof a === "object" && "verdict" in a && "checks" in a;
}

function VerdictCard({ title, run, headSha }: { title: string; run: Run | undefined; headSha: string | null }) {
  const q = useRun(run?.id ?? 0, Boolean(run));
  const art = q.data?.artifact;
  let body;
  if (!run) body = <EmptyState title="Pas encore de verdict" className="py-6" />;
  else if (q.isPending) body = <Skeleton className="m-4 h-16" />;
  else if (!q.data) body = <ErrorState error={q.error} onRetry={() => void q.refetch()} className="py-6" />;
  else if (!isReview(art) && !isQa(art))
    body = (
      <div className="p-4 text-sm text-fg-muted">
        Run <StateBadge state={run.status} /> — aucun livrable valide.
      </div>
    );
  else {
    const ok = art.verdict === "approved" || art.verdict === "passed";
    const shaMatch = headSha ? art.sha === headSha : undefined;
    body = (
      <div className="space-y-3 p-4 text-sm">
        <div className="flex flex-wrap items-center gap-2">
          {ok ? <CheckCircle2 className="h-4 w-4 text-success" aria-hidden /> : <XCircle className="h-4 w-4 text-danger" aria-hidden />}
          <StateBadge state={art.verdict} />
          <Mono>{shortSha(art.sha)}</Mono>
          {shaMatch === false && <ToneBadge tone="warning">SHA différent du head</ToneBadge>}
        </div>
        <p className="text-fg-muted">{art.summary}</p>
        {isReview(art) && art.findings.length > 0 && (
          <ul className="space-y-1">
            {art.findings.map((f, i) => (
              <li key={i} className="flex gap-2">
                <ToneBadge tone={f.severity === "P0" || f.severity === "P1" ? "danger" : "neutral"}>{f.severity}</ToneBadge>
                <span className="min-w-0">
                  <span className="font-mono text-xs text-fg-subtle">{f.file}</span> — {f.message}
                </span>
              </li>
            ))}
          </ul>
        )}
        {isQa(art) && (
          <ul className="space-y-1">
            {art.acceptance.map((c, i) => (
              <li key={i} className="flex gap-2">
                {c.status === "met" ? <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success" aria-label="Rempli" /> : <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-danger" aria-label="Non rempli" />}
                <span>
                  {c.criterion} <span className="text-fg-subtle">— {c.evidence}</span>
                </span>
              </li>
            ))}
          </ul>
        )}
        <Link to={`/runs/${run.id}`} className="focus-ring inline-block rounded text-xs text-accent hover:underline">
          Voir le run #{run.id}
        </Link>
      </div>
    );
  }
  return (
    <Card>
      <CardHeader title={title} icon={<CircleDashed className="h-4 w-4 text-fg-subtle" aria-hidden />} />
      {body}
    </Card>
  );
}

function Info({ t }: { t: TicketDetail }) {
  return (
    <DefList
      items={[
        ["Issue", t.issue_url ? <ExternalLink href={t.issue_url}>#{t.issue_number}</ExternalLink> : "—"],
        ["Branche", t.branch ? <Mono>{t.branch}</Mono> : "—"],
        ["head_sha", t.head_sha ? <Mono>{t.head_sha}</Mono> : "—"],
        ["Tentative", String(t.attempt)],
        ["PR draft", t.pr_url ? <ExternalLink href={t.pr_url}>#{t.pr_number}</ExternalLink> : "—"],
        ["CI", <CiBadge key="ci" t={t} />],
        ["Feature", <Link key="f" to={`/features/${t.feature_id}`} className="focus-ring rounded text-accent hover:underline">#{t.feature_id}</Link>],
        ["Mis à jour", <Time key="u" value={t.updated_at ?? t.created_at} />],
      ]}
    />
  );
}

export default function TicketDetailPage() {
  const id = Number(useParams().id);
  const q = useTicket(id);
  const action = useTicketAction(id);
  const toast = useToast();
  const [tab, setTab] = useState("body");
  const [pending, setPending] = useState<"cancel" | "retry" | null>(null);

  if (q.isPending)
    return (
      <>
        <PageHeader title={<Skeleton className="h-6 w-72" />} />
        <Skeleton className="h-64" />
      </>
    );
  if (!q.data)
    return (
      <>
        <PageHeader title={`Ticket #${id}`} breadcrumbs={[{ label: "Pipeline", to: "/pipeline" }]} />
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      </>
    );
  const t = q.data;
  const pendingApprovals = t.approvals.filter((a) => a.status === "pending");
  const run = () =>
    pending &&
    action.mutate(pending, {
      onSuccess: () => {
        toast.success(pending === "retry" ? "Relance demandée" : "Ticket annulé");
        setPending(null);
      },
      onError: (e) => toast.error("Action refusée", errorMessage(e)),
    });

  return (
    <>
      <PageHeader
        breadcrumbs={[
          { label: "Features", to: "/features" },
          { label: `Feature #${t.feature_id}`, to: `/features/${t.feature_id}` },
        ]}
        title={t.title}
        meta={
          <>
            <StateBadge state={t.state} />
            <span className="font-mono">{t.key}</span>
            <span aria-hidden>·</span>
            <span>tentative {t.attempt}</span>
          </>
        }
        actions={
          <>
            {t.state === "BLOCKED" && (
              <Button size="sm" variant="primary" onClick={() => setPending("retry")} icon={<RotateCcw className="h-3.5 w-3.5" aria-hidden />}>
                Relancer
              </Button>
            )}
            {!TICKET_TERMINAL.has(t.state) && (
              <Button size="sm" variant="danger" onClick={() => setPending("cancel")} icon={<XCircle className="h-3.5 w-3.5" aria-hidden />}>
                Annuler
              </Button>
            )}
          </>
        }
      />
      {q.isError && (
        <div className="mb-4">
          <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
        </div>
      )}
      {t.state_reason && (
        <p className="mb-4 rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg-muted">
          <span className="font-medium text-fg">Raison : </span>
          {t.state_reason}
        </p>
      )}
      {pendingApprovals.length > 0 && (
        <Card className="mb-4 border-review/40">
          <CardHeader title="À décider" />
          <ApprovalList approvals={pendingApprovals} />
        </Card>
      )}
      <div className="grid gap-4 xl:grid-cols-[1fr_360px]">
        <div className="space-y-4">
          <div className="grid gap-4 md:grid-cols-2">
            <VerdictCard title="Verdict Reviewer" run={latest(t.runs, "reviewer")} headSha={t.head_sha} />
            <VerdictCard title="Verdict QA" run={latest(t.runs, "qa")} headSha={t.head_sha} />
          </div>
          <Card className="p-4">
            <Tabs
              label="Sections du ticket"
              value={tab}
              onChange={setTab}
              tabs={[
                { id: "body", label: "Description" },
                { id: "runs", label: "Runs", count: t.runs.length },
                { id: "approvals", label: "Approbations", count: t.approvals.length },
                { id: "timeline", label: "Timeline", count: t.timeline.length },
              ]}
            >
              {tab === "body" && (
                <div className="space-y-4">
                  <p className="whitespace-pre-wrap text-sm text-fg">{t.body || "—"}</p>
                  {t.acceptance && t.acceptance.length > 0 && (
                    <div>
                      <p className="label-caps mb-1">Critères d'acceptation</p>
                      <ul className="list-disc space-y-1 pl-5 text-sm text-fg marker:text-fg-subtle">
                        {t.acceptance.map((c, i) => (
                          <li key={i}>{c}</li>
                        ))}
                      </ul>
                    </div>
                  )}
                </div>
              )}
              {tab === "runs" && <RunsTable runs={t.runs} />}
              {tab === "approvals" && <ApprovalList approvals={t.approvals} />}
              {tab === "timeline" && <Timeline entries={t.timeline} truncatedLink={t.timeline_truncated ? `/audit?subject_type=ticket&subject_id=${t.id}` : undefined} />}
            </Tabs>
          </Card>
        </div>
        <Card>
          <CardHeader title="Détails" />
          <div className="p-4">
            <Info t={t} />
          </div>
        </Card>
      </div>
      {pending && (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={run}
          loading={action.isPending}
          title={pending === "retry" ? "Relancer ce ticket ?" : "Annuler ce ticket ?"}
          description={pending === "retry" ? "Le ticket revient à l'état précédant le blocage et le job est rejoué." : "Action définitive : jobs annulés, runs stoppés."}
          confirmLabel={pending === "retry" ? "Relancer" : "Annuler le ticket"}
          tone={pending === "retry" ? "primary" : "danger"}
        />
      )}
    </>
  );
}
