import { useEffect, useState, type FormEvent } from "react";
import { Bot, Pencil, RefreshCw } from "lucide-react";
import { errorMessage } from "@/api/client";
import { useAgents, usePatchAgent, useSyncAgents } from "@/api/queries";
import type { Agent } from "@/api/types";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog, Dialog } from "@/components/ui/Dialog";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card, Field, Input, Mono } from "@/components/ui/primitives";
import { StateBadge } from "@/components/ui/StateBadge";
import { Switch } from "@/components/ui/Switch";
import { EmptyState, QueryView } from "@/components/ui/States";
import { Time } from "@/components/ui/Time";
import { useToast } from "@/components/ui/Toast";
import { cn } from "@/lib/cn";
import { formatNumber } from "@/lib/format";
import { roleLabel } from "@/lib/states";

function EditAgentDialog({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const [model, setModel] = useState("");
  const [budget, setBudget] = useState("");
  const [touched, setTouched] = useState(false);
  const m = usePatchAgent();
  const toast = useToast();
  useEffect(() => {
    if (agent) {
      setModel(agent.desired_model ?? "");
      setBudget(String(agent.max_tokens_per_run));
      setTouched(false);
    }
  }, [agent]);
  const n = Number(budget);
  const budgetErr = !/^\d+$/.test(budget) || n <= 0 ? "Entier strictement positif." : undefined;
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setTouched(true);
    if (!agent || budgetErr) return;
    m.mutate(
      { role: agent.role, patch: { desired_model: model.trim(), max_tokens_per_run: n } },
      {
        onSuccess: () => {
          toast.success("Agent mis à jour");
          onClose();
        },
      },
    );
  };
  return (
    <Dialog
      open={Boolean(agent)}
      onClose={onClose}
      title={agent ? `Modifier ${roleLabel(agent.role)}` : ""}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Annuler
          </Button>
          <Button variant="primary" type="submit" form="edit-agent" loading={m.isPending}>
            Enregistrer
          </Button>
        </>
      }
    >
      <form id="edit-agent" onSubmit={submit} noValidate className="space-y-4">
        {m.isError && (
          <p role="alert" className="text-sm text-danger">
            {errorMessage(m.error)}
          </p>
        )}
        <Field label="Modèle souhaité" hint="Comparé au modèle observé lors des runs pour détecter une dérive.">
          {(p) => <Input {...p} value={model} onChange={(e) => setModel(e.target.value)} className="font-mono" data-autofocus />}
        </Field>
        <Field label="Budget max de tokens par run" required error={touched ? budgetErr : undefined} hint="Au-delà, le run est rejeté (erreur budget).">
          {(p) => <Input {...p} inputMode="numeric" value={budget} onChange={(e) => setBudget(e.target.value)} />}
        </Field>
      </form>
    </Dialog>
  );
}

function AgentCard({ a, onEdit }: { a: Agent; onEdit: () => void }) {
  const patch = usePatchAgent();
  const toast = useToast();
  const drift = a.sync_state === "DRIFT_DETECTED" || (a.desired_model && a.observed_model && a.desired_model !== a.observed_model);
  return (
    <Card className={cn("flex flex-col", !a.enabled && "opacity-75")}>
      <div className="flex items-start justify-between gap-3 border-b border-border p-4">
        <div className="flex min-w-0 items-center gap-3">
          <span className="rounded-md border border-border bg-panel p-2 text-fg-muted" aria-hidden>
            <Bot className="h-4 w-4" />
          </span>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-fg">{roleLabel(a.role)}</h2>
            <p className="truncate font-mono text-xs text-fg-subtle">profil {a.hermes_profile}</p>
          </div>
        </div>
        <Switch
          checked={a.enabled}
          label={`${a.enabled ? "Désactiver" : "Activer"} l'agent ${roleLabel(a.role)}`}
          disabled={patch.isPending}
          onChange={(v) =>
            patch.mutate(
              { role: a.role, patch: { enabled: v } },
              {
                onSuccess: () => toast.success(v ? "Agent activé" : "Agent désactivé"),
                onError: (e) => toast.error("Modification refusée", errorMessage(e)),
              },
            )
          }
        />
      </div>
      <dl className="grid flex-1 grid-cols-2 gap-x-4 gap-y-3 p-4 text-sm">
        <div>
          <dt className="text-xs text-fg-subtle">Modèle souhaité</dt>
          <dd className="mt-0.5 truncate font-mono text-xs text-fg">{a.desired_model || "—"}</dd>
        </div>
        <div>
          <dt className="text-xs text-fg-subtle">Modèle observé</dt>
          <dd className={cn("mt-0.5 truncate font-mono text-xs", drift ? "text-warning" : "text-fg")}>
            {a.observed_model || "—"}
            {a.observed_provider && <span className="text-fg-subtle"> · {a.observed_provider}</span>}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-fg-subtle">Synchronisation</dt>
          <dd className="mt-0.5">
            <StateBadge state={a.sync_state} />
          </dd>
        </div>
        <div>
          <dt className="text-xs text-fg-subtle">Budget / run</dt>
          <dd className="mt-0.5 tabular-nums text-fg">{formatNumber(a.max_tokens_per_run)} tokens</dd>
        </div>
        <div>
          <dt className="text-xs text-fg-subtle">Observé</dt>
          <dd className="mt-0.5 text-xs text-fg-muted">
            <Time value={a.observed_at} />
          </dd>
        </div>
        <div>
          <dt className="text-xs text-fg-subtle">Instructions</dt>
          <dd className="mt-0.5 text-xs text-fg-muted">
            <Mono>rev {a.instructions_revision}</Mono>
          </dd>
        </div>
      </dl>
      <div className="flex justify-end border-t border-border px-4 py-2.5">
        <Button size="sm" variant="ghost" onClick={onEdit} icon={<Pencil className="h-3.5 w-3.5" aria-hidden />}>
          Modifier
        </Button>
      </div>
    </Card>
  );
}

export default function AgentsPage() {
  const q = useAgents();
  const sync = useSyncAgents();
  const toast = useToast();
  const [editing, setEditing] = useState<Agent | null>(null);
  const [confirmSync, setConfirmSync] = useState(false);
  const drift = q.data?.filter((a) => a.sync_state === "DRIFT_DETECTED").length ?? 0;
  return (
    <>
      <PageHeader
        title="Agents"
        description="Un agent par rôle, lié à un profil Hermes."
        meta={drift > 0 ? <StateBadge state="DRIFT_DETECTED" label={`${drift} agent(s) en dérive`} /> : undefined}
        actions={
          <Button onClick={() => setConfirmSync(true)} icon={<RefreshCw className="h-4 w-4" aria-hidden />}>
            Synchroniser
          </Button>
        }
      />
      <QueryView query={q} isEmpty={(d) => d.length === 0} empty={<Card><EmptyState icon={<Bot className="h-5 w-5" />} title="Aucun agent configuré" /></Card>}>
        {(d) => (
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {d.map((a) => (
              <AgentCard key={a.role} a={a} onEdit={() => setEditing(a)} />
            ))}
          </div>
        )}
      </QueryView>
      <EditAgentDialog agent={editing} onClose={() => setEditing(null)} />
      <ConfirmDialog
        open={confirmSync}
        onClose={() => setConfirmSync(false)}
        onConfirm={() =>
          sync.mutate(undefined, {
            onSuccess: () => {
              setConfirmSync(false);
              toast.success("Synchronisation demandée", "Un job sync_agents a été créé.");
            },
            onError: (e) => toast.error("Synchronisation impossible", errorMessage(e)),
          })
        }
        loading={sync.isPending}
        title="Synchroniser les agents ?"
        description="Crée un job sync_agents qui interroge chaque profil Hermes pour relever le modèle observé."
        confirmLabel="Synchroniser"
      />
    </>
  );
}
