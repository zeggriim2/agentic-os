import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AlertCircle, FileText, Layers, Ticket as TicketIcon, X } from "lucide-react";
import { usePipeline } from "@/api/queries";
import type { PipelineBoard, PipelineColumn, PipelineItem } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Select, Skeleton } from "@/components/ui/primitives";
import { StateBadge, Tag } from "@/components/ui/StateBadge";
import { EmptyState, QueryView } from "@/components/ui/States";
import { Tabs } from "@/components/ui/Tabs";
import { Time } from "@/components/ui/Time";
import { cn } from "@/lib/cn";
import { stateMeta, TONE_CLASSES } from "@/lib/states";

type Board = "features" | "tickets";

/** Avancement d'une feature : « 1/3 mergé · T2 : revue de code ». */
function Progress({ item }: { item: PipelineItem }) {
  const p = item.progress;
  if (!p || p.total === 0) return null;
  const short = p.current?.key.replace(/^.*-(T\d+)$/, "$1");
  return (
    <p className="mt-1.5 text-2xs text-fg-muted" data-testid="feature-progress">
      <span className="font-medium tabular-nums text-fg">
        {p.done}/{p.total}
      </span>{" "}
      {p.done > 1 ? "mergés" : "mergé"}
      {p.current && (
        <>
          {" · "}
          {short} : {stateMeta(p.current.state).label.toLowerCase()}
        </>
      )}
    </p>
  );
}

function PipelineCard({ item, onShowTickets }: { item: PipelineItem; onShowTickets: (featureId: number) => void }) {
  const to = item.type === "feature" ? `/features/${item.id}` : `/tickets/${item.id}`;
  return (
    <div
      data-testid="pipeline-card"
      className={cn(
        "rounded-md border bg-surface transition-colors hover:border-border-strong hover:bg-elevated",
        item.attention ? "border-review/50" : "border-border",
      )}
    >
      <Link to={to} className="focus-ring block rounded-md p-3">
        <div className="mb-1.5 flex items-center gap-1.5 text-2xs text-fg-subtle">
          {item.type === "feature" ? <FileText className="h-3 w-3" aria-hidden /> : <TicketIcon className="h-3 w-3" aria-hidden />}
          <Tag className="max-w-[120px] truncate">{item.project}</Tag>
          {item.attention && (
            <span
              className="ml-auto inline-flex items-center gap-1 rounded-full bg-review/15 px-1.5 py-0.5 font-medium text-review"
              title="Une décision ou une action humaine est attendue"
            >
              <AlertCircle className="h-3 w-3" aria-hidden />
              Attention
            </span>
          )}
        </div>
        <p className="line-clamp-2 text-sm font-medium text-fg">{item.title}</p>
        {item.type === "ticket" && item.feature_title && <p className="mt-1 line-clamp-1 text-2xs text-fg-subtle">Feature : {item.feature_title}</p>}
        {item.type === "feature" && <Progress item={item} />}
        <div className="mt-2 flex items-center justify-between gap-2">
          <StateBadge state={item.state} />
          <Time value={item.updated_at} className="text-2xs text-fg-subtle" />
        </div>
      </Link>
      {item.type === "feature" && item.progress && item.progress.total > 0 && (
        <button
          type="button"
          onClick={() => onShowTickets(item.id)}
          className="focus-ring w-full rounded-b-md border-t border-border px-3 py-1.5 text-left text-2xs font-medium text-accent hover:bg-hover"
        >
          Voir les tickets →
        </button>
      )}
    </div>
  );
}

function Column({ col, onShowTickets }: { col: PipelineColumn; onShowTickets: (featureId: number) => void }) {
  const tone = TONE_CLASSES[stateMeta(col.state).tone];
  const attention = col.items.filter((i) => i.attention).length;
  return (
    <section
      aria-label={`${col.label} (${col.items.length})`}
      className="flex w-[272px] shrink-0 flex-col rounded-lg border border-border bg-panel"
      data-testid="pipeline-column"
    >
      <header className="flex items-center gap-2 border-b border-border px-3 py-2.5">
        <span aria-hidden className={cn("h-2 w-2 rounded-full", tone.dot)} />
        <h2 className="flex-1 truncate text-xs font-semibold text-fg">{col.label}</h2>
        {attention > 0 && <span className="text-2xs font-medium text-review">{attention} !</span>}
        <span className="rounded-full bg-hover px-1.5 text-2xs tabular-nums text-fg-muted">{col.items.length}</span>
      </header>
      <div className="scroll-thin flex max-h-[calc(100vh-280px)] flex-col gap-2 overflow-y-auto p-2">
        {col.items.length === 0 ? (
          <p className="px-1 py-3 text-center text-xs text-fg-subtle">Vide</p>
        ) : (
          col.items.map((it) => <PipelineCard key={`${it.type}-${it.id}`} item={it} onShowTickets={onShowTickets} />)
        )}
      </div>
    </section>
  );
}

/** Applique les filtres projet et feature à un tableau. */
export function filterBoard(board: PipelineBoard, project: string, featureId: number | null): PipelineBoard {
  return {
    columns: board.columns.map((c) => ({
      ...c,
      items: c.items.filter((i) => (!project || i.project === project) && (featureId === null || i.feature_id === featureId)),
    })),
  };
}

const count = (b: PipelineBoard) => b.columns.reduce((n, c) => n + c.items.length, 0);
const attentionCount = (b: PipelineBoard) => b.columns.reduce((n, c) => n + c.items.filter((i) => i.attention).length, 0);

const SHOW_EMPTY_KEY = "aos.pipeline.showEmpty";
const PROJECT_KEY = "aos.pipeline.project";

function readPref(key: string, fallback: string): string {
  try {
    return localStorage.getItem(key) ?? fallback;
  } catch {
    return fallback;
  }
}

function writePref(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    /* stockage indisponible : préférence non mémorisée */
  }
}

export default function PipelinePage() {
  const q = usePipeline();
  const [sp, setSp] = useSearchParams();
  const board: Board = sp.get("tab") === "tickets" ? "tickets" : "features";
  const featureParam = Number(sp.get("feature"));
  const featureId = board === "tickets" && Number.isInteger(featureParam) && featureParam > 0 ? featureParam : null;
  // Étapes vides masquées par défaut ; projet choisi : préférences mémorisées.
  const [showEmpty, setShowEmpty] = useState(() => readPref(SHOW_EMPTY_KEY, "0") === "1");
  const [project, setProject] = useState(() => readPref(PROJECT_KEY, ""));

  const setBoard = (b: string) => setSp(b === "tickets" ? { tab: "tickets" } : {}, { replace: true });
  const showTickets = (id: number) => setSp({ tab: "tickets", feature: String(id) });
  const clearFeature = () => setSp({ tab: "tickets" }, { replace: true });
  const toggleEmpty = () => {
    setShowEmpty(!showEmpty);
    writePref(SHOW_EMPTY_KEY, showEmpty ? "0" : "1");
  };
  const chooseProject = (p: string) => {
    setProject(p);
    writePref(PROJECT_KEY, p);
  };

  return (
    <>
      <PageHeader
        title="Pipeline"
        description="Features : de l'idée au feu vert, puis l'avancement de leurs tickets. Tickets : le travail des agents jusqu'au merge. Les transitions sont pilotées par le moteur et les approbations."
      />
      <QueryView
        query={q}
        isEmpty={(d) => count(d.features) + count(d.tickets) === 0}
        empty={<EmptyState icon={<Layers className="h-5 w-5" />} title="Pipeline vide" description="Créez une feature pour démarrer." />}
        skeleton={
          <div className="flex gap-3 overflow-hidden">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-80 w-[272px] shrink-0" />
            ))}
          </div>
        }
      >
        {(d) => {
          // Un projet mémorisé qui n'existe plus est ignoré.
          const proj = project && d.projects.some((p) => p.slug === project) ? project : "";
          const features = filterBoard(d.features, proj, null);
          const tickets = filterBoard(d.tickets, proj, featureId);
          const current = board === "features" ? features : tickets;
          const emptyCount = current.columns.filter((c) => c.items.length === 0).length;
          const tabLabel = (label: string, b: PipelineBoard) => {
            const a = attentionCount(b);
            return (
              <>
                {label}
                {a > 0 && (
                  <span className="text-2xs font-semibold text-review" title={`${a} en attente d'une action`}>
                    {a} !
                  </span>
                )}
              </>
            );
          };
          const featureTitle =
            featureId !== null ? d.tickets.columns.flatMap((c) => c.items).find((i) => i.feature_id === featureId)?.feature_title : undefined;
          return (
            <>
              <div className="mb-3 flex flex-wrap items-center gap-3">
                <label className="flex items-center gap-2 text-sm text-fg-muted">
                  Projet
                  <Select value={proj} onChange={(e) => chooseProject(e.target.value)} className="w-44">
                    <option value="">Tous les projets</option>
                    {d.projects.map((p) => (
                      <option key={p.id} value={p.slug}>
                        {p.slug}
                      </option>
                    ))}
                  </Select>
                </label>
                {featureId !== null && (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-hover px-2.5 py-1 text-xs text-fg" data-testid="feature-filter">
                    Feature : {featureTitle ?? `#${featureId}`}
                    <button
                      type="button"
                      onClick={clearFeature}
                      className="focus-ring rounded-full text-fg-muted hover:text-fg"
                      aria-label="Retirer le filtre de feature"
                    >
                      <X className="h-3 w-3" aria-hidden />
                    </button>
                  </span>
                )}
                {emptyCount > 0 && current.columns.some((c) => c.items.length > 0) && (
                  <Button variant="secondary" size="sm" onClick={toggleEmpty} aria-pressed={showEmpty} className="ml-auto">
                    {showEmpty ? "Masquer les étapes vides" : `Afficher les étapes vides (${emptyCount})`}
                  </Button>
                )}
              </div>
              <Tabs
                label="Tableaux du pipeline"
                value={board}
                onChange={setBoard}
                tabs={[
                  {
                    id: "features",
                    label: tabLabel("Features", features),
                    count: count(features),
                  },
                  {
                    id: "tickets",
                    label: tabLabel("Tickets", tickets),
                    count: count(tickets),
                  },
                ]}
              >
                {count(current) === 0 ? (
                  <p className="py-8 text-center text-sm text-fg-subtle">
                    {board === "features" ? "Aucune feature pour ce filtre." : "Aucun ticket pour ce filtre."}
                  </p>
                ) : (
                  <div className="scroll-thin -mx-4 flex gap-3 overflow-x-auto px-4 pb-3 sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8">
                    {current.columns
                      .filter((c) => showEmpty || c.items.length > 0)
                      .map((c) => (
                        <Column key={c.state} col={c} onShowTickets={showTickets} />
                      ))}
                  </div>
                )}
              </Tabs>
            </>
          );
        }}
      </QueryView>
    </>
  );
}
