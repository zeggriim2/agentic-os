import { useSearchParams } from "react-router-dom";
import { CheckSquare } from "lucide-react";
import { useApprovals } from "@/api/queries";
import { ApprovalList } from "@/components/domain";
import { PageHeader } from "@/components/ui/PageHeader";
import { Card } from "@/components/ui/primitives";
import { EmptyState, QueryView } from "@/components/ui/States";
import { cn } from "@/lib/cn";
import { stateMeta } from "@/lib/states";

export default function ApprovalsPage() {
  const [sp, setSp] = useSearchParams();
  const status = sp.get("status") ?? "pending";
  const q = useApprovals({ status: status === "all" ? "" : status });
  const filters = ["pending", "approved", "executed", "execution_failed", "rejected", "all"] as const;

  return (
    <>
      <PageHeader title="Approbations" description="Chaque approbation autorise un effet précis. Décision, puis exécution, auditées séparément." />
      <div role="group" aria-label="Filtrer par statut" className="mb-3 flex flex-wrap gap-1.5">
        {filters.map((f) => (
          <button
            key={f}
            type="button"
            aria-pressed={status === f}
            onClick={() => setSp(f === "pending" ? {} : { status: f }, { replace: true })}
            className={cn(
              "focus-ring h-7 rounded-full border px-3 text-xs font-medium transition-colors",
              status === f ? "border-accent/50 bg-accent/10 text-fg" : "border-border text-fg-muted hover:bg-hover hover:text-fg",
            )}
          >
            {f === "all" ? "Toutes" : stateMeta(f).label}
          </button>
        ))}
      </div>
      <Card>
        <QueryView
          query={q}
          isEmpty={(d) => d.length === 0}
          empty={
            <EmptyState
              icon={<CheckSquare className="h-5 w-5" />}
              title={status === "pending" ? "Aucune décision en attente" : "Aucune approbation"}
              description={status === "pending" ? "Vous serez notifié (Telegram + temps réel) dès qu'une décision est requise." : undefined}
            />
          }
        >
          {(d) => <ApprovalList approvals={d} />}
        </QueryView>
      </Card>
    </>
  );
}
