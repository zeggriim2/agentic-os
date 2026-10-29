package store

import (
	"encoding/json"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// Project est une ligne de projects.
type Project struct {
	ID             int64    `db:"id"`
	Slug           string   `db:"slug"`
	Name           string   `db:"name"`
	Repository     string   `db:"repository"`
	BaseBranch     string   `db:"base_branch"`
	RequiredChecks []string `db:"required_checks"`
	Mode           string   `db:"mode"`
	Enabled        bool     `db:"enabled"`
	TestCommand    string   `db:"test_command"`
	// AgentProfiles : rôle → profil Hermes propre au projet (absent = profil par défaut de l'agent).
	AgentProfiles map[string]string `db:"agent_profiles"`
	// AgentModels : rôle → fournisseur/modèle imposés au run (absent = desired_model de l'agent, sinon défaut du profil).
	AgentModels map[string]domain.ModelChoice `db:"agent_models"`
	// Canal Telegram du projet (groupe, sujet de forum ; vide/0 = chat par défaut).
	TelegramChatID   string `db:"telegram_chat_id"`
	TelegramThreadID int64  `db:"telegram_thread_id"`
	// IntegrationMode : mode d'intégration proposé par défaut aux nouvelles features.
	IntegrationMode string    `db:"integration_mode"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

const projectCols = `id, slug, name, repository, base_branch, required_checks, mode, enabled, test_command, agent_profiles, agent_models, telegram_chat_id, telegram_thread_id, integration_mode, created_at, updated_at`

// ProfileFor renvoie le profil Hermes à utiliser pour role : celui du projet s'il en définit un,
// sinon le profil par défaut de l'agent.
func (p *Project) ProfileFor(role, defaultProfile string) string {
	if p != nil {
		if v := p.AgentProfiles[role]; v != "" {
			return v
		}
	}
	return defaultProfile
}

// ModelFor résout le modèle imposé au run de role : celui du projet s'il en définit un, sinon le
// modèle souhaité de l'agent (sans fournisseur), sinon aucun (défaut du profil Hermes).
func (p *Project) ModelFor(role, agentDesiredModel string) domain.ModelChoice {
	if p != nil {
		if m := p.AgentModels[role]; m.Model != "" {
			return m
		}
	}
	return domain.ModelChoice{Model: agentDesiredModel}
}

// Agent est une ligne d'agents.
type Agent struct {
	ID                   int64      `db:"id"`
	Role                 string     `db:"role"`
	HermesProfile        string     `db:"hermes_profile"`
	DesiredModel         string     `db:"desired_model"`
	ObservedModel        *string    `db:"observed_model"`
	ObservedProvider     *string    `db:"observed_provider"`
	SyncState            string     `db:"sync_state"`
	ObservedAt           *time.Time `db:"observed_at"`
	MaxTokensPerRun      int        `db:"max_tokens_per_run"`
	Enabled              bool       `db:"enabled"`
	InstructionsRevision int        `db:"instructions_revision"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
}

const agentCols = `id, role, hermes_profile, desired_model, observed_model, observed_provider, sync_state, observed_at, max_tokens_per_run, enabled, instructions_revision, created_at, updated_at`

// Feature est une ligne de features.
type Feature struct {
	ID            int64           `db:"id"`
	ProjectID     int64           `db:"project_id"`
	Title         string          `db:"title"`
	Description   string          `db:"description"`
	State         string          `db:"state"`
	PreviousState *string         `db:"previous_state"`
	StateReason   string          `db:"state_reason"`
	Spec          json.RawMessage `db:"spec"`
	Design        json.RawMessage `db:"design"`
	Version       int             `db:"version"`
	// IntegrationMode : direct ou feature_branch (domain.Integration*).
	IntegrationMode string `db:"integration_mode"`
	// FeatureBranch : branche de feature (mode feature_branch), fixée au feu vert ou à la bascule.
	FeatureBranch *string `db:"feature_branch"`
	// FeaturePRNumber/URL : PR finale branche de feature → base.
	FeaturePRNumber *int      `db:"feature_pr_number"`
	FeaturePRURL    *string   `db:"feature_pr_url"`
	CreatedAt       time.Time `db:"created_at"`
	UpdatedAt       time.Time `db:"updated_at"`
}

// TargetBranch renvoie la branche que visent les tickets de la feature : sa branche de feature en
// mode feature_branch (une fois fixée), sinon la branche de base du projet.
func (f *Feature) TargetBranch(base string) string {
	if f != nil && f.IntegrationMode == "feature_branch" && f.FeatureBranch != nil && *f.FeatureBranch != "" {
		return *f.FeatureBranch
	}
	return base
}

const featureCols = `id, project_id, title, description, state, previous_state, state_reason, spec, design, version, integration_mode, feature_branch, feature_pr_number, feature_pr_url, created_at, updated_at`

// Ticket est une ligne de tickets.
type Ticket struct {
	ID            int64           `db:"id"`
	FeatureID     int64           `db:"feature_id"`
	ProjectID     int64           `db:"project_id"`
	Key           string          `db:"key"`
	Seq           int             `db:"seq"`
	Title         string          `db:"title"`
	Body          string          `db:"body"`
	Acceptance    json.RawMessage `db:"acceptance"`
	State         string          `db:"state"`
	PreviousState *string         `db:"previous_state"`
	StateReason   string          `db:"state_reason"`
	IssueNumber   *int            `db:"issue_number"`
	IssueURL      *string         `db:"issue_url"`
	Branch        *string         `db:"branch"`
	HeadSHA       *string         `db:"head_sha"`
	Attempt       int             `db:"attempt"`
	PRNumber      *int            `db:"pr_number"`
	PRURL         *string         `db:"pr_url"`
	CIState       string          `db:"ci_state"`
	Version       int             `db:"version"`
	CreatedAt     time.Time       `db:"created_at"`
	UpdatedAt     time.Time       `db:"updated_at"`
}

const ticketCols = `id, feature_id, project_id, key, seq, title, body, acceptance, state, previous_state, state_reason, issue_number, issue_url, branch, head_sha, attempt, pr_number, pr_url, ci_state, version, created_at, updated_at`

// Job est une ligne de jobs.
type Job struct {
	ID             int64           `db:"id"`
	Kind           string          `db:"kind"`
	SubjectType    string          `db:"subject_type"`
	SubjectID      int64           `db:"subject_id"`
	ProjectID      *int64          `db:"project_id"`
	Status         string          `db:"status"`
	Attempts       int             `db:"attempts"`
	MaxAttempts    int             `db:"max_attempts"`
	RunAfter       time.Time       `db:"run_after"`
	LeaseOwner     *string         `db:"lease_owner"`
	LeaseExpiresAt *time.Time      `db:"lease_expires_at"`
	IdempotencyKey string          `db:"idempotency_key"`
	LastError      *string         `db:"last_error"`
	ErrorClass     *string         `db:"error_class"`
	Payload        json.RawMessage `db:"payload"`
	CreatedAt      time.Time       `db:"created_at"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

const jobCols = `id, kind, subject_type, subject_id, project_id, status, attempts, max_attempts, run_after, lease_owner, lease_expires_at, idempotency_key, last_error, error_class, payload, created_at, updated_at`

// Run est une ligne de runs.
type Run struct {
	ID               int64           `db:"id"`
	JobID            int64           `db:"job_id"`
	AgentRole        string          `db:"agent_role"`
	HermesProfile    string          `db:"hermes_profile"`
	HermesRunID      *string         `db:"hermes_run_id"`
	Status           string          `db:"status"`
	InputTokens      int64           `db:"input_tokens"`
	OutputTokens     int64           `db:"output_tokens"`
	TotalTokens      int64           `db:"total_tokens"`
	CacheReadTokens  int64           `db:"cache_read_tokens"`
	CacheWriteTokens int64           `db:"cache_write_tokens"`
	Model            *string         `db:"model"`
	Provider         *string         `db:"provider"`
	Output           *string         `db:"output"`
	Artifact         json.RawMessage `db:"artifact"`
	Error            *string         `db:"error"`
	StartedAt        time.Time       `db:"started_at"`
	FinishedAt       *time.Time      `db:"finished_at"`
	StopRequestedAt  *time.Time      `db:"stop_requested_at"`
	StopAttempts     int             `db:"stop_attempts"`
	StopError        *string         `db:"stop_error"`
	// Surcharge de modèle demandée à Hermes (nil = défaut du profil) et repli sur ce défaut après refus du fournisseur.
	RequestedModel    *string   `db:"requested_model"`
	RequestedProvider *string   `db:"requested_provider"`
	ModelFallback     bool      `db:"model_fallback"`
	CreatedAt         time.Time `db:"created_at"`
	UpdatedAt         time.Time `db:"updated_at"`
}

const runCols = `id, job_id, agent_role, hermes_profile, hermes_run_id, status, input_tokens, output_tokens, total_tokens, cache_read_tokens, cache_write_tokens, model, provider, output, artifact, error, started_at, finished_at, stop_requested_at, stop_attempts, stop_error, requested_model, requested_provider, model_fallback, created_at, updated_at`

// Delivery est une livraison de webhook GitHub.
type Delivery struct {
	ID            int64           `db:"id"`
	DeliveryID    string          `db:"delivery_id"`
	Event         string          `db:"event"`
	Status        string          `db:"status"`
	Attempts      int             `db:"attempts"`
	Payload       json.RawMessage `db:"payload"`
	LastError     *string         `db:"last_error"`
	NextAttemptAt *time.Time      `db:"next_attempt_at"`
}

const deliveryCols = `id, delivery_id, event, status, attempts, payload, last_error, next_attempt_at`

// RunEvent est une ligne de run_events.
type RunEvent struct {
	ID        int64           `db:"id"`
	RunID     int64           `db:"run_id"`
	Seq       int             `db:"seq"`
	Event     string          `db:"event"`
	Data      json.RawMessage `db:"data"`
	CreatedAt time.Time       `db:"created_at"`
}

// Approval est une ligne d'approvals.
type Approval struct {
	ID              int64           `db:"id"`
	Kind            string          `db:"kind"`
	SubjectType     string          `db:"subject_type"`
	SubjectID       int64           `db:"subject_id"`
	Title           string          `db:"title"`
	Effect          string          `db:"effect"`
	Details         json.RawMessage `db:"details"`
	Status          string          `db:"status"`
	Decision        *string         `db:"decision"`
	DecidedBy       *string         `db:"decided_by"`
	DecidedAt       *time.Time      `db:"decided_at"`
	DecisionComment *string         `db:"decision_comment"`
	ExecutedAt      *time.Time      `db:"executed_at"`
	ExecutionResult json.RawMessage `db:"execution_result"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

const approvalCols = `id, kind, subject_type, subject_id, title, effect, details, status, decision, decided_by, decided_at, decision_comment, executed_at, execution_result, created_at, updated_at`

// AuditEntry est une ligne d'audit_log.
type AuditEntry struct {
	ID          int64           `db:"id"`
	At          time.Time       `db:"at"`
	Actor       string          `db:"actor"`
	Action      string          `db:"action"`
	SubjectType string          `db:"subject_type"`
	SubjectID   *int64          `db:"subject_id"`
	Before      json.RawMessage `db:"before"`
	After       json.RawMessage `db:"after"`
	RunID       *int64          `db:"run_id"`
	SHA         *string         `db:"sha"`
}

const auditCols = `id, at, actor, action, subject_type, subject_id, before, after, run_id, sha`

// Session est une session utilisateur.
type Session struct {
	ID        int64     `db:"id"`
	TokenHash string    `db:"token_hash"`
	Login     string    `db:"login"`
	CSRFToken string    `db:"csrf_token"`
	ExpiresAt time.Time `db:"expires_at"`
}
