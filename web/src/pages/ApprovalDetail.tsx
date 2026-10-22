import { useState, type FormEvent, type ReactNode } from "react";
import { useParams } from "react-router-dom";
import { AlertOctagon, Check, CheckCircle2, Clock, HelpCircle, Loader2, ShieldCheck, X, XCircle } from "lucide-react";
import { ApiError, errorMessage } from "@/api/client";
import { useApproval, useDecideApproval } from "@/api/queries";
import type { Approval } from "@/api/types";
import { SubjectLink } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { CodeBlock } from "@/components/ui/CodeBlock";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, DefList, Field, Skeleton, Textarea } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { DegradedBanner, ErrorState } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { cn } from "@/lib/cn";
import { formatParis } from "@/lib/format";
import { approvalKindLabel } from "@/lib/states";

function asStringList(v: unknown): string[] | null {
  return Array.isArray(v) && v.every((x) => typeof x === "string") ? (v as string[]) : null;
}

function DetailsView({ details }: { details: Approval["details"] }) {
  if (!details || typeof details !== "object" || Object.keys(details).length === 0) {
    return <p className="text-sm text-fg-subtle">Aucun détail complémentaire.</p>;
  }
  const questions = asStringList(details.open_questions);
  const rest = Object.entries(details).filter(([k]) => k !== "open_questions");
  return (
    <div className="space-y-4">
      {questions && questions.length > 0 && (
        <div className="rounded-md border border-review/30 bg-review/10 p-3">
          <p className="mb-1 flex items-center gap-2 text-sm font-semibold text-fg">
            <HelpCircle className="h-4 w-4 text-review" aria-hidden />
            Questions ouvertes du PO
          </p>
          <ul className="list-disc space-y-1 pl-5 text-sm text-fg">
            {questions.map((q, i) => (
              <li key={i}>{q}</li>
            ))}
          </ul>
          <p className="mt-2 text-xs text-fg-muted">Pour y répondre, rejetez en mettant votre réponse en commentaire : elle est transmise à l'agent.</p>
        </div>
      )}
      {rest.length > 0 && (
        <DefList
          items={rest.map(([k, v]) => {
            const list = asStringList(v);
            let node: ReactNode;
            if (typeof v === "string" || typeof v === "number" || typeof v === "boolean") node = <span className="whitespace-pre-wrap">{String(v)}</span>;
            else if (list) node = <ul className="list-disc pl-4">{list.map((s, i) => <li key={i}>{s}</li>)}</ul>;
            else node = <CodeBlock value={v} maxHeight="max-h-64" />;
            return [<span key={k} className="font-mono text-xs">{k}</span>, node];
          })}
        />
      )}
    </div>
  );
}

/** Résultat relu depuis l'API après la décision : décision enregistrée / exécutée / échec. */
function Outcome({ a }: { a: Approval }) {
  const box = (tone: string, icon: ReactNode, title: string, children?: ReactNode) => (
    <div role="status" data-testid="approval-outcome" data-status={a.status} className={cn("rounded-lg border p-4", tone)}>
      <p className="flex items-center gap-2 text-sm font-semibold text-fg">
        {icon}
        {title}
      </p>
      <div className="mt-2 space-y-2 text-sm text-fg-muted">{children}</div>
    </div>
  );
  const decided = (
    <p>
      Décision de <span className="font-medium text-fg">{a.decided_by ?? "—"}</span>
      {a.decided_at && <> le {formatParis(a.decided_at)}</>}
      {a.decision_comment && (
        <>
          {" "}
          : « <span className="text-fg">{a.decision_comment}</span> »
        </>
      )}
    </p>
  );
  switch (a.status) {
    case "approved":
      return box(
        "border-info/30 bg-info/5",
        <Loader2 className="h-4 w-4 animate-spin text-info" aria-hidden />,
        "Décision enregistrée — exécution en cours",
        <>
          {decided}
          <p>L'effet autorisé est en cours d'exécution par le moteur. Cette page se met à jour automatiquement.</p>
        </>,
      );
    case "executed":
      return box(
        "border-success/30 bg-success/5",
        <CheckCircle2 className="h-4 w-4 text-success" aria-hidden />,
        "Exécutée",
        <>
          {decided}
          {a.executed_at && <p>Exécutée le {formatParis(a.executed_at)}.</p>}
          {a.execution_result != null && <CodeBlock value={a.execution_result} maxHeight="max-h-64" />}
        </>,
      );
    case "execution_failed":
      return box(
        "border-danger/30 bg-danger/5",
        <AlertOctagon className="h-4 w-4 text-danger" aria-hidden />,
        "Échec d'exécution",
        <>
          {decided}
          <p>La décision est enregistrée mais l'effet n'a pas pu être appliqué.</p>
          {a.execution_result != null && <CodeBlock value={a.execution_result} maxHeight="max-h-64" />}
        </>,
      );
    case "rejected":
      return box("border-border bg-panel", <XCircle className="h-4 w-4 text-fg-muted" aria-hidden />, "Décision enregistrée — rejetée", decided);
    case "expired":
      return box("border-border bg-panel", <Clock className="h-4 w-4 text-fg-muted" aria-hidden />, "Expirée", <p>Cette demande n'est plus valable.</p>);
    default:
      return null;
  }
}

function DecisionForm({ a }: { a: Approval }) {
  const [comment, setComment] = useState("");
  const [error, setError] = useState<string | undefined>();
  const m = useDecideApproval(a.id);
  const toast = useToast();

  const decide = (decision: "approved" | "rejected") => {
    const c = comment.trim();
    if (decision === "rejected" && !c) {
      setError("Un commentaire est obligatoire pour rejeter.");
      return;
    }
    setError(undefined);
    m.mutate(
      { decision, comment: c },
      {
        onSuccess: () => toast.success(decision === "approved" ? "Approbation enregistrée" : "Rejet enregistré"),
        onError: (e) => {
          if (e instanceof ApiError && e.status === 409) toast.info("Déjà décidée", "L'état à jour a été rechargé.");
          else toast.error("Décision non enregistrée", errorMessage(e));
        },
      },
    );
  };
  const onSubmit = (e: FormEvent) => e.preventDefault();

  return (
    <form onSubmit={onSubmit} className="space-y-3" aria-label="Décision">
      <Field label="Commentaire" error={error} hint="Facultatif pour approuver, obligatoire pour rejeter (il est transmis à l'agent comme feedback).">
        {(p) => <Textarea {...p} value={comment} onChange={(e) => { setComment(e.target.value); if (error) setError(undefined); }} rows={4} />}
      </Field>
      {m.isError && !(m.error instanceof ApiError && m.error.status === 409) && (
        <p role="alert" className="text-sm text-danger">
          {errorMessage(m.error)}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button variant="danger" onClick={() => decide("rejected")} disabled={m.isPending} loading={m.isPending && m.variables?.decision === "rejected"} icon={<X className="h-4 w-4" aria-hidden />}>
          Rejeter
        </Button>
        <Button variant="primary" onClick={() => decide("approved")} disabled={m.isPending} loading={m.isPending && m.variables?.decision === "approved"} icon={<Check className="h-4 w-4" aria-hidden />}>
          Approuver
        </Button>
      </div>
    </form>
  );
}

export default function ApprovalDetailPage() {
  const id = Number(useParams().id);
  const q = useApproval(id, { poll: true });
  const crumbs = [{ label: "Approbations", to: "/approvals" }];

  if (q.isPending)
    return (
      <>
        <PageHeader title={<Skeleton className="h-6 w-72" />} breadcrumbs={crumbs} />
        <Skeleton className="h-40" />
      </>
    );
  if (!q.data)
    return (
      <>
        <PageHeader title={`Approbation #${id}`} breadcrumbs={crumbs} />
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      </>
    );
  const a = q.data;
  return (
    <>
      <PageHeader
        breadcrumbs={crumbs}
        title={a.title}
        meta={
          <>
            <StateBadge state={a.status} />
            <span>{approvalKindLabel(a.kind)}</span>
            <span aria-hidden>·</span>
            <SubjectLink type={a.subject_type} id={a.subject_id} />
            <span aria-hidden>·</span>
            <span>
              demandée <Time value={a.created_at} />
            </span>
          </>
        }
      />
      {q.isError && (
        <div className="mb-4">
          <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
        </div>
      )}
      <div className="grid gap-4 lg:grid-cols-[1fr_380px]">
        <div className="space-y-4">
          <section
            aria-labelledby="effect-title"
            data-testid="approval-effect"
            className="rounded-lg border-2 border-accent/50 bg-accent/5 p-4"
          >
            <p id="effect-title" className="label-caps flex items-center gap-1.5 text-accent">
              <ShieldCheck className="h-3.5 w-3.5" aria-hidden />
              Ce que cette approbation autorise
            </p>
            <p className="mt-2 whitespace-pre-wrap text-base font-medium leading-relaxed text-fg">{a.effect}</p>
          </section>
          <Card>
            <CardHeader title="Détails" />
            <div className="p-4">
              <DetailsView details={a.details} />
            </div>
          </Card>
        </div>
        <div className="space-y-4">
          {a.status === "pending" ? (
            <Card className="border-review/40">
              <CardHeader title="Votre décision" description="Approuver déclenche immédiatement l'effet ci-contre." />
              <div className="p-4">
                <DecisionForm a={a} />
              </div>
            </Card>
          ) : (
            <Outcome a={a} />
          )}
        </div>
      </div>
    </>
  );
}
