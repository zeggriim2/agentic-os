export type Tone = "neutral" | "info" | "progress" | "review" | "success" | "danger" | "warning";

interface StateMeta {
  label: string;
  tone: Tone;
}

/**
 * Palette unique des états, utilisée partout (badges, pipeline, filtres).
 * neutral = en attente/inactif, progress = agent au travail, review = décision humaine attendue,
 * info = étape technique/externe, success = terminé OK, danger = échec/bloqué, warning = dégradé.
 */
export const STATE_META: Record<string, StateMeta> = {
  // Feature
  IDEA: { label: "Idée", tone: "neutral" },
  SPEC: { label: "Spécification", tone: "progress" },
  SPEC_REVIEW: { label: "Revue spec", tone: "review" },
  NEEDS_CLARIFICATION: { label: "Clarification", tone: "review" },
  TECH_DESIGN: { label: "Design technique", tone: "progress" },
  TECH_DESIGN_REVIEW: { label: "Revue design", tone: "review" },
  ISSUES_OPENED: { label: "Issues ouvertes", tone: "info" },
  HUMAN_GATE: { label: "Feu vert humain", tone: "review" },
  READY_FOR_DEV: { label: "Prêt pour dev", tone: "info" },
  IN_DEV: { label: "En développement", tone: "progress" },
  DONE: { label: "Terminé", tone: "success" },
  BLOCKED: { label: "Bloqué", tone: "danger" },
  CANCELLED: { label: "Annulé", tone: "neutral" },
  // Ticket
  DEV: { label: "Développement", tone: "progress" },
  REVIEW: { label: "Revue de code", tone: "progress" },
  QA: { label: "QA", tone: "progress" },
  CHANGES_REQUESTED: { label: "Changements demandés", tone: "review" },
  AWAITING_PR_APPROVAL: { label: "PR à approuver", tone: "review" },
  PR_OPENED: { label: "PR ouverte", tone: "info" },
  HUMAN_MERGE: { label: "Merge humain", tone: "review" },
  FAILED: { label: "Échec", tone: "danger" },
  // Runs
  submitted: { label: "Soumis", tone: "neutral" },
  running: { label: "En cours", tone: "progress" },
  completed: { label: "Terminé", tone: "success" },
  failed: { label: "Échec", tone: "danger" },
  stopping: { label: "Arrêt en cours", tone: "warning" },
  stopped: { label: "Arrêté", tone: "warning" },
  lost: { label: "Perdu", tone: "danger" },
  // Jobs
  queued: { label: "En file", tone: "neutral" },
  leased: { label: "Pris en charge", tone: "progress" },
  succeeded: { label: "Réussi", tone: "success" },
  cancelled: { label: "Annulé", tone: "neutral" },
  // Approbations
  pending: { label: "En attente", tone: "review" },
  approved: { label: "Approuvée", tone: "info" },
  rejected: { label: "Rejetée", tone: "neutral" },
  executed: { label: "Exécutée", tone: "success" },
  execution_failed: { label: "Échec d'exécution", tone: "danger" },
  expired: { label: "Expirée", tone: "neutral" },
  // CI
  unverified: { label: "Non vérifiée", tone: "neutral" },
  success: { label: "CI verte", tone: "success" },
  failure: { label: "CI rouge", tone: "danger" },
  // Verdicts
  changes_requested: { label: "Changements demandés", tone: "review" },
  passed: { label: "Validé", tone: "success" },
  // Synchronisation agents
  SYNCED: { label: "Synchronisé", tone: "success" },
  DRIFT_DETECTED: { label: "Dérive", tone: "warning" },
  UNKNOWN: { label: "Inconnu", tone: "neutral" },
  UNREACHABLE: { label: "Injoignable", tone: "danger" },
  // Santé
  ok: { label: "OK", tone: "success" },
  up: { label: "OK", tone: "success" },
  healthy: { label: "OK", tone: "success" },
  degraded: { label: "Dégradé", tone: "warning" },
  down: { label: "Hors service", tone: "danger" },
  error: { label: "Erreur", tone: "danger" },
  unreachable: { label: "Injoignable", tone: "danger" },
  disabled: { label: "Désactivé", tone: "neutral" },
  not_configured: { label: "Non configuré", tone: "neutral" },
  unknown: { label: "Inconnu", tone: "neutral" },
};

export function stateMeta(state: string | null | undefined): StateMeta {
  if (!state) return { label: "—", tone: "neutral" };
  return STATE_META[state] ?? { label: state, tone: "neutral" };
}

export const TONE_CLASSES: Record<Tone, { badge: string; dot: string; text: string; border: string }> = {
  neutral: { badge: "bg-neutral/10 text-neutral border-neutral/25", dot: "bg-neutral", text: "text-neutral", border: "border-neutral/40" },
  info: { badge: "bg-info/10 text-info border-info/25", dot: "bg-info", text: "text-info", border: "border-info/40" },
  progress: { badge: "bg-progress/10 text-progress border-progress/25", dot: "bg-progress", text: "text-progress", border: "border-progress/40" },
  review: { badge: "bg-review/10 text-review border-review/25", dot: "bg-review", text: "text-review", border: "border-review/40" },
  success: { badge: "bg-success/10 text-success border-success/25", dot: "bg-success", text: "text-success", border: "border-success/40" },
  danger: { badge: "bg-danger/10 text-danger border-danger/25", dot: "bg-danger", text: "text-danger", border: "border-danger/40" },
  warning: { badge: "bg-warning/10 text-warning border-warning/25", dot: "bg-warning", text: "text-warning", border: "border-warning/40" },
};

export const ROLE_LABELS: Record<string, string> = {
  po: "Product Owner",
  tech_lead: "Tech Lead",
  developer: "Développeur",
  reviewer: "Reviewer",
  qa: "QA",
};
export const roleLabel = (r: string | null | undefined) => (r ? ROLE_LABELS[r] ?? r : "—");

export const APPROVAL_KIND_LABELS: Record<string, string> = {
  spec_review: "Revue de spécification",
  design_review: "Revue de design",
  dev_gate: "Mise en développement",
  rework: "Reprise (rework)",
  publish_pr: "Publication de PR",
};
export const approvalKindLabel = (k: string) => APPROVAL_KIND_LABELS[k] ?? k;

export const FEATURE_TERMINAL = new Set(["DONE", "CANCELLED"]);
export const TICKET_TERMINAL = new Set(["DONE", "CANCELLED"]);
export const RUN_ACTIVE = new Set(["submitted", "running", "stopping"]);
