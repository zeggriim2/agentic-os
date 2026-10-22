import { Bot, Database, GitBranch, MessageCircle, Server, Cpu } from "lucide-react";
import type { ReactNode } from "react";
import { useHealth } from "@/api/queries";
import type { HealthComponent } from "@/api/types";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, CardHeader, DefList, Mono, Skeleton } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { EmptyState, QueryView } from "@/components/ui/States";
import { formatParisTime } from "@/lib/format";

/** Normalise un composant de santé (chaîne, booléen ou objet) en {status, message}. */
export function normalizeHealth(c: HealthComponent | number | undefined): { status: string; message?: string } {
  if (c === undefined || c === null) return { status: "unknown" };
  if (typeof c === "boolean") return { status: c ? "ok" : "error" };
  if (typeof c === "number") return { status: "ok", message: String(c) };
  if (typeof c === "string") return { status: c };
  const status = typeof c.status === "string" ? c.status : c.ok === true ? "ok" : c.ok === false ? "error" : "unknown";
  const message = typeof c.message === "string" ? c.message : typeof c.error === "string" ? c.error : undefined;
  return { status, message };
}

function Component({ name, icon, value }: { name: string; icon: ReactNode; value: HealthComponent | undefined }) {
  const h = normalizeHealth(value);
  return (
    <Card className="p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <span className="rounded-md border border-border bg-panel p-1.5 text-fg-subtle" aria-hidden>
            {icon}
          </span>
          <h2 className="text-sm font-semibold text-fg">{name}</h2>
        </div>
        <StateBadge state={h.status} />
      </div>
      {h.message && <p className="mt-2 break-words text-xs text-fg-muted">{h.message}</p>}
    </Card>
  );
}

export default function HealthPage() {
  const q = useHealth();
  return (
    <>
      <PageHeader
        title="Santé"
        description="État des dépendances. Actualisé toutes les 30 s."
        meta={q.dataUpdatedAt ? <span>Dernière lecture : {formatParisTime(new Date(q.dataUpdatedAt))}</span> : undefined}
      />
      <QueryView
        query={q}
        skeleton={
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        }
      >
        {(h) => {
          const workers = normalizeHealth(h.workers as HealthComponent);
          return (
            <div className="space-y-4">
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                <Component name="Base de données" icon={<Database className="h-4 w-4" />} value={h.database} />
                <Component name="Hermes" icon={<Bot className="h-4 w-4" />} value={h.hermes} />
                <Component name="GitHub" icon={<GitBranch className="h-4 w-4" />} value={h.github} />
                <Component name="Telegram" icon={<MessageCircle className="h-4 w-4" />} value={h.telegram} />
              </div>
              <div className="grid gap-4 lg:grid-cols-2">
                <Card>
                  <CardHeader title="Profils Hermes" icon={<Bot className="h-4 w-4 text-fg-subtle" aria-hidden />} />
                  {h.profiles?.length ? (
                    <ul className="divide-y divide-border">
                      {h.profiles.map((p) => (
                        <li key={p.profile} className="flex items-center justify-between px-4 py-2.5">
                          <Mono>{p.profile}</Mono>
                          <StateBadge state={p.status} />
                        </li>
                      ))}
                    </ul>
                  ) : (
                    <EmptyState title="Aucun profil" className="py-6" />
                  )}
                </Card>
                <Card>
                  <CardHeader title="Service" icon={<Server className="h-4 w-4 text-fg-subtle" aria-hidden />} />
                  <div className="p-4">
                    <DefList
                      items={[
                        [
                          <span key="w" className="inline-flex items-center gap-1.5">
                            <Cpu className="h-3.5 w-3.5" aria-hidden /> Workers
                          </span>,
                          typeof h.workers === "number" ? String(h.workers) : <StateBadge state={workers.status} label={workers.message} />,
                        ],
                        ["Version", <Mono key="v">{h.version || "—"}</Mono>],
                        ["Révision", <Mono key="r">{h.revision ? h.revision.slice(0, 12) : "—"}</Mono>],
                        ["Schéma", <Mono key="s">v{String(h.schema_version)}</Mono>],
                      ]}
                    />
                  </div>
                </Card>
              </div>
            </div>
          );
        }}
      </QueryView>
    </>
  );
}
