import { useState } from "react";
import { AlertTriangle, Cpu } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useHermesModels, useProfileModels, useRequestProfileModel } from "@/api/queries";
import type { ModelRef, ProfileMismatch, ProfileModel } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/Dialog";
import { Card, CardHeader, Mono } from "@/components/ui/primitives";
import { ToneBadge } from "@/components/ui/StateBadge";
import { QueryView } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { roleLabel } from "@/lib/states";
import { ModelSelect } from "./Projects";

const fmt = (m: ModelRef | null | undefined) => (m ? `${m.provider}/${m.model}` : "—");

function StateCell({ p }: { p: ProfileModel }) {
  if (!p.state) return <span className="text-xs text-fg-subtle">—</span>;
  if (p.state === "PENDING") return <ToneBadge tone="progress">En attente d'application</ToneBadge>;
  if (p.state === "FAILED")
    return (
      <ToneBadge tone="danger" title={p.error ?? undefined}>
        Échec
      </ToneBadge>
    );
  return p.matches_desired ? (
    <ToneBadge tone="success">Appliqué</ToneBadge>
  ) : (
    <ToneBadge tone="warning" title="Le modèle lu dans Hermes ne correspond plus à la demande (modifié hors d'Agentic OS ?)">
      Appliqué, puis modifié
    </ToneBadge>
  );
}

function usageText(p: ProfileModel) {
  if (p.usages.length === 0) return "non utilisé";
  return p.usages.map((u) => `${u.project || "par défaut"} · ${roleLabel(u.role)}`).join(", ");
}

/** Modèle par défaut des profils Hermes aos-* : choisi ici, appliqué sur l'hôte par le timer aos-profile-sync. */
export function ProfileModelsSection() {
  const q = useProfileModels();
  const models = useHermesModels();
  const req = useRequestProfileModel();
  const toast = useToast();
  const [choice, setChoice] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState<{ profile: string; to: ModelRef; from: ModelRef | null } | null>(null);
  const ask = (profile: string, to: ModelRef, from: ModelRef | null) => setConfirm({ profile, to, from });
  return (
    <Card className="mt-6">
      <CardHeader
        icon={<Cpu className="h-4 w-4" aria-hidden />}
        title="Modèle par défaut des profils Hermes"
        description="Hermes résout toujours le modèle par défaut du profil avant le modèle demandé par le projet : s'il est indisponible (quota), le run échoue. Choisissez-le ici ; le timer aos-profile-sync l'applique sur l'hôte en moins d'une minute."
      />
      <QueryView query={q}>
        {(d) => (
          <div className="space-y-3 p-4">
            {d.sync.stale && (
              <div role="alert" className="flex items-start gap-2 rounded-md border border-warning/30 bg-warning/10 px-3 py-2 text-xs text-fg">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />
                <span>
                  {d.sync.last_seen_at ? (
                    <>
                      Le script de synchronisation ne tourne plus (dernier passage <Time value={d.sync.last_seen_at} />)
                    </>
                  ) : (
                    "Le script de synchronisation n'a jamais tourné"
                  )}{" "}
                  : les demandes resteront en attente. Sur l'hôte Hermes : <Mono>deploy/agents/install-profile-sync.sh</Mono>
                </span>
              </div>
            )}
            {d.mismatches.length > 0 && (
              <div role="alert" className="rounded-md border border-danger/30 bg-danger/10 px-3 py-2 text-xs text-fg" data-testid="profile-mismatches">
                <p className="font-medium">Fournisseur demandé différent du défaut du profil : ces runs dépendent aussi de ce fournisseur.</p>
                <ul className="mt-1 space-y-1">
                  {d.mismatches.map((m: ProfileMismatch) => (
                    <li key={`${m.project}-${m.role}`} className="flex flex-wrap items-center gap-2">
                      <span>
                        {m.project} · {roleLabel(m.role)} : demande <Mono>{fmt(m.requested)}</Mono>, profil <Mono>{m.profile}</Mono> sur{" "}
                        <Mono>{fmt(m.current)}</Mono>
                      </span>
                      <Button size="sm" variant="secondary" onClick={() => ask(m.profile, m.requested, m.current)}>
                        Aligner le profil
                      </Button>
                    </li>
                  ))}
                </ul>
              </div>
            )}
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-fg-subtle">
                  <tr>
                    <th className="py-1.5 pr-3 font-medium">Profil</th>
                    <th className="py-1.5 pr-3 font-medium">Modèle actuel</th>
                    <th className="py-1.5 pr-3 font-medium">Demande</th>
                    <th className="py-1.5 pr-3 font-medium">Nouveau modèle</th>
                  </tr>
                </thead>
                <tbody>
                  {d.items.map((p) => {
                    const v = choice[p.profile] ?? "";
                    const cut = v.indexOf("/");
                    const to: ModelRef | null = cut > 0 ? { provider: v.slice(0, cut), model: v.slice(cut + 1) } : null;
                    const same = to && p.current && to.provider === p.current.provider && to.model === p.current.model;
                    return (
                      <tr key={p.profile} className="border-t border-border align-top" data-testid={`profile-${p.profile}`}>
                        <td className="py-2 pr-3">
                          <Mono>{p.profile}</Mono>
                          <p className="text-2xs text-fg-subtle">{usageText(p)}</p>
                        </td>
                        <td className="py-2 pr-3">
                          {p.current ? (
                            <Mono>{fmt(p.current)}</Mono>
                          ) : (
                            <span className="text-xs text-danger" title={p.current_error}>
                              illisible
                            </span>
                          )}
                        </td>
                        <td className="py-2 pr-3">
                          <div className="flex flex-col gap-1">
                            {p.desired && <Mono className="text-xs">{fmt(p.desired)}</Mono>}
                            <StateCell p={p} />
                            {p.state === "FAILED" && p.error && <p className="max-w-xs text-2xs text-danger">{p.error}</p>}
                          </div>
                        </td>
                        <td className="py-2 pr-3">
                          <div className="flex items-center gap-2">
                            <ModelSelect
                              aria-label={`Nouveau modèle pour ${p.profile}`}
                              value={v}
                              onChange={(x) => setChoice((c) => ({ ...c, [p.profile]: x }))}
                              providers={models.data ?? []}
                              emptyLabel={models.isError ? "Catalogue Hermes indisponible" : "Choisir…"}
                            />
                            <Button size="sm" disabled={!to || !!same} onClick={() => to && ask(p.profile, to, p.current)}>
                              Appliquer
                            </Button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </QueryView>
      {confirm && (
        <ConfirmDialog
          open
          onClose={() => setConfirm(null)}
          onConfirm={() =>
            req.mutate(
              { profile: confirm.profile, ...confirm.to },
              {
                onSuccess: () => {
                  toast.success("Demande enregistrée", `${confirm.profile} → ${fmt(confirm.to)} (application sous une minute)`);
                  setChoice((c) => ({ ...c, [confirm.profile]: "" }));
                  setConfirm(null);
                },
                onError: (e) => toast.error("Demande refusée", errorMessage(e)),
              },
            )
          }
          loading={req.isPending}
          title={`Changer le modèle par défaut de ${confirm.profile} ?`}
          description={`${fmt(confirm.from)} → ${fmt(confirm.to)}. Le timer de l'hôte écrit ce modèle dans la configuration du profil Hermes (copie de sauvegarde conservée) ; il s'applique aux runs suivants, pas à ceux en cours.`}
          confirmLabel="Changer"
          tone="primary"
        />
      )}
    </Card>
  );
}
