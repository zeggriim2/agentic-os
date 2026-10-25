// Types de l'API Agentic OS — d'après docs/technique/architecture.md (§4, §5, §7, §9).
// Les champs marqués optionnels ne sont pas garantis par le contrat : l'UI les affiche s'ils sont présents.

export type ISODate = string;
export type ID = number;

// ---------- Erreurs & listes ----------
export interface ApiErrorBody {
  error: { code: string; message: string };
}

export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

// ---------- Auth ----------
export interface LoginRequest {
  login: string;
  password: string;
}
export interface LoginResponse {
  csrf_token: string;
  login?: string;
}
export interface Me {
  login: string;
  csrf_token: string;
}

// ---------- États ----------
export const FEATURE_STATES = [
  "IDEA",
  "SPEC",
  "SPEC_REVIEW",
  "TECH_DESIGN",
  "TECH_DESIGN_REVIEW",
  "ISSUES_OPENED",
  "HUMAN_GATE",
  "READY_FOR_DEV",
  "IN_DEV",
  "DONE",
  "BLOCKED",
  "CANCELLED",
] as const;
export type FeatureState = (typeof FEATURE_STATES)[number];

export const TICKET_STATES = [
  "READY_FOR_DEV",
  "DEV",
  "REVIEW",
  "QA",
  "AWAITING_PR_APPROVAL",
  "PR_OPENED",
  "HUMAN_MERGE",
  "DONE",
  "CHANGES_REQUESTED",
  "BLOCKED",
  "FAILED",
  "CANCELLED",
] as const;
export type TicketState = (typeof TICKET_STATES)[number];

export const RUN_STATUSES = ["submitted", "running", "stopping", "completed", "failed", "stopped", "lost"] as const;
export type RunStatus = (typeof RUN_STATUSES)[number];

export type JobStatus = "queued" | "leased" | "succeeded" | "failed" | "cancelled";
export type ErrorClass = "timeout" | "budget" | "policy" | "provider" | "invalid_output" | "external" | "internal";
export type CIState = "unverified" | "pending" | "success" | "failure";

export const AGENT_ROLES = ["po", "tech_lead", "developer", "reviewer", "qa"] as const;
export type AgentRole = (typeof AGENT_ROLES)[number];
export type SyncState = "SYNCED" | "DRIFT_DETECTED" | "UNKNOWN" | "UNREACHABLE";

export const APPROVAL_STATUSES = ["pending", "approved", "rejected", "executed", "execution_failed", "expired"] as const;
export type ApprovalStatus = (typeof APPROVAL_STATUSES)[number];
export type ApprovalKind = "spec_review" | "design_review" | "dev_gate" | "rework" | "publish_pr";
export type SubjectType = "feature" | "ticket" | "approval" | "project" | "agent" | "run" | string;

export type ProjectMode = "sandbox" | "product";

// ---------- Entités ----------
export interface Timestamps {
  created_at: ISODate;
  updated_at?: ISODate;
}

export interface Project extends Timestamps {
  id: ID;
  slug: string;
  name: string;
  repository: string;
  base_branch: string;
  required_checks: string[];
  mode: ProjectMode;
  enabled: boolean;
  test_command: string | null;
}
export interface ProjectCreate {
  slug: string;
  name: string;
  repository: string;
  base_branch: string;
  required_checks: string[];
  mode: ProjectMode;
  test_command: string;
}
export type ProjectPatch = Partial<Omit<ProjectCreate, "slug">> & { enabled?: boolean; confirm?: string };

export interface Agent extends Timestamps {
  id?: ID;
  role: AgentRole;
  hermes_profile: string;
  desired_model: string | null;
  observed_model: string | null;
  observed_provider: string | null;
  sync_state: SyncState;
  observed_at: ISODate | null;
  max_tokens_per_run: number;
  enabled: boolean;
  instructions_revision: number;
}
export interface AgentPatch {
  desired_model?: string;
  max_tokens_per_run?: number;
  enabled?: boolean;
}

// Livrables (§7)
export interface PoSpec {
  summary: string;
  user_stories: string[];
  acceptance_criteria: string[];
  open_questions: string[];
}
export interface DesignDecision {
  decision: string;
  rationale: string;
  rejected_alternatives: string[];
}
export interface TechDesign {
  summary: string;
  decisions: DesignDecision[];
  risks: string[];
  tickets: { title: string; body: string; acceptance_criteria: string[] }[];
}
export interface DevelopArtifact {
  summary: string;
  files_changed: string[];
  tests: { command: string; exit_code: number }[];
  notes: string;
}
export interface ReviewFinding {
  severity: "P0" | "P1" | "P2" | "P3";
  file: string;
  message: string;
}
export interface ReviewArtifact {
  verdict: "approved" | "changes_requested";
  sha: string;
  findings: ReviewFinding[];
  summary: string;
}
export interface QaArtifact {
  verdict: "passed" | "failed";
  sha: string;
  checks: { command: string; exit_code: number }[];
  acceptance: { criterion: string; status: "met" | "not_met"; evidence: string }[];
  summary: string;
}

export interface Feature extends Timestamps {
  id: ID;
  project_id: ID;
  project?: string | { id: ID; slug: string; name?: string };
  title: string;
  description: string;
  state: FeatureState;
  state_reason: string | null;
  spec: PoSpec | null;
  design: TechDesign | null;
  version: number;
}
export interface FeatureCreate {
  project_id: ID;
  title: string;
  description: string;
}

export interface Ticket extends Timestamps {
  id: ID;
  feature_id: ID;
  project_id: ID;
  key: string;
  title: string;
  body: string;
  acceptance: string[] | null;
  state: TicketState;
  state_reason: string | null;
  issue_number: number | null;
  issue_url: string | null;
  branch: string | null;
  head_sha: string | null;
  attempt: number;
  pr_number: number | null;
  pr_url: string | null;
  ci_state: CIState;
  version: number;
}

export interface Run extends Timestamps {
  id: ID;
  job_id: ID;
  agent_role: AgentRole;
  hermes_profile: string;
  hermes_run_id: string | null;
  status: RunStatus;
  input_tokens: number | null;
  output_tokens: number | null;
  total_tokens: number | null;
  model: string | null;
  provider: string | null;
  error: string | null;
  started_at: ISODate | null;
  finished_at: ISODate | null;
  stop_attempts?: number;
  stop_error?: string | null;
  // Champs de contexte non garantis par le contrat (jointure job → sujet).
  job_kind?: string;
  subject_type?: SubjectType;
  subject_id?: ID;
  project_id?: ID;
}

export interface RunEvent {
  id?: ID;
  run_id?: ID;
  seq: number;
  event: string;
  data: unknown;
  created_at?: ISODate;
}

export interface RunDetail extends Run {
  output: string | null;
  artifact: unknown;
  events: RunEvent[];
  /** Les événements les plus anciens ont été omis (seuls les 2 000 derniers sont renvoyés). */
  events_truncated?: boolean;
}

export interface Approval extends Timestamps {
  id: ID;
  kind: ApprovalKind;
  subject_type: SubjectType;
  subject_id: ID;
  title: string;
  effect: string;
  details: Record<string, unknown> | null;
  status: ApprovalStatus;
  decided_by: string | null;
  decided_at: ISODate | null;
  decision_comment: string | null;
  executed_at: ISODate | null;
  execution_result: unknown;
}
export interface ApprovalDecision {
  decision: "approved" | "rejected";
  comment: string;
}

export interface AuditEntry {
  id: ID;
  at: ISODate;
  actor: string;
  action: string;
  subject_type: SubjectType;
  subject_id: ID | null;
  before: unknown;
  after: unknown;
  run_id: ID | null;
  sha: string | null;
}

export interface FeatureDetail extends Feature {
  tickets: Ticket[];
  approvals: Approval[];
  runs: Run[];
  timeline: AuditEntry[];
  /** Entrées les plus anciennes omises (seules les 1 000 dernières sont renvoyées). */
  timeline_truncated?: boolean;
}

export interface TicketDetail extends Ticket {
  runs: Run[];
  approvals: Approval[];
  timeline: AuditEntry[];
  timeline_truncated?: boolean;
}

// ---------- Vue d'ensemble ----------
export interface OverviewAlert {
  level: "info" | "warning" | "error" | "critical" | string;
  message: string;
  link: string | null;
}
export interface BlockedRef {
  id: ID;
  title?: string;
  key?: string;
  state_reason?: string | null;
}
export interface Overview {
  pending_approvals: number;
  blocked: { features: number | BlockedRef[]; tickets: number | BlockedRef[] };
  active_runs: number;
  tokens_today: number;
  tokens_7d: { day: string; total: number }[];
  dispatch_paused: boolean;
  alerts: OverviewAlert[];
  recent_activity: AuditEntry[];
}

// ---------- Pipeline ----------
export interface PipelineItem {
  type: "feature" | "ticket";
  id: ID;
  title: string;
  project: string;
  state: FeatureState | TicketState;
  updated_at: ISODate;
  attention: boolean;
}
export interface PipelineColumn {
  state: FeatureState | TicketState;
  label: string;
  items: PipelineItem[];
}
export interface Pipeline {
  columns: PipelineColumn[];
}

// ---------- Santé ----------
export type HealthComponent =
  | string
  | boolean
  | { status?: string; ok?: boolean; message?: string; error?: string; [k: string]: unknown };
export interface Health {
  database: HealthComponent;
  hermes: HealthComponent;
  github: HealthComponent;
  telegram: HealthComponent;
  profiles: { profile: string; status: string; [k: string]: unknown }[];
  workers: number | HealthComponent;
  schema_version: number | string;
  version: string;
  revision: string;
}

// ---------- SSE ----------
export const SSE_EVENT_TYPES = [
  "job.updated",
  "run.updated",
  "run.event",
  "approval.updated",
  "feature.updated",
  "ticket.updated",
  "overview.invalidate",
] as const;
export type SseEventType = (typeof SSE_EVENT_TYPES)[number];

export interface SsePayload {
  id?: ID;
  run_id?: ID;
  feature_id?: ID;
  ticket_id?: ID;
  seq?: number;
  event?: string;
  data?: unknown;
  [k: string]: unknown;
}
