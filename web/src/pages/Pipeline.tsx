import { useState } from "react";
import { Link } from "react-router-dom";
import { AlertCircle, FileText, Layers, Ticket as TicketIcon } from "lucide-react";
import { usePipeline } from "@/api/queries";
import type { PipelineColumn, PipelineItem } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Skeleton } from "@/components/ui/primitives";
import { StateBadge, Tag } from "@/components/ui/StateBadge";
import { EmptyState, QueryView } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { cn } from "@/lib/cn";
import { stateMeta, TONE_CLASSES } from "@/lib/states";

function PipelineCard({ item }: { item: PipelineItem }) {
  const to = item.type === "feature" ? `/features/${item.id}` : `/tickets/${item.id}`;
  return (
    <Link
      to={to}
      data-testid="pipeline-card"
      className={cn(
        "focus-ring block rounded-md border bg-surface p-3 transition-colors hover:border-border-strong hover:bg-elevated",
        item.attention ? "border-review/50" : "border-border",
      )}
    >
      <div className="mb-1.5 flex items-center gap-1.5 text-2xs text-fg-subtle">
        {item.type === "feature" ? <FileText className="h-3 w-3" aria-hidden /> : <TicketIcon className="h-3 w-3" aria-hidden />}
        <span>{item.type === "feature" ? "Feature" : "Ticket"}</span>
        <span aria-hidden>·</span>
        <Tag className="max-w-[120px] truncate">{item.project}</Tag>
        {item.attention && (
          <span className="ml-auto inline-flex items-center gap-1 rounded-full bg-review/15 px-1.5 py-0.5 font-medium text-review" title="Une décision ou une action humaine est attendue">
            <AlertCircle className="h-3 w-3" aria-hidden />
            Attention
          </span>
        )}
      </div>
      <p className="line-clamp-2 text-sm font-medium text-fg">{item.title}</p>
      <div className="mt-2 flex items-center justify-between gap-2">
        <StateBadge state={item.state} />
        <Time value={item.updated_at} className="text-2xs text-fg-subtle" />
      </div>
    </Link>
  );
}

function Column({ col }: { col: PipelineColumn }) {
  const tone = TONE_CLASSES[stateMeta(col.state).tone];
  const attention = col.items.filter((i) => i.attention).length;
  return (
    <section aria-label={`${col.label} (${col.items.length})`} className="flex w-[272px] shrink-0 flex-col rounded-lg border border-border bg-panel" data-testid="pipeline-column">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2.5">
        <span aria-hidden className={cn("h-2 w-2 rounded-full", tone.dot)} />
        <h2 className="flex-1 truncate text-xs font-semibold text-fg">{col.label}</h2>
        {attention > 0 && <span className="text-2xs font-medium text-review">{attention} !</span>}
        <span className="rounded-full bg-hover px-1.5 text-2xs tabular-nums text-fg-muted">{col.items.length}</span>
      </header>
      <div className="scroll-thin flex max-h-[calc(100vh-220px)] flex-col gap-2 overflow-y-auto p-2">
        {col.items.length === 0 ? <p className="px-1 py-3 text-center text-xs text-fg-subtle">Vide</p> : col.items.map((it) => <PipelineCard key={`${it.type}-${it.id}`} item={it} />)}
      </div>
    </section>
  );
}

const SHOW_EMPTY_KEY = "aos.pipeline.showEmpty";

function readShowEmpty(): boolean {
  try {
    return localStorage.getItem(SHOW_EMPTY_KEY) === "1";
  } catch {
    return false;
  }
}

export default function PipelinePage() {
  const q = usePipeline();
  // Les étapes vides sont masquées par défaut : sur 18 colonnes, seules celles qui contiennent
  // quelque chose sont utiles ; le choix est mémorisé.
  const [showEmpty, setShowEmpty] = useState(readShowEmpty);
  const toggle = () => {
    const next = !showEmpty;
    setShowEmpty(next);
    try {
      localStorage.setItem(SHOW_EMPTY_KEY, next ? "1" : "0");
    } catch {
      /* stockage indisponible : préférence non mémorisée */
    }
  };
  const emptyCount = q.data?.columns.filter((c) => c.items.length === 0).length ?? 0;
  return (
    <>
      <PageHeader
        title="Pipeline"
        description="Features et tickets par état. Les transitions sont pilotées par le moteur et les approbations — pas de glisser-déposer."
        actions={
          emptyCount > 0 && q.data && q.data.columns.some((c) => c.items.length > 0) ? (
            <Button variant="secondary" size="sm" onClick={toggle} aria-pressed={showEmpty}>
              {showEmpty ? "Masquer les étapes vides" : `Afficher les étapes vides (${emptyCount})`}
            </Button>
          ) : undefined
        }
      />
      <QueryView
        query={q}
        isEmpty={(d) => d.columns.every((c) => c.items.length === 0)}
        empty={<EmptyState icon={<Layers className="h-5 w-5" />} title="Pipeline vide" description="Créez une feature pour démarrer." />}
        skeleton={
          <div className="flex gap-3 overflow-hidden">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-80 w-[272px] shrink-0" />
            ))}
          </div>
        }
      >
        {(d) => (
          <div className="scroll-thin -mx-4 flex gap-3 overflow-x-auto px-4 pb-3 sm:-mx-6 sm:px-6 lg:-mx-8 lg:px-8">
            {d.columns
              .filter((c) => showEmpty || c.items.length > 0)
              .map((c) => (
                <Column key={c.state} col={c} />
              ))}
          </div>
        )}
      </QueryView>
    </>
  );
}
