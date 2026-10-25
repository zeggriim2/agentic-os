package httpapi

import (
	"encoding/json"
	"time"

	"github.com/zeggriim2/agentic-os/internal/store"
)

// Projections explicites : aucun secret, aucune clé d'idempotence, aucun lease owner,
// aucun chemin hôte absolu.

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func tsp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := ts(*t)
	return &s
}

func rawOrNull(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return b
}

type projectView struct {
	ID             int64    `json:"id"`
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Repository     string   `json:"repository"`
	BaseBranch     string   `json:"base_branch"`
	RequiredChecks []string `json:"required_checks"`
	Mode           string   `json:"mode"`
	Enabled        bool     `json:"enabled"`
	TestCommand    string   `json:"test_command"`
	CreatedAt      string   `json:"created_at"`
	UpdatedAt      string   `json:"updated_at"`
}

func viewProject(p *store.Project) projectView {
	rc := p.RequiredChecks
	if rc == nil {
		rc = []string{}
	}
	return projectView{p.ID, p.Slug, p.Name, p.Repository, p.BaseBranch, rc, p.Mode, p.Enabled, p.TestCommand, ts(p.CreatedAt), ts(p.UpdatedAt)}
}

type agentView struct {
	Role                 string  `json:"role"`
	HermesProfile        string  `json:"hermes_profile"`
	DesiredModel         string  `json:"desired_model"`
	ObservedModel        *string `json:"observed_model"`
	ObservedProvider     *string `json:"observed_provider"`
	SyncState            string  `json:"sync_state"`
	ObservedAt           *string `json:"observed_at"`
	MaxTokensPerRun      int     `json:"max_tokens_per_run"`
	Enabled              bool    `json:"enabled"`
	InstructionsRevision int     `json:"instructions_revision"`
	UpdatedAt            string  `json:"updated_at"`
}

func viewAgent(a *store.Agent) agentView {
	return agentView{a.Role, a.HermesProfile, a.DesiredModel, a.ObservedModel, a.ObservedProvider, a.SyncState, tsp(a.ObservedAt),
		a.MaxTokensPerRun, a.Enabled, a.InstructionsRevision, ts(a.UpdatedAt)}
}

type featureView struct {
	ID            int64   `json:"id"`
	ProjectID     int64   `json:"project_id"`
	Project       string  `json:"project"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	State         string  `json:"state"`
	PreviousState *string `json:"previous_state"`
	StateReason   string  `json:"state_reason"`
	Version       int     `json:"version"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

func viewFeature(f *store.Feature, projects map[int64]string) featureView {
	return featureView{f.ID, f.ProjectID, projects[f.ProjectID], f.Title, f.Description, f.State, f.PreviousState, f.StateReason,
		f.Version, ts(f.CreatedAt), ts(f.UpdatedAt)}
}

type ticketView struct {
	ID            int64           `json:"id"`
	FeatureID     int64           `json:"feature_id"`
	ProjectID     int64           `json:"project_id"`
	Key           string          `json:"key"`
	Title         string          `json:"title"`
	Body          string          `json:"body"`
	Acceptance    json.RawMessage `json:"acceptance"`
	State         string          `json:"state"`
	PreviousState *string         `json:"previous_state"`
	StateReason   string          `json:"state_reason"`
	IssueNumber   *int            `json:"issue_number"`
	IssueURL      *string         `json:"issue_url"`
	Branch        *string         `json:"branch"`
	HeadSHA       *string         `json:"head_sha"`
	Attempt       int             `json:"attempt"`
	PRNumber      *int            `json:"pr_number"`
	PRURL         *string         `json:"pr_url"`
	CIState       string          `json:"ci_state"`
	Worktree      string          `json:"worktree"`
	Version       int             `json:"version"`
	CreatedAt     string          `json:"created_at"`
	UpdatedAt     string          `json:"updated_at"`
}

func viewTicket(t *store.Ticket, projects map[int64]string) ticketView {
	wt := ""
	if slug := projects[t.ProjectID]; slug != "" {
		wt = slug + "/" + t.Key // relatif à AOS_WORKSPACE_ROOT
	}
	return ticketView{t.ID, t.FeatureID, t.ProjectID, t.Key, t.Title, t.Body, rawOrNull(t.Acceptance), t.State, t.PreviousState, t.StateReason,
		t.IssueNumber, t.IssueURL, t.Branch, t.HeadSHA, t.Attempt, t.PRNumber, t.PRURL, t.CIState, wt, t.Version, ts(t.CreatedAt), ts(t.UpdatedAt)}
}

type jobView struct {
	ID          int64   `json:"id"`
	Kind        string  `json:"kind"`
	SubjectType string  `json:"subject_type"`
	SubjectID   int64   `json:"subject_id"`
	Status      string  `json:"status"`
	Attempts    int     `json:"attempts"`
	MaxAttempts int     `json:"max_attempts"`
	ErrorClass  *string `json:"error_class"`
	LastError   *string `json:"last_error"`
	RunAfter    string  `json:"run_after"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

func viewJob(j *store.Job) jobView {
	return jobView{j.ID, j.Kind, j.SubjectType, j.SubjectID, j.Status, j.Attempts, j.MaxAttempts, j.ErrorClass, j.LastError,
		ts(j.RunAfter), ts(j.CreatedAt), ts(j.UpdatedAt)}
}

type runView struct {
	ID            int64           `json:"id"`
	JobID         int64           `json:"job_id"`
	AgentRole     string          `json:"agent_role"`
	HermesProfile string          `json:"hermes_profile"`
	HermesRunID   *string         `json:"hermes_run_id"`
	Status        string          `json:"status"`
	InputTokens   int64           `json:"input_tokens"`
	OutputTokens  int64           `json:"output_tokens"`
	TotalTokens   int64           `json:"total_tokens"`
	Model         *string         `json:"model"`
	Provider      *string         `json:"provider"`
	Error         *string         `json:"error"`
	HasArtifact   bool            `json:"has_artifact"`
	StartedAt     string          `json:"started_at"`
	FinishedAt    *string         `json:"finished_at"`
	StopAttempts  int             `json:"stop_attempts"`
	StopError     *string         `json:"stop_error"`
	Artifact      json.RawMessage `json:"artifact,omitempty"`
}

func viewRun(r *store.Run, withArtifact bool) runView {
	v := runView{r.ID, r.JobID, r.AgentRole, r.HermesProfile, r.HermesRunID, r.Status, r.InputTokens, r.OutputTokens, r.TotalTokens,
		r.Model, r.Provider, r.Error, len(r.Artifact) > 0, ts(r.StartedAt), tsp(r.FinishedAt), r.StopAttempts, r.StopError, nil}
	if withArtifact {
		v.Artifact = rawOrNull(r.Artifact)
	}
	return v
}

type approvalView struct {
	ID              int64           `json:"id"`
	Kind            string          `json:"kind"`
	SubjectType     string          `json:"subject_type"`
	SubjectID       int64           `json:"subject_id"`
	Title           string          `json:"title"`
	Effect          string          `json:"effect"`
	Details         json.RawMessage `json:"details"`
	Status          string          `json:"status"`
	Decision        *string         `json:"decision"`
	DecidedBy       *string         `json:"decided_by"`
	DecidedAt       *string         `json:"decided_at"`
	DecisionComment *string         `json:"decision_comment"`
	ExecutedAt      *string         `json:"executed_at"`
	ExecutionResult json.RawMessage `json:"execution_result"`
	CreatedAt       string          `json:"created_at"`
}

func viewApproval(a *store.Approval) approvalView {
	return approvalView{a.ID, a.Kind, a.SubjectType, a.SubjectID, a.Title, a.Effect, rawOrNull(a.Details), a.Status, a.Decision,
		a.DecidedBy, tsp(a.DecidedAt), a.DecisionComment, tsp(a.ExecutedAt), rawOrNull(a.ExecutionResult), ts(a.CreatedAt)}
}

type auditView struct {
	ID          int64           `json:"id"`
	At          string          `json:"at"`
	Actor       string          `json:"actor"`
	Action      string          `json:"action"`
	SubjectType string          `json:"subject_type"`
	SubjectID   *int64          `json:"subject_id"`
	Before      json.RawMessage `json:"before"`
	After       json.RawMessage `json:"after"`
	RunID       *int64          `json:"run_id"`
	SHA         *string         `json:"sha"`
}

func viewAudit(a *store.AuditEntry) auditView {
	return auditView{a.ID, ts(a.At), a.Actor, a.Action, a.SubjectType, a.SubjectID, rawOrNull(a.Before), rawOrNull(a.After), a.RunID, a.SHA}
}

func mapSlice[T, V any](in []T, f func(*T) V) []V {
	out := make([]V, 0, len(in))
	for i := range in {
		out = append(out, f(&in[i]))
	}
	return out
}

func list[T any](items []T, limit int, id func(T) int64, view func(*T) any) map[string]any {
	items, next := store.NextCursor(items, limit, id)
	return map[string]any{"items": mapSlice(items, view), "next_cursor": next}
}
