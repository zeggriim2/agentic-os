import { useSearchParams } from "react-router-dom";
import { Activity } from "lucide-react";
import { useProjects, useRuns } from "@/api/queries";
import { AGENT_ROLES, RUN_STATUSES } from "@/api/types";
import { RunsTable } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Field, Select } from "@/components/ui/primitives";
import { DegradedBanner, EmptyState, ErrorState, ListSkeleton } from "@/components/ui/States";
import { roleLabel, stateMeta } from "@/lib/states";

export default function RunsPage() {
  const [sp, setSp] = useSearchParams();
  const status = sp.get("status") ?? "";
  const role = sp.get("role") ?? "";
  const projectId = sp.get("project_id") ?? "";
  const projects = useProjects();
  const q = useRuns({ status, role, project_id: projectId });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];
  const setFilter = (k: string, v: string) => {
    const n = new URLSearchParams(sp);
    if (v) n.set(k, v);
    else n.delete(k);
    setSp(n, { replace: true });
  };
  const filtered = Boolean(status || role || projectId);

  return (
    <>
      <PageHeader title="Runs" description="Exécutions des agents Hermes, en temps réel." />
      <div className="mb-3 flex flex-wrap items-end gap-3">
        <Field label="Statut" className="w-44">
          {(p) => (
            <Select {...p} value={status} onChange={(e) => setFilter("status", e.target.value)}>
              <option value="">Tous</option>
              {RUN_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {stateMeta(s).label}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Rôle" className="w-44">
          {(p) => (
            <Select {...p} value={role} onChange={(e) => setFilter("role", e.target.value)}>
              <option value="">Tous</option>
              {AGENT_ROLES.map((r) => (
                <option key={r} value={r}>
                  {roleLabel(r)}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="Projet" className="w-44">
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
        {filtered && (
          <Button variant="ghost" size="sm" className="mb-1" onClick={() => setSp(new URLSearchParams(), { replace: true })}>
            Réinitialiser
          </Button>
        )}
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
              <EmptyState icon={<Activity className="h-5 w-5" />} title={filtered ? "Aucun run ne correspond aux filtres" : "Aucun run pour le moment"} />
            ) : (
              <RunsTable runs={items} />
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
    </>
  );
}
