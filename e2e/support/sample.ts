import type { Api } from "./env";

export interface Sample {
  featureId: number;
  runId: number;
  approvalId: number;
  ticketId: number;
}

const TITLE = "Exemple d'affichage";

/**
 * Feature d'exemple (projet « divers ») menée jusqu'au feu vert développement : fournit un détail
 * de feature, un run terminé avec événements, une approbation en attente et un ticket.
 * Réutilisée si elle existe déjà (fichiers de test exécutés en série sur le même serveur).
 */
export async function ensureSample(api: Api): Promise<Sample> {
  const projectId = await api.ensureProject("divers", "o/divers");
  const list = await api.get<{ items: { id: number; title: string }[] }>(`/api/features?project_id=${projectId}&limit=200`);
  let featureId = list.items.find((f) => f.title === TITLE)?.id;
  if (!featureId) {
    featureId = await api.createFeature(projectId, TITLE);
    await api.featureAction(featureId, "start_spec");
  }
  // Approuve spec puis design jusqu'à l'approbation de feu vert (laissée en attente).
  let f: any;
  let gate: any;
  const deadline = Date.now() + 120_000;
  for (;;) {
    f = await api.feature(featureId);
    const pending = f.approvals.filter((a: any) => a.status === "pending");
    gate = pending.find((a: any) => a.kind === "dev_gate");
    if (gate && f.tickets.length > 0) break;
    for (const a of pending) if (a.kind === "spec_review" || a.kind === "design_review") await api.decide(a.id, "approved");
    if (Date.now() > deadline) throw new Error(`exemple bloqué à l'état ${f.feature.state}`);
    await new Promise((r) => setTimeout(r, 1_000));
  }
  return { featureId, runId: f.runs[0].id, approvalId: gate.id, ticketId: f.tickets[0].id };
}

/** Pages principales vérifiées par les tests responsive et accessibilité. */
export function mainPages(s: Sample): { name: string; path: string; heading: RegExp }[] {
  return [
    { name: "vue d'ensemble", path: "/", heading: /Vue d'ensemble/ },
    { name: "projets", path: "/projects", heading: /Projets/ },
    { name: "features", path: "/features", heading: /Features/ },
    { name: "détail de feature", path: `/features/${s.featureId}`, heading: new RegExp(TITLE) },
    { name: "approbations", path: "/approvals", heading: /Approbations/ },
    { name: "détail d'approbation", path: `/approvals/${s.approvalId}`, heading: /Feu vert développement/ },
    { name: "détail de ticket", path: `/tickets/${s.ticketId}`, heading: /Ajouter le fichier de démonstration/ },
    { name: "détail de run", path: `/runs/${s.runId}`, heading: /Run #\d+/ },
    { name: "agents", path: "/agents", heading: /Agents/ },
    { name: "audit", path: "/audit", heading: /Audit/ },
  ];
}
