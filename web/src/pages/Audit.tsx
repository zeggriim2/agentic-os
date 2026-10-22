import { Fragment, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ChevronRight, ScrollText, Search } from "lucide-react";
import { useAudit } from "@/api/queries";
import { Actor, AuditDiff, SubjectLink } from "@/components/domain";
import { Button } from "@/components/ui/Button";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Field, Input, Select } from "@/components/ui/primitives";
import { Tag } from "@/components/ui/StateBadge";
import { DegradedBanner, EmptyState, ErrorState, ListSkeleton } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { cn } from "@/lib/cn";
import { shortSha } from "@/lib/format";

const SUBJECT_TYPES = ["feature", "ticket", "approval", "project", "agent", "run", "job"];

export default function AuditPage() {
  const [sp, setSp] = useSearchParams();
  const actor = sp.get("actor") ?? "";
  const subjectType = sp.get("subject_type") ?? "";
  const subjectId = sp.get("subject_id") ?? "";
  const [draft, setDraft] = useState({ actor, subjectType, subjectId });
  const [open, setOpen] = useState<number | null>(null);
  const q = useAudit({ actor, subject_type: subjectType, subject_id: subjectId });
  const items = q.data?.pages.flatMap((p) => p.items) ?? [];

  const apply = (e: FormEvent) => {
    e.preventDefault();
    const n = new URLSearchParams();
    if (draft.actor.trim()) n.set("actor", draft.actor.trim());
    if (draft.subjectType) n.set("subject_type", draft.subjectType);
    if (draft.subjectId.trim()) n.set("subject_id", draft.subjectId.trim());
    setSp(n, { replace: true });
  };
  const filtered = Boolean(actor || subjectType || subjectId);

  return (
    <>
      <PageHeader title="Audit" description="Journal append-only de toutes les transitions et décisions." />
      <form onSubmit={apply} className="mb-3 flex flex-wrap items-end gap-3" aria-label="Filtres d'audit">
        <Field label="Acteur" className="w-56">
          {(p) => <Input {...p} value={draft.actor} onChange={(e) => setDraft({ ...draft, actor: e.target.value })} placeholder="user:admin, system, agent:qa" />}
        </Field>
        <Field label="Type de sujet" className="w-40">
          {(p) => (
            <Select {...p} value={draft.subjectType} onChange={(e) => setDraft({ ...draft, subjectType: e.target.value })}>
              <option value="">Tous</option>
              {SUBJECT_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </Select>
          )}
        </Field>
        <Field label="ID du sujet" className="w-28">
          {(p) => <Input {...p} inputMode="numeric" value={draft.subjectId} onChange={(e) => setDraft({ ...draft, subjectId: e.target.value.replace(/\D/g, "") })} />}
        </Field>
        <Button type="submit" icon={<Search className="h-4 w-4" aria-hidden />}>
          Filtrer
        </Button>
        {filtered && (
          <Button
            variant="ghost"
            onClick={() => {
              setDraft({ actor: "", subjectType: "", subjectId: "" });
              setSp(new URLSearchParams(), { replace: true });
            }}
          >
            Réinitialiser
          </Button>
        )}
      </form>
      <Card>
        {q.isPending ? (
          <ListSkeleton rows={8} />
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
              <EmptyState icon={<ScrollText className="h-5 w-5" />} title={filtered ? "Aucune entrée pour ces filtres" : "Journal vide"} />
            ) : (
              <div className="scroll-thin overflow-x-auto">
                <table className="w-full min-w-[820px] text-sm">
                  <thead>
                    <tr className="border-b border-border text-left text-xs text-fg-subtle">
                      <th className="w-8 px-3 py-2">
                        <span className="sr-only">Déplier</span>
                      </th>
                      <th className="px-3 py-2 font-medium">Date</th>
                      <th className="px-3 py-2 font-medium">Acteur</th>
                      <th className="px-3 py-2 font-medium">Action</th>
                      <th className="px-3 py-2 font-medium">Sujet</th>
                      <th className="px-3 py-2 font-medium">Run / SHA</th>
                    </tr>
                  </thead>
                  <tbody>
                    {items.map((e) => {
                      const isOpen = open === e.id;
                      return (
                        <Fragment key={e.id}>
                          <tr className={cn("border-b border-border/60 hover:bg-hover/40", isOpen && "bg-hover/40")}>
                            <td className="px-3 py-2">
                              <button
                                type="button"
                                onClick={() => setOpen(isOpen ? null : e.id)}
                                aria-expanded={isOpen}
                                aria-label={isOpen ? "Masquer le diff" : "Afficher le diff avant/après"}
                                className="focus-ring rounded p-0.5 text-fg-subtle hover:text-fg"
                              >
                                <ChevronRight className={cn("h-4 w-4 transition-transform", isOpen && "rotate-90")} aria-hidden />
                              </button>
                            </td>
                            <td className="whitespace-nowrap px-3 py-2 text-xs text-fg-muted">
                              <Time value={e.at} />
                            </td>
                            <td className="px-3 py-2">
                              <Actor actor={e.actor} />
                            </td>
                            <td className="px-3 py-2 font-mono text-xs text-fg">{e.action}</td>
                            <td className="px-3 py-2">
                              <SubjectLink type={e.subject_type} id={e.subject_id} />
                            </td>
                            <td className="px-3 py-2 text-xs">
                              {e.run_id && (
                                <Link to={`/runs/${e.run_id}`} className="focus-ring mr-2 rounded text-accent hover:underline">
                                  #{e.run_id}
                                </Link>
                              )}
                              {e.sha && <Tag className="font-mono">{shortSha(e.sha)}</Tag>}
                            </td>
                          </tr>
                          {isOpen && (
                            <tr className="border-b border-border/60 bg-panel/50">
                              <td colSpan={6} className="px-4 py-3">
                                <AuditDiff entry={e} />
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      );
                    })}
                  </tbody>
                </table>
              </div>
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
