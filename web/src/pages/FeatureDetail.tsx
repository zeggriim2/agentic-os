import { useState, type FormEvent, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { ExternalLink as ExtIcon, GitBranch, GitPullRequest, HelpCircle, MessageSquareWarning, Play, Plus, RotateCcw, Trash2, XCircle } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useFeature, useFeatureAction, useFeatureIntegration, useProjects, useRequestCorrection } from "@/api/queries";
import type { FeatureDetail, IntegrationMode, Ticket } from "@/api/types";
import { ApprovalList, RunsTable, Timeline } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, ExternalLink, Field, Input, Mono, Skeleton, Textarea } from "@/components/ui/primitives";
import { StateBadge, Tag } from "@/components/ui/StateBadge";
import { DegradedBanner, EmptyState, ErrorState } from "@/components/ui/States";
import { Tabs } from "@/components/ui/Tabs";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { shortSha } from "@/lib/format";
import { CORRECTION_LIMITS, LIMITS, validFeatureDescription, validFeatureTitle } from "@/lib/validation";
import { FEATURE_TERMINAL, INTEGRATION_HINTS, integrationLabel } from "@/lib/states";
import { projectLabel } from "./Features";

type Action = "start_spec" | "retry" | "cancel";

const ACTION_TEXT: Record<Action, { title: string; desc: string; label: string; tone: "primary" | "danger" }> = {
  start_spec: {
    title: "Démarrer la spécification ?",
    desc: "Un job po_spec est créé : l'agent PO rédige user stories, critères d'acceptation et questions ouvertes.",
    label: "Démarrer",
    tone: "primary",
  },
  retry: {
    title: "Relancer depuis l'état précédent ?",
    desc: "La feature revient à l'état précédant le blocage et le job correspondant est rejoué.",
    label: "Relancer",
    tone: "primary",
  },
  cancel: {
    title: "Annuler cette feature ?",
    desc: "Action définitive : les jobs en file sont annulés et les runs en cours sont stoppés.",
    label: "Annuler la feature",
    tone: "danger",
  },
};

function Section({ title, children, icon }: { title: string; children: ReactNode; icon?: ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-fg">
        {icon}
        {title}
      </h3>
      {children}
    </section>
  );
}

function BulletList({ items, empty }: { items: string[] | undefined; empty: string }) {
  if (!items?.length) return <p className="text-sm text-fg-subtle">{empty}</p>;
  return (
    <ul className="list-disc space-y-1 pl-5 text-sm text-fg marker:text-fg-subtle">
      {items.map((s, i) => (
        <li key={i}>{s}</li>
      ))}
    </ul>
  );
}

function SpecTab({ f }: { f: FeatureDetail }) {
  if (!f.spec) return <EmptyState title="Pas encore de spécification" description="La spec apparaît ici quand l'agent PO a livré." />;
  const s = f.spec;
  return (
    <div className="space-y-6">
      {s.open_questions?.length > 0 && (
        <div className="rounded-md border border-review/30 bg-review/10 p-3">
          <Section title={`Questions ouvertes (${s.open_questions.length})`} icon={<HelpCircle className="h-4 w-4 text-review" aria-hidden />}>
            <BulletList items={s.open_questions} empty="" />
            <p className="text-xs text-fg-muted">Répondez en rejetant l'approbation de spec avec un commentaire.</p>
          </Section>
        </div>
      )}
      <Section title="Résumé">
        <p className="whitespace-pre-wrap text-sm text-fg">{s.summary}</p>
      </Section>
      <Section title="User stories">
        <BulletList items={s.user_stories} empty="Aucune." />
      </Section>
      <Section title="Critères d'acceptation">
        <BulletList items={s.acceptance_criteria} empty="Aucun." />
      </Section>
    </div>
  );
}

function DesignTab({ f }: { f: FeatureDetail }) {
  if (!f.design)
    return <EmptyState title="Pas encore de design technique" description="Le design apparaît après validation de la spec et livraison du Tech Lead." />;
  const d = f.design;
  return (
    <div className="space-y-6">
      <Section title="Résumé">
        <p className="whitespace-pre-wrap text-sm text-fg">{d.summary}</p>
      </Section>
      <Section title={`Décisions (${d.decisions?.length ?? 0})`}>
        <div className="space-y-3">
          {d.decisions?.map((dec, i) => (
            <div key={i} className="rounded-md border border-border bg-panel p-3">
              <p className="text-sm font-medium text-fg">{dec.decision}</p>
              <p className="mt-1 text-sm text-fg-muted">{dec.rationale}</p>
              {dec.rejected_alternatives?.length > 0 && (
                <div className="mt-2">
                  <p className="label-caps mb-1">Alternatives rejetées</p>
                  <ul className="space-y-0.5 text-sm text-fg-muted">
                    {dec.rejected_alternatives.map((a, j) => (
                      <li key={j} className="line-through decoration-fg-subtle/50">
                        {a}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
            </div>
          ))}
        </div>
      </Section>
      <Section title="Risques">
        <BulletList items={d.risks} empty="Aucun risque identifié." />
      </Section>
      {d.tickets?.length > 0 && (
        <Section title={`Tickets proposés (${d.tickets.length})`}>
          <ol className="list-decimal space-y-1 pl-5 text-sm text-fg">
            {d.tickets.map((t, i) => (
              <li key={i}>{t.title}</li>
            ))}
          </ol>
        </Section>
      )}
    </div>
  );
}

export function CiBadge({ t }: { t: Pick<Ticket, "ci_state"> }) {
  return <StateBadge state={t.ci_state} label={t.ci_state === "pending" ? "CI en cours" : undefined} />;
}

function TicketsTab({ f }: { f: FeatureDetail }) {
  if (!f.tickets.length) return <EmptyState title="Aucun ticket" description="Les tickets sont créés après validation du design." />;
  return (
    <div className="scroll-thin overflow-x-auto">
      <table className="w-full min-w-[760px] text-sm">
        <thead>
          <tr className="border-b border-border text-left text-xs text-fg-subtle">
            <th className="px-3 py-2 font-medium">Ticket</th>
            <th className="px-3 py-2 font-medium">État</th>
            <th className="px-3 py-2 font-medium">Branche</th>
            <th className="px-3 py-2 font-medium">SHA</th>
            <th className="px-3 py-2 font-medium">PR</th>
            <th className="px-3 py-2 font-medium">CI</th>
          </tr>
        </thead>
        <tbody>
          {f.tickets.map((t) => (
            <tr key={t.id} className="border-b border-border/60 last:border-0 hover:bg-hover/50">
              <td className="px-3 py-2">
                <Link to={`/tickets/${t.id}`} className="focus-ring block rounded">
                  <span className="font-mono text-xs text-fg-subtle">{t.key}</span>
                  {t.origin === "correction" && (
                    <span className="ml-2" data-testid="ticket-correction">
                      <Tag>Correction</Tag>
                    </span>
                  )}
                  <span className="block font-medium text-fg hover:text-accent">{t.title}</span>
                </Link>
              </td>
              <td className="px-3 py-2">
                <StateBadge state={t.state} />
              </td>
              <td className="px-3 py-2 font-mono text-xs text-fg-muted">{t.branch ?? "—"}</td>
              <td className="px-3 py-2">{t.head_sha ? <Mono>{shortSha(t.head_sha)}</Mono> : <span className="text-fg-subtle">—</span>}</td>
              <td className="px-3 py-2">
                {t.pr_url ? <ExternalLink href={t.pr_url}>#{t.pr_number}</ExternalLink> : <span className="text-fg-subtle">—</span>}
              </td>
              <td className="px-3 py-2">
                <CiBadge t={t} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Le mode d'intégration reste modifiable tant qu'aucun ticket n'est mergé et que la feature n'est ni en recette ni terminée. */
export function integrationLocked(f: FeatureDetail): string | null {
  if (FEATURE_TERMINAL.has(f.state)) return "Feature terminée.";
  if (f.state === "ACCEPTANCE" || (f.state === "BLOCKED" && f.previous_state === "ACCEPTANCE")) return "Feature en recette.";
  const merged = f.tickets.find((t) => t.state === "DONE");
  return merged ? `Le ticket ${merged.key} est déjà mergé.` : null;
}

/** La recette humaine peut renvoyer une correction : feature en recette, ou bloquée depuis la recette. */
export function canRequestCorrection(f: FeatureDetail): boolean {
  if (f.integration_mode !== "feature_branch" || !f.feature_branch) return false;
  return f.state === "ACCEPTANCE" || (f.state === "BLOCKED" && f.previous_state === "ACCEPTANCE");
}

export function CorrectionDialog({ f, open, onClose }: { f: FeatureDetail; open: boolean; onClose: () => void }) {
  const m = useRequestCorrection(f.id);
  const toast = useToast();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [criteria, setCriteria] = useState<string[]>([""]);
  const [touched, setTouched] = useState(false);
  const filled = criteria.map((c) => c.trim()).filter(Boolean);
  const errors = {
    title: !title.trim() ? "Le titre est requis." : !validFeatureTitle(title) ? `${LIMITS.featureTitle} caractères maximum.` : undefined,
    description: !description.trim()
      ? "Décrivez le problème constaté (page, étapes, résultat attendu)."
      : !validFeatureDescription(description)
        ? "Description trop longue."
        : undefined,
    criteria: criteria.some((c) => c.length > CORRECTION_LIMITS.criterion) ? `${CORRECTION_LIMITS.criterion} caractères maximum par critère.` : undefined,
  };
  const reset = () => {
    setTitle("");
    setDescription("");
    setCriteria([""]);
    setTouched(false);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (errors.title || errors.description || errors.criteria) return;
    m.mutate(
      { title: title.trim(), description: description.trim(), acceptance_criteria: filled },
      {
        onSuccess: (t) => {
          toast.success("Correction demandée", `Ticket ${t?.key ?? ""} créé sur ${f.feature_branch} : la feature repart en développement.`);
          reset();
          onClose();
        },
      },
    );
  };
  const withPR = f.feature_pr_number
    ? `La PR finale #${f.feature_pr_number} reste ouverte et suivra la branche ; elle sera mise à jour au retour en recette.`
    : "La PR finale sera proposée au retour en recette.";
  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="lg"
      title="Demander une correction"
      description={`Un ticket de correction est créé sur ${f.feature_branch} et suit le cycle habituel (développement, revue, QA, PR vers la branche de feature, merge par vous). ${withPR}`}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Annuler
          </Button>
          <Button variant="primary" type="submit" form="request-correction" loading={m.isPending}>
            Créer le ticket de correction
          </Button>
        </>
      }
    >
      <form id="request-correction" onSubmit={submit} noValidate className="space-y-4">
        {m.isError && (
          <p role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
            {errorMessage(m.error)}
          </p>
        )}
        <Field label="Titre" required error={touched ? errors.title : undefined}>
          {(p) => (
            <Input
              {...p}
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              maxLength={LIMITS.featureTitle}
              placeholder="Récapitulatif : le bloc forfait 10 % ne s'affiche pas"
              data-autofocus
            />
          )}
        </Field>
        <Field
          label="Problème constaté"
          required
          error={touched ? errors.description : undefined}
          hint="Page ou URL, étapes pour reproduire, ce que vous voyez, ce que vous attendiez."
        >
          {(p) => <Textarea {...p} rows={6} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        <fieldset className="space-y-2">
          <legend className="text-sm font-medium text-fg">Critères d'acceptation</legend>
          <p className="text-xs text-fg-muted">
            Facultatif. Le QA vérifie chaque critère. Sans critère : « le problème décrit est corrigé et vérifié dans l'application ».
          </p>
          {criteria.map((c, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                aria-label={`Critère ${i + 1}`}
                value={c}
                maxLength={CORRECTION_LIMITS.criterion}
                onChange={(e) => setCriteria(criteria.map((x, j) => (j === i ? e.target.value : x)))}
              />
              {criteria.length > 1 && (
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={`Retirer le critère ${i + 1}`}
                  onClick={() => setCriteria(criteria.filter((_, j) => j !== i))}
                  icon={<Trash2 className="h-3.5 w-3.5" aria-hidden />}
                />
              )}
            </div>
          ))}
          {touched && errors.criteria && <p className="text-xs text-danger">{errors.criteria}</p>}
          {criteria.length < CORRECTION_LIMITS.criteria && (
            <Button variant="ghost" size="sm" onClick={() => setCriteria([...criteria, ""])} icon={<Plus className="h-3.5 w-3.5" aria-hidden />}>
              Ajouter un critère
            </Button>
          )}
        </fieldset>
      </form>
    </Dialog>
  );
}

function IntegrationCard({ f, base }: { f: FeatureDetail; base: string | undefined }) {
  const m = useFeatureIntegration(f.id);
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const mode: IntegrationMode = f.integration_mode ?? "direct";
  const next: IntegrationMode = mode === "direct" ? "feature_branch" : "direct";
  const locked = integrationLocked(f);
  const started = f.state === "READY_FOR_DEV" || f.state === "IN_DEV";
  const openPRs = f.tickets.filter((t) => t.pr_number && (t.state === "PR_OPENED" || t.state === "HUMAN_MERGE")).length;
  const desc =
    (next === "feature_branch"
      ? `Les tickets seront mergés dans une branche de feature, puis une PR finale vers ${base ?? "la base"} sera ouverte après la recette.`
      : `Chaque ticket ouvrira sa PR directement vers ${base ?? "la base"}.`) +
    (started
      ? next === "feature_branch"
        ? ` Agentic OS crée la branche de feature depuis ${base ?? "la base"}${openPRs ? ` et redirige ${openPRs} PR déjà ouverte(s) vers elle` : ""}.`
        : openPRs
          ? ` ${openPRs} PR déjà ouverte(s) seront redirigées vers ${base ?? "la base"}.`
          : ""
      : " Aucun effet sur GitHub avant le feu vert.");
  return (
    <Card>
      <CardHeader title="Intégration" />
      <div className="space-y-2 p-4 text-sm">
        <p className="font-medium text-fg" data-testid="integration-mode">
          {integrationLabel(mode)}
        </p>
        <p className="text-xs text-fg-muted">{INTEGRATION_HINTS[mode]}</p>
        {mode === "feature_branch" && (
          <p className="flex items-center gap-1.5 text-fg-muted" data-testid="integration-branch">
            <GitBranch className="h-3.5 w-3.5" aria-hidden />
            {f.feature_branch ? <Mono>{f.feature_branch}</Mono> : <span className="text-xs">Branche créée au feu vert.</span>}
          </p>
        )}
        {f.feature_pr_url && (
          <p className="flex items-center gap-1.5 text-fg-muted">
            <GitPullRequest className="h-3.5 w-3.5" aria-hidden />
            <ExternalLink href={f.feature_pr_url}>
              PR finale #{f.feature_pr_number} <ExtIcon className="inline h-3 w-3" aria-hidden />
            </ExternalLink>
          </p>
        )}
        {locked ? (
          <p className="text-xs text-fg-subtle">Mode verrouillé : {locked}</p>
        ) : (
          <Button size="sm" variant="secondary" onClick={() => setConfirm(true)}>
            Passer en {integrationLabel(next).toLowerCase()}
          </Button>
        )}
      </div>
      {confirm && (
        <ConfirmDialog
          open
          onClose={() => setConfirm(false)}
          onConfirm={() =>
            m.mutate(next, {
              onSuccess: () => {
                setConfirm(false);
                toast.success("Mode d'intégration modifié", integrationLabel(next));
              },
              onError: (e) => toast.error("Changement refusé", errorMessage(e)),
            })
          }
          loading={m.isPending}
          title={`Passer en intégration « ${integrationLabel(next)} » ?`}
          description={desc}
          confirmLabel="Changer"
          tone="primary"
        />
      )}
    </Card>
  );
}

export default function FeatureDetailPage() {
  const id = Number(useParams().id);
  const q = useFeature(id);
  const projects = useProjects();
  const action = useFeatureAction(id);
  const toast = useToast();
  const [tab, setTab] = useState("spec");
  const [pending, setPending] = useState<Action | null>(null);
  const [correcting, setCorrecting] = useState(false);

  const crumbs = [{ label: "Features", to: "/features" }];
  if (q.isPending)
    return (
      <>
        <PageHeader title={<Skeleton className="h-6 w-72" />} breadcrumbs={crumbs} />
        <Skeleton className="h-64" />
      </>
    );
  if (!q.data)
    return (
      <>
        <PageHeader title={`Feature #${id}`} breadcrumbs={crumbs} />
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      </>
    );
  const f = q.data;
  const actions: Action[] = [];
  if (f.state === "IDEA") actions.push("start_spec");
  if (f.state === "BLOCKED") actions.push("retry");
  if (!FEATURE_TERMINAL.has(f.state)) actions.push("cancel");
  const pendingApprovals = f.approvals.filter((a) => a.status === "pending");
  const correctable = canRequestCorrection(f);

  const run = () => {
    if (!pending) return;
    const a = pending;
    action.mutate(a, {
      onSuccess: () => {
        setPending(null);
        toast.success(a === "start_spec" ? "Spécification démarrée" : a === "retry" ? "Relance demandée" : "Feature annulée");
      },
      onError: (e) => toast.error("Action refusée", errorMessage(e)),
    });
  };

  return (
    <>
      <PageHeader
        breadcrumbs={crumbs}
        title={f.title}
        meta={
          <>
            <StateBadge state={f.state} />
            <Tag>{projectLabel(f, projects.data)}</Tag>
            <span>#{f.id}</span>
            <span aria-hidden>·</span>
            <span>
              créée <Time value={f.created_at} />
            </span>
            {f.updated_at && (
              <>
                <span aria-hidden>·</span>
                <span>
                  maj <Time value={f.updated_at} />
                </span>
              </>
            )}
          </>
        }
        actions={[
          correctable && (
            <Button
              key="correction"
              variant="secondary"
              size="sm"
              onClick={() => setCorrecting(true)}
              icon={<MessageSquareWarning className="h-3.5 w-3.5" aria-hidden />}
            >
              Demander une correction
            </Button>
          ),
          ...actions.map((a) => (
            <Button
              key={a}
              variant={a === "cancel" ? "danger" : "primary"}
              size="sm"
              onClick={() => setPending(a)}
              icon={
                a === "start_spec" ? (
                  <Play className="h-3.5 w-3.5" aria-hidden />
                ) : a === "retry" ? (
                  <RotateCcw className="h-3.5 w-3.5" aria-hidden />
                ) : (
                  <XCircle className="h-3.5 w-3.5" aria-hidden />
                )
              }
            >
              {a === "start_spec" ? "Démarrer la spec" : a === "retry" ? "Relancer" : "Annuler"}
            </Button>
          )),
        ]}
      />
      {correctable && <CorrectionDialog f={f} open={correcting} onClose={() => setCorrecting(false)} />}
      {f.state === "ACCEPTANCE" && (
        <p className="mb-4 rounded-md border border-review/40 bg-panel px-3 py-2 text-sm text-fg-muted" data-testid="acceptance-hint">
          <span className="font-medium text-fg">Recette : </span>
          testez la branche <Mono>{f.feature_branch}</Mono>. Un défaut ? « Demander une correction » ajoute un ticket sur cette branche ; sinon mergez la PR
          finale.
        </p>
      )}
      {q.isError && (
        <div className="mb-4">
          <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
        </div>
      )}
      {f.state_reason && (
        <p className="mb-4 rounded-md border border-border bg-panel px-3 py-2 text-sm text-fg-muted">
          <span className="font-medium text-fg">Raison : </span>
          {f.state_reason}
        </p>
      )}
      {pendingApprovals.length > 0 && (
        <Card className="mb-4 border-review/40">
          <CardHeader title="À décider" description="Cette feature attend votre décision." />
          <ApprovalList approvals={pendingApprovals} />
        </Card>
      )}
      <div className="grid gap-4 xl:grid-cols-[1fr_320px]">
        {/* min-w-0 : sans lui, la largeur minimale des onglets/tableaux élargit la grille (débordement mobile) */}
        <Card className="min-w-0 p-4">
          <Tabs
            label="Sections de la feature"
            value={tab}
            onChange={setTab}
            tabs={[
              { id: "spec", label: "Spec" },
              { id: "design", label: "Design" },
              { id: "tickets", label: "Tickets", count: f.tickets.length },
              { id: "runs", label: "Runs", count: f.runs.length },
              { id: "timeline", label: "Timeline", count: f.timeline.length },
            ]}
          >
            {tab === "spec" && <SpecTab f={f} />}
            {tab === "design" && <DesignTab f={f} />}
            {tab === "tickets" && <TicketsTab f={f} />}
            {tab === "runs" && <RunsTable runs={f.runs} />}
            {tab === "timeline" && (
              <Timeline entries={f.timeline} truncatedLink={f.timeline_truncated ? `/audit?subject_type=feature&subject_id=${f.id}` : undefined} />
            )}
          </Tabs>
        </Card>
        <div className="min-w-0 space-y-4">
          <IntegrationCard f={f} base={projects.data?.find((p) => p.id === f.project_id)?.base_branch} />
          <Card>
            <CardHeader title="Description" />
            <p className="whitespace-pre-wrap p-4 text-sm text-fg-muted">{f.description || "—"}</p>
          </Card>
          <Card>
            <CardHeader title="Approbations" />
            <ApprovalList approvals={f.approvals} />
          </Card>
          {f.tickets.some((t) => t.pr_url || t.branch) && (
            <Card>
              <CardHeader title="Liens Git" />
              <ul className="space-y-1.5 p-4 text-sm">
                {f.tickets.map((t) => (
                  <li key={t.id} className="flex items-center gap-2 text-fg-muted">
                    {t.pr_url ? <GitPullRequest className="h-3.5 w-3.5" aria-hidden /> : <GitBranch className="h-3.5 w-3.5" aria-hidden />}
                    <span className="font-mono text-xs">{t.key}</span>
                    {t.pr_url && (
                      <ExternalLink href={t.pr_url}>
                        PR #{t.pr_number} <ExtIcon className="inline h-3 w-3" aria-hidden />
                      </ExternalLink>
                    )}
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>
      </div>
      {pending && (
        <ConfirmDialog
          open
          onClose={() => setPending(null)}
          onConfirm={run}
          loading={action.isPending}
          title={ACTION_TEXT[pending].title}
          description={ACTION_TEXT[pending].desc}
          confirmLabel={ACTION_TEXT[pending].label}
          tone={ACTION_TEXT[pending].tone}
        />
      )}
    </>
  );
}
