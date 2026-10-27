import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { GitMerge, RefreshCw, Tag as TagIcon } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { api, buildQuery, errorMessage } from "@/api/client";
import { qk, useGitGraph } from "@/api/queries";
import type { GitGraph, GitKind, GitRef, GitTag } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Select } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { useToast } from "@/components/ui/Toast";
import { EmptyState, QueryView } from "@/components/ui/States";
import { formatParisShort, shortSha } from "@/lib/format";
import { KIND_COLORS, KIND_LABELS, KIND_ORDER, layoutGraph, type Row } from "@/lib/gitGraph";

const ROW_H = 30;
const LANE_W = 16;
const PAD = 10;
const PERIODS = [7, 30, 90, 365];
const KINDS_KEY = "aos.gitgraph.kinds";

const laneX = (i: number) => PAD + i * LANE_W;

function readKinds(): GitKind[] {
  try {
    const v = JSON.parse(localStorage.getItem(KINDS_KEY) ?? "null") as unknown;
    if (Array.isArray(v)) return KIND_ORDER.filter((k) => v.includes(k));
  } catch {
    /* préférence illisible : valeur par défaut */
  }
  return [...KIND_ORDER];
}

function RowGraph({ row, width }: { row: Row; width: number }) {
  const mid = ROW_H / 2;
  const cx = laneX(row.lane);
  const path = (x1: number, y1: number, x2: number, y2: number) =>
    x1 === x2 ? `M${x1} ${y1} L${x2} ${y2}` : `M${x1} ${y1} C${x1} ${(y1 + y2) / 2} ${x2} ${(y1 + y2) / 2} ${x2} ${y2}`;
  const merge = row.commit.parents.length > 1;
  return (
    <svg width={PAD * 2 + Math.max(width - 1, 0) * LANE_W} height={ROW_H} className="shrink-0" aria-hidden>
      {row.segments.map((s, i) => {
        const color = KIND_COLORS[s.kind];
        switch (s.type) {
          case "through":
            return <path key={i} d={path(laneX(s.from), 0, laneX(s.from), ROW_H)} stroke={color} strokeWidth={2} fill="none" />;
          case "in":
            return <path key={i} d={path(laneX(s.from), 0, cx, mid)} stroke={color} strokeWidth={2} fill="none" />;
          case "out":
            return <path key={i} d={path(cx, mid, laneX(s.to), ROW_H)} stroke={color} strokeWidth={2} fill="none" />;
          default:
            return <path key={i} d={`M${cx} ${mid} L${cx} ${ROW_H}`} stroke={color} strokeWidth={2} strokeDasharray="2 3" fill="none" />;
        }
      })}
      <circle
        cx={cx}
        cy={mid}
        r={merge ? 5 : 4}
        fill={merge ? "rgb(var(--surface))" : KIND_COLORS[row.commit.kind]}
        stroke={KIND_COLORS[row.commit.kind]}
        strokeWidth={2}
      />
    </svg>
  );
}

function RefBadge({ r }: { r: GitRef }) {
  return (
    <span className="inline-flex items-center gap-1">
      <a
        href={r.url}
        target="_blank"
        rel="noreferrer noopener"
        className="rounded border px-1.5 py-0.5 font-mono text-[11px] hover:underline"
        style={{ borderColor: KIND_COLORS[r.kind], color: KIND_COLORS[r.kind] }}
      >
        {r.name}
        {r.merged && <span className="ml-1 text-fg-subtle">(mergée)</span>}
      </a>
      {r.ticket && (
        <Link to={`/tickets/${r.ticket.id}`} className="inline-flex items-center gap-1 text-[11px] text-fg-muted hover:underline">
          {r.ticket.key} <StateBadge state={r.ticket.state} />
        </Link>
      )}
      {r.pr && (
        <a href={r.pr.url} target="_blank" rel="noreferrer noopener" className="text-[11px] text-accent hover:underline">
          PR #{r.pr.number}
        </a>
      )}
    </span>
  );
}

function TagBadge({ t }: { t: GitTag }) {
  return (
    <a
      href={t.url}
      target="_blank"
      rel="noreferrer noopener"
      className="inline-flex items-center gap-1 rounded bg-hover px-1.5 py-0.5 font-mono text-[11px] text-fg hover:underline"
    >
      <TagIcon className="h-3 w-3" aria-hidden />
      {t.name}
    </a>
  );
}

function Graph({ data }: { data: GitGraph }) {
  const layout = useMemo(() => layoutGraph(data.commits), [data.commits]);
  const refsAt = useMemo(() => {
    const m = new Map<string, GitRef[]>();
    for (const r of data.refs) m.set(r.sha, [...(m.get(r.sha) ?? []), r]);
    return m;
  }, [data.refs]);
  const tagsAt = useMemo(() => {
    const m = new Map<string, GitTag[]>();
    for (const t of data.tags) m.set(t.sha, [...(m.get(t.sha) ?? []), t]);
    return m;
  }, [data.tags]);
  return (
    <div className="overflow-x-auto" data-testid="git-graph">
      <ul className="min-w-max">
        {layout.rows.map((row) => {
          const c = row.commit;
          return (
            <li key={c.sha} className="flex items-center gap-3 pr-4 hover:bg-hover/40" style={{ height: ROW_H }} data-testid="git-commit">
              <RowGraph row={row} width={layout.width} />
              <div className="flex min-w-0 items-center gap-2 text-sm">
                {(refsAt.get(c.sha) ?? []).map((r) => (
                  <RefBadge key={r.name} r={r} />
                ))}
                {(tagsAt.get(c.sha) ?? []).map((t) => (
                  <TagBadge key={t.name} t={t} />
                ))}
                {c.parents.length > 1 && <GitMerge className="h-3.5 w-3.5 shrink-0 text-fg-subtle" aria-label="commit de merge" />}
                <span className="max-w-[48ch] truncate text-fg" title={c.subject}>
                  {c.subject}
                </span>
                {c.pr && (
                  <a href={c.pr.url} target="_blank" rel="noreferrer noopener" className="shrink-0 text-xs text-accent hover:underline">
                    #{c.pr.number}
                  </a>
                )}
              </div>
              <div className="ml-auto flex shrink-0 items-center gap-3 pl-4 text-xs text-fg-subtle">
                <span className="hidden md:inline">{c.author}</span>
                <span>{formatParisShort(c.date)}</span>
                <a href={c.url} target="_blank" rel="noreferrer noopener" className="font-mono hover:underline" title={c.sha}>
                  {shortSha(c.sha)}
                </a>
              </div>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

export default function ProjectGitGraphPage() {
  const id = Number(useParams().id);
  const [days, setDays] = useState(30);
  const [kinds, setKinds] = useState<GitKind[]>(readKinds);
  const [merged, setMerged] = useState(false);
  const q = useGitGraph(id, days, kinds, merged);
  const qc = useQueryClient();
  const [refreshing, setRefreshing] = useState(false);
  const toast = useToast();

  const toggleKind = (k: GitKind) => {
    const next = kinds.includes(k) ? kinds.filter((x) => x !== k) : KIND_ORDER.filter((x) => x === k || kinds.includes(x));
    setKinds(next);
    try {
      localStorage.setItem(KINDS_KEY, JSON.stringify(next));
    } catch {
      /* stockage indisponible */
    }
  };

  const refresh = async () => {
    setRefreshing(true);
    try {
      const data = await api.get<GitGraph>(
        `/api/projects/${id}/git-graph${buildQuery({ days, kinds: kinds.join(","), merged: merged ? 1 : undefined, refresh: 1 })}`,
      );
      qc.setQueryData(qk.gitGraph(id, days, kinds.join(","), merged), data);
    } catch (e) {
      toast.error("Actualisation impossible", errorMessage(e));
    } finally {
      setRefreshing(false);
    }
  };

  const name = q.data?.project.name ?? `Projet #${id}`;
  return (
    <>
      <PageHeader
        title={`Graphe Git — ${name}`}
        description="Branches actives, PR, merges et tags du dépôt (lecture seule). Les couleurs suivent les types de branches GitFlow."
        breadcrumbs={[{ label: "Projets", to: "/projects" }]}
        actions={
          <Button size="sm" variant="secondary" onClick={refresh} loading={refreshing} icon={<RefreshCw className="h-3.5 w-3.5" aria-hidden />}>
            Actualiser
          </Button>
        }
      />
      <Card className="mb-4 flex flex-wrap items-center gap-x-5 gap-y-3 p-3">
        <label className="flex items-center gap-2 text-sm text-fg-muted">
          Période
          <Select aria-label="Période" value={days} onChange={(e) => setDays(Number(e.target.value))} className="w-auto">
            {PERIODS.map((p) => (
              <option key={p} value={p}>
                {p === 365 ? "1 an" : `${p} derniers jours`}
              </option>
            ))}
          </Select>
        </label>
        <fieldset className="flex flex-wrap items-center gap-3">
          <legend className="sr-only">Types de branches</legend>
          {KIND_ORDER.map((k) => (
            <label key={k} className="flex cursor-pointer items-center gap-1.5 text-sm text-fg">
              <input type="checkbox" checked={kinds.includes(k)} onChange={() => toggleKind(k)} />
              <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ background: KIND_COLORS[k] }} aria-hidden />
              {KIND_LABELS[k]}
            </label>
          ))}
        </fieldset>
        <label className="flex cursor-pointer items-center gap-1.5 text-sm text-fg">
          <input type="checkbox" checked={merged} onChange={(e) => setMerged(e.target.checked)} />
          Branches déjà mergées
          {!merged && q.data && q.data.hidden_merged > 0 && <span className="text-xs text-fg-subtle">({q.data.hidden_merged} masquées)</span>}
        </label>
      </Card>
      {kinds.length === 0 ? (
        <Card>
          <EmptyState title="Aucun type de branche sélectionné" description="Cochez au moins un type pour afficher le graphe." />
        </Card>
      ) : (
        <Card className="py-2">
          <QueryView
            query={q}
            isEmpty={(d) => d.commits.length === 0}
            empty={<EmptyState title="Aucune branche active" description="Aucun commit sur la période pour les types choisis." />}
          >
            {(d) => (
              <>
                {d.truncated && (
                  <p className="px-4 pb-2 text-xs text-warning" role="status">
                    Graphe tronqué aux {d.commits.length} commits les plus récents : réduisez la période ou les types.
                  </p>
                )}
                <Graph data={d} />
              </>
            )}
          </QueryView>
        </Card>
      )}
    </>
  );
}
