import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Layers, Plus } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useCreateFeature, useFeatures, useProjects } from "@/api/queries";
import { FEATURE_STATES, type Feature, type IntegrationMode, type Project } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { Dialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Field, Input, Select, Textarea } from "@/components/ui/primitives";
import { StateBadge, Tag } from "@/components/ui/StateBadge";
import { DegradedBanner, EmptyState, ErrorState, ListSkeleton } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { INTEGRATION_HINTS, stateMeta } from "@/lib/states";
import { formatNumber } from "@/lib/format";
import { LIMITS, validFeatureDescription, validFeatureTitle } from "@/lib/validation";

export function projectLabel(f: Pick<Feature, "project" | "project_id">, projects: Project[] | undefined): string {
  if (typeof f.project === "string") return f.project;
  if (f.project && typeof f.project === "object") return f.project.slug;
  return projects?.find((p) => p.id === f.project_id)?.slug ?? `#${f.project_id}`;
}

function CreateFeatureDialog({ open, onClose, projects }: { open: boolean; onClose: () => void; projects: Project[] }) {
  const enabled = projects.filter((p) => p.enabled);
  const [projectId, setProjectId] = useState<string>("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  // "" = défaut du projet choisi (pré-remplissage) ; sinon choix explicite pour cette feature.
  const [mode, setMode] = useState<IntegrationMode | "">("");
  const [touched, setTouched] = useState(false);
  const m = useCreateFeature();
  const toast = useToast();
  const nav = useNavigate();

  const pid = projectId || (enabled[0] ? String(enabled[0].id) : "");
  const project = enabled.find((p) => String(p.id) === pid);
  const integration: IntegrationMode = mode || project?.integration_mode || "direct";
  const errors = {
    project: !pid ? "Choisissez un projet actif." : undefined,
    title: !title.trim() ? "Le titre est requis." : !validFeatureTitle(title) ? `${LIMITS.featureTitle} caractères maximum.` : undefined,
    description: !description.trim()
      ? "Décrivez l'idée pour le PO."
      : !validFeatureDescription(description)
        ? `${formatNumber(LIMITS.featureDescription)} caractères maximum.`
        : undefined,
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (errors.project || errors.title || errors.description) return;
    m.mutate(
      { project_id: Number(pid), title: title.trim(), description: description.trim(), integration_mode: integration },
      {
        onSuccess: (f) => {
          toast.success("Feature créée", "État initial : Idée. Lancez la spécification quand vous êtes prêt.");
          onClose();
          setTitle("");
          setDescription("");
          setMode("");
          setTouched(false);
          if (f && typeof f.id === "number") nav(`/features/${f.id}`);
        },
      },
    );
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Nouvelle feature"
      description="Elle démarre à l'état Idée ; rien n'est lancé tant que vous ne démarrez pas la spécification."
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Annuler
          </Button>
          <Button variant="primary" type="submit" form="create-feature" loading={m.isPending}>
            Créer
          </Button>
        </>
      }
    >
      <form id="create-feature" onSubmit={submit} noValidate className="space-y-4">
        {m.isError && (
          <p role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-sm text-danger">
            {errorMessage(m.error)}
          </p>
        )}
        <Field
          label="Projet"
          required
          error={touched ? errors.project : undefined}
          hint={enabled.length === 0 ? "Aucun projet actif : activez-en un dans Projets." : undefined}
        >
          {(p) => (
            <Select
              {...p}
              value={pid}
              onChange={(e) => {
                setProjectId(e.target.value);
                setMode("");
              }}
              disabled={enabled.length === 0}
            >
              {enabled.map((pr) => (
                <option key={pr.id} value={pr.id}>
                  {pr.name} ({pr.slug}) — {pr.mode}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Titre" required error={touched ? errors.title : undefined}>
          {(p) => <Input {...p} value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} data-autofocus />}
        </Field>
        <Field
          label="Description"
          required
          error={touched ? errors.description : undefined}
          hint="Contexte, objectif, contraintes. Le PO s'en sert pour rédiger la spec."
        >
          {(p) => <Textarea {...p} rows={6} value={description} onChange={(e) => setDescription(e.target.value)} />}
        </Field>
        <Field
          label="Intégration"
          hint={`${INTEGRATION_HINTS[integration]} Défaut du projet : ${project?.integration_mode === "feature_branch" ? "branche de feature" : "directe"} ; modifiable jusqu'au premier ticket mergé.`}
        >
          {(p) => (
            <Select {...p} value={integration} onChange={(e) => setMode(e.target.value as IntegrationMode)}>
              <option value="direct">Directe</option>
              <option value="feature_branch">Branche de feature</option>
            </Select>
          )}
        </Field>
      </form>
    </Dialog>
  );
}

export default function FeaturesPage() {
  const [sp, setSp] = useSearchParams();
  const state = sp.get("state") ?? "";
  const projectId = sp.get("project_id") ?? "";
  const [creating, setCreating] = useState(false);
  const projects = useProjects();
  const q = useFeatures({ state, project_id: projectId });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];

  const setFilter = (k: string, v: string) => {
    const n = new URLSearchParams(sp);
    if (v) n.set(k, v);
    else n.delete(k);
    setSp(n, { replace: true });
  };

  return (
    <>
      <PageHeader
        title="Features"
        description="Idées transformées en spec, design, tickets puis PR draft."
        actions={
          <Button variant="primary" onClick={() => setCreating(true)} icon={<Plus className="h-4 w-4" aria-hidden />} disabled={!projects.data}>
            Nouvelle feature
          </Button>
        }
      />
      <div className="mb-3 flex flex-wrap items-end gap-3">
        <Field label="État" className="w-48">
          {(p) => (
            <Select {...p} value={state} onChange={(e) => setFilter("state", e.target.value)}>
              <option value="">Tous</option>
              {FEATURE_STATES.map((s) => (
                <option key={s} value={s}>
                  {stateMeta(s).label}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Projet" className="w-48">
          {(p) => (
            <Select {...p} value={projectId} onChange={(e) => setFilter("project_id", e.target.value)}>
              <option value="">Tous</option>
              {projects.data?.map((pr) => (
                <option key={pr.id} value={pr.id}>
                  {pr.slug}
                </option>
              ))}
            </Select>
          )}
        </Field>
      </div>
      <Card>
        {q.isPending ? (
          <ListSkeleton />
        ) : q.data === undefined ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : (
          <>
            {q.isError && (
              <div className="p-3">
                <DegradedBanner error={q.error} onRetry={() => void q.refetch()} />
              </div>
            )}
            {items.length === 0 ? (
              <EmptyState
                icon={<Layers className="h-5 w-5" />}
                title={state || projectId ? "Aucune feature ne correspond aux filtres" : "Aucune feature"}
                action={
                  !state && !projectId ? (
                    <Button size="sm" variant="primary" onClick={() => setCreating(true)} icon={<Plus className="h-3.5 w-3.5" aria-hidden />}>
                      Créer la première
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <ul className="divide-y divide-border">
                {items.map((f) => (
                  <li key={f.id}>
                    <Link to={`/features/${f.id}`} className="focus-ring flex items-center gap-3 px-4 py-3 hover:bg-hover/50">
                      <span className="w-10 shrink-0 text-xs tabular-nums text-fg-subtle">#{f.id}</span>
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-medium text-fg">{f.title}</p>
                        {f.state_reason && <p className="truncate text-xs text-fg-muted">{f.state_reason}</p>}
                      </div>
                      <Tag className="hidden sm:inline-flex">{projectLabel(f, projects.data)}</Tag>
                      <StateBadge state={f.state} />
                      <Time value={f.updated_at ?? f.created_at} className="hidden w-28 text-right text-xs text-fg-subtle md:block" />
                    </Link>
                  </li>
                ))}
              </ul>
            )}
            {q.hasNextPage && (
              <div className="border-t border-border p-3 text-center">
                <Button size="sm" variant="ghost" onClick={() => void q.fetchNextPage()} loading={q.isFetchingNextPage}>
                  Charger plus
                </Button>
              </div>
            )}
          </>
        )}
      </Card>
      {projects.data && <CreateFeatureDialog open={creating} onClose={() => setCreating(false)} projects={projects.data} />}
    </>
  );
}
